# 第二阶段：业务前端桌面能力

补充：Shell 命令、共享缓存、事件回调与请求响应、环境检测已加入“业务桥接”，详见 [接入说明](business-bridge.md)。

入口：工作台侧栏的“桌面能力”，或 `/shell/desktop.html`。宿主管理页和托盘也提供入口。新接口统一从 SDK 的 `Desktop` 导出，错误使用 `HybridError.code` 判断。

## 授权与启用

默认配置保持第一阶段的权限，新能力不会因页面被加入 `allowedOrigins` 而自动开放。内置页面按 `desktopCapabilities` 开启；远程页面必须在 `permissions` 中列出完整能力，显式列表会覆盖默认值。子窗口始终受创建者能力上限约束。

```json
{
  "desktopCapabilities": ["files", "downloads", "clipboard", "notifications", "shortcuts", "settings", "credentials", "protocol"],
  "trayEnabled": true,
  "permissions": {
    "https://app.example.com": ["host", "windows", "lifecycle", "events", "files", "downloads", "clipboard"]
  }
}
```

开发体验可复制 `settings.example.json` 到自己的配置文件，再通过 `HYBRID_SETTINGS` 指定后执行 `wails3 dev`。无需覆盖现有 `settings.json`。`trayEnabled` 可以独立开启托盘；`closeBehavior: "hide"` 始终开启托盘，避免隐藏后无法找回窗口。

| 能力 | SDK 方法 | 关键行为 |
| --- | --- | --- |
| files | SelectFile / ReadFile / WriteFile / RevealFile / ReleaseFile | 原生文件、目录、保存选择；当前窗口的临时 token 授权 |
| files | ListDirectory / OpenChild | 最多 500 个直接条目，子项只读，拒绝目录跳转和符号链接 |
| downloads | StartDownload / ListDownloads / CancelDownload / RetryDownload | 流式下载、字节进度、取消、从头重试、退出保护 |
| downloads | OpenDownload / RemoveDownload | 完成后打开或定位；仅清理任务记录，不删除文件 |
| clipboard | ClipboardText / ClipboardImage | 文本读写与清空，PNG 图像读写，明确报告占用与格式错误 |
| notifications | Notify | 系统通知，点击后唤醒主窗口并尝试进入站内路由 |
| shortcuts | SetShortcut | 应用内或全局快捷键，注册和注销，冲突返回 E_CONFLICT |
| protocol | ConfigureProtocol | 显式注册或移除当前用户的固定 hybrid-infra 协议 |
| settings | GetSettings / SaveSettings | 校验并原子保存启动配置，返回 restartRequired |
| settings | SavePreferences / ResetPreferences | 即时偏好：主题、语言、通知开关 |
| credentials | Credential | 系统凭据库读写与删除，按数据目录和来源隔离 |

## 文件与下载接入

```javascript
import { Desktop, Hybrid } from '/native/client.js';

await Hybrid.connect({ requiredCapabilities: ['files', 'downloads'] });
// 对话框允许用户停留较长时间，按业务需要增加等待时间。
const target = await Hybrid.call(
  () => Desktop.SelectFile('save', 'report.csv'),
  { timeoutMs: 300000 },
);
const task = await Desktop.StartDownload('https://example.com/report.csv', target.token);
const downloads = await Desktop.ListDownloads(); // 每秒轮询即可；received / total 为字节。
await Desktop.CancelDownload(task.id); // 异步取消，等待状态进入 cancelled 后才能重试。
```

- `SelectFile(mode, name)` 的 mode 为 `open`、`directory` 或 `save`。取消返回 `E_CANCELLED`。保存选择代表用户授权覆盖此目标；只有 save 凭证可写。
- 文件路径仅展示，后续接口不接受任意路径。token 使用系统随机源生成，绑定当前来源及原生窗口，关闭窗口后撤销。不是针对本机恶意进程并发替换文件的 OS 沙箱。
- `ReadFile` / `WriteFile` 的二进制数据是 Base64 字符串，单次最多 512 KiB。UTF-8 文本需用 `TextEncoder` / `TextDecoder` 转换；大型业务文件应使用流式接口。
- `OpenChild(directoryToken, name)` 只能打开直接子项；不允许 `../`、绝对路径、ADS、符号链接等绕过。子目录可继续列出。
- 写文件先写同目录临时文件、同步磁盘后原子替换；失败清理临时内容。
- 下载最多同时 4 个、历史最多 100 条、单文件最多 2 GiB，30 分钟超时。只接受 HTTP(S)，校验重定向，不自动附带业务 Cookie 或登录凭据，允许授权业务访问本地网络地址。
- 下载自行登记/释放宿主退出保护任务，无须业务额外 BeginTask。确认退出或关闭所属窗口会取消传输。取消不删除之前已存在的目标文件。
- `RetryDownload` 仅适用于 failed / cancelled，创建新任务并从头下载，不支持断点续传。历史记录仅保留在当前窗口会话中。
- `OpenDownload(id, true)` 定位文件，`false` 用系统默认程序打开。`RemoveDownload` 会在没有其他下载引用时释放目标凭证；不会删除文件。
- 使用文件结束后调用 `ReleaseFile`；运行中的下载目标不能释放，需先取消并等待收尾。

## 系统集成

```javascript
await Desktop.ClipboardText('write', '已复制的内容');
const text = await Desktop.ClipboardText('read', '');
await Desktop.Notify('导出完成', '点击返回订单页', '/orders');
await Desktop.SetShortcut('Ctrl+Shift+Alt+H', '/#workspace', true, true);
// 第三个参数为是否全局；第四个参数为注册或注销。
await Desktop.SetShortcut('Ctrl+Shift+Alt+H', '', true, false);
await Desktop.ConfigureProtocol(true);
// 浏览器或系统入口：hybrid-infra://open?route=%2Forders%3Fid%3D42
```

通知、快捷键和协议只接受 `/` 开头的站内路径或 `#` 路由，基于配置中的主页面 URL 解析；不执行任意系统命令或打开任意远程地址。主窗口有未完成任务时拒绝替换页面，以保护业务状态。通知保留最近 128 条点击路由，较旧通知不再恢复站内路由。

托盘包含主窗口、宿主管理、桌面能力入口和未完成任务数量，退出仍受任务确认保护。快捷键只接受 `Ctrl+Shift+[Alt+]字母或数字`，有效期为本次应用运行；操作系统冲突以 `E_CONFLICT` 返回。快捷键和通知入口为应用级资源，不随注册页面关闭而消失。

Windows 的协议注册仅修改当前用户的固定 `hybrid-infra` 项，不需要管理员权限；检测到其他安装路径时拒绝覆盖。移除时也核对归属。开发二进制路径改变前先移除原注册。协议初次启动和第二实例转交均可处理；`singleInstance: true` 时复用现有主窗口。

图像剪贴板支持 PNG（最多 512 KiB、400 万像素），写入同时提供 CF_DIB 兼容格式；读取兼容无压缩 24/32 位 CF_DIB。不是所有截图程序的私有/压缩格式都受支持。

## 设置和系统凭据

```javascript
const snapshot = await Desktop.GetSettings();
await Desktop.SavePreferences({ theme: 'system', locale: 'zh-CN', notifications: true });
await Desktop.SaveSettings({ ...snapshot.saved, trayEnabled: true });
// 返回 active / saved / preferences / restartRequired；active 在当前进程保持不变。
await Desktop.Credential('access-token', '业务令牌', 'set');
const token = await Desktop.Credential('access-token', '', 'get');
await Desktop.Credential('access-token', '', 'delete');
```

偏好保存在用户数据目录 `preferences.json`，主题为 system / light / dark，语言为 zh-CN / en-US。通知开关立即由宿主执行；主题和语言由业务页面读取后应用，外壳不会改写远程业务的样式或文本。演示控制台展示主题选择，但不提供整套业务翻译。恢复默认仅恢复偏好，不删除启动权限、下载文件或凭据。

启动配置写入当前配置查找规则选中的文件；尚无文件时写入用户数据目录的 settings.json。完整校验通过后才原子替换。窗口、来源、能力、托盘等统一重启生效，避免运行中并发策略不一致。

凭据使用 Windows 通用凭据管理器，最多 2560 字节。名称为 1–80 位 ASCII 字母、数字、点、下划线或连字符。按用户、数据目录和页面来源隔离，同来源不同窗口可以共享登录态。秘密不会进入普通 KV、偏好文件或宿主日志；不要把长期秘密写进启动配置或通知正文。

## 验证与平台范围

自动测试覆盖按需授权、窗口归属、读写上限、目录跳转拒绝、窗口关闭撤销、下载成功/失败/取消/重试和临时文件清理、退出保护释放、配置原子保存与重启状态、路由校验、PNG/DIB 像素转换，以及 Windows 原生凭据写读删与数据目录隔离。测试使用独立临时目录和本地 HTTP 服务，系统凭据使用随机测试名称并在退出时删除。

Windows 是本阶段实际构建及原生窗口检查的平台。已实机检查 1200×800 原生布局、内部滚动、文件对话框取消返回 `E_CANCELLED`，以及第二实例携带协议参数后唤醒原窗口并进入 `/#workspace`。文件对话框、下载、文本剪贴板和设置复用 Wails/Go 跨平台接口；文件定位、图像剪贴板、系统凭据与协议注册目前只有 Windows 适配，其他平台返回 `E_UNSUPPORTED`，不会降级为明文秘密存储。通知由 Wails 原生服务实现，显示取决于系统设置；当前通知点击路由只保存在运行中的进程内，不保证退出后点击旧通知恢复路由。未对真实通知弹出/点击、系统协议关联和跨应用全局快捷键完成自动端到端验收。

`go test -race` 在当前环境因 CGO 关闭而不可运行；普通并发行为通过下载集成测试验证。后续跨平台发布前需补齐对应原生适配和实机验收。
