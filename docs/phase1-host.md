# 第一阶段：桌面宿主基础能力

实现版本：宿主 `0.1.0`，SDK 协议 `1`，Wails `v3.0.0-beta.33`。

## 配置

完整配置见根目录 `settings.example.json`。新增字段未提供时使用默认值：

| 字段 | 默认值 | 作用 |
| --- | --- | --- |
| singleInstance | true | 按用户数据目录区分实例，重复启动唤醒主窗口，不重复执行启动参数 |
| closeBehavior | exit | exit：关闭主窗口退出整个应用；hide：隐藏主窗口并提供托盘恢复、退出菜单 |
| rememberWindows | true | 保存具名窗口的普通状态位置、尺寸及最大化状态 |
| minWidth / minHeight | 800 / 600 | 主窗口最小尺寸；显示器工作区更小时以可见区域为准 |
| loadTimeoutSeconds | 30 | 页面加载看门狗，范围 1–300 秒 |
| externalLinks | browser | 导航范围外的 HTTP(S) 链接在系统浏览器打开；block 则拒绝 |
| navigationOrigins | [] | 可在应用窗口中展示的额外来源，不自动授予 RPC 权限 |
| permissions | {} | 为指定来源覆盖能力集合，空数组表示全部拒绝 |

窗口状态写入用户数据目录 `window-state.json`，使用临时文件、同步及替换写入，不保存弹框状态。具名窗口重新创建时恢复状态；显示器移除后按现有工作区重新定位。关闭的子窗口不会在下次启动时自动重新打开。

`hide` 模式关闭窗口不会取消正在执行的任务。退出菜单才会触发任务检查。系统终止进程、崩溃或强制关机不受业务任务保护。

## SDK

修改 Go 服务后生成内置与独立接入包：

```shell
wails3 generate bindings -clean=true
pnpm --dir frontend run build
node scripts/generate-hybrid.mjs
```

外部前端复制整个 `hybrid/`，包括 `sdk-core.js`。保留旧的 UI、Store、Log、Runtime 导出，新增 Host、Windows 和 Hybrid。

```javascript
import { Hybrid, Host, Windows, UI } from '/native/client.js';

const info = await Hybrid.connect({
  timeoutMs: 5000,
  requiredCapabilities: ['host', 'windows', 'lifecycle']
});
console.log(info.version, info.protocolVersion, info.platform, info.capabilities);

// 相同 name 复用并聚焦已有窗口，首次创建时应用传入配置。
// 要打开不同详情，请使用不同 name，或由业务事件驱动已有窗口更新。
const detail = await Hybrid.call(() => Windows.Open({
  name: 'order-123', title: '订单详情', url: '/orders/123', width: 1000, height: 720
}));
await Hybrid.call(() => Windows.Focus(detail.id));
const windows = await Hybrid.call(() => Windows.List());
await Hybrid.call(() => Windows.Close(detail.id));

// 自动登记和释放退出保护任务。业务操作仍由调用方执行和取消。
await Hybrid.withTask('导出报表', async () => {
  await exportReport();
});

const aborter = new AbortController();
const pending = Hybrid.call(() => UI.Confirm({ body: '继续导出？' }), {
  timeoutMs: 15000, signal: aborter.signal
});
// 页面卸载或用户主动取消时：aborter.abort();
await pending;
```

`Hybrid.call` 使用毫秒计时，默认 30 秒；超时或 AbortSignal 触发后会取消底层 CancellablePromise。原始 bindings 继续保留 Wails 取消语义；需要统一错误码时使用 `Hybrid.call`。只有 `Confirm` 等支持 context 的持续请求能在取消后清理持续工作，已完成的即时操作不会撤回。

`Hybrid.connect` 校验协议版本和必需能力，成功结果在当前页面复用；失败后可重试。它判断 RPC 是否可用，不依赖仅检查某个全局变量。普通浏览器没有宿主服务，管理页面会展示连接错误，不会伪造窗口列表。

手动任务接口为 `Host.BeginTask(label)`、`Host.EndTask(id)`、`Host.GetPendingTasks()`。任务归属于发起调用的原生窗口，其他窗口不能结束它。`Host.Quit()`、`Windows.Close()` 在有相关任务时返回 `E_BUSY`；点击原生关闭按钮会允许用户明确确认继续退出/关闭。刷新和宿主处理的页面导航也会检查本窗口任务。业务应在 finally 中释放任务；管理页面可手动结束属于当前窗口的遗留任务。

## 导航与权限

默认允许本地宿主来源、主页面来源和 allowedOrigins 中的来源使用第一阶段能力：

- host：宿主信息、加载状态、重试。
- windows：窗口管理及 Wails 窗口操作。
- ui：纯文字消息和确认框。
- ui.html：可信 HTML 浮窗，HTML 不做清洗，只能使用受信任应用代码。
- store / log：已有 KV、日志服务。
- lifecycle：任务与退出管理。
- events：Wails 事件发送，确认框返回也需要此能力。

例如，让报表页面仅访问宿主信息和存储：

```json
{
  "url": "https://app.example.com/",
  "navigationOrigins": ["https://reports.example.com"],
  "permissions": {
    "https://reports.example.com": ["host", "store"]
  }
}
```

permissions 的来源也加入 CORS 允许列表。额外业务窗口需要交互弹框时，至少按需授予 host、windows、ui、events；HTML 浮窗再额外授予 ui.html。独立前端的 CSP 必须允许宿主模块和连接地址。

宿主在 `/wails/runtime` 检查来源、能力、方法名或 ID，直接绕过 SDK 调用 bindings 也受同样限制。空 Origin、opaque Origin、未知方法、未授权能力均拒绝。第一阶段不开放原始 Wails Application.Quit、Clipboard、Dialog、Browser 等子系统；使用宿主接口执行生命周期和外部链接策略。F12 同时受 debug 配置控制。

本阶段单次 RPC 最大 1 MiB，分块请求明确拒绝，避免分块绕过方法检查。后续文件与下载应使用专用通道，不把大文件放进 RPC。

受控链接和 window.open 按导航策略分流，页面加载完成后根据原生提供的来源元数据再次校验。直接 location 跳转、服务器重定向等可能在校验前已发出网络请求；这里不是网络防火墙或任意网页隔离沙箱。原生 RPC 按实际 HTTP Origin 授权，不因原窗口曾经受信任就继承权限。配置的可信来源之间不提供账号/租户隔离。自定义 transport 若绕过默认 HTTP 管道，必须实现等效授权上下文，不会自动继承权限。

## 加载与失败恢复

主窗口与普通子窗口从本地 `/shell/status.html` 启动，再加载目标地址。原生加载完成时发送带来源校验的就绪通知；SDK 也可调用 `Host.Ready()`。加载超时进入本地错误页，显示原因、重试按钮和返回内置首页按钮。返回内置首页不会覆盖配置文件中的远程 URL。

普通 HTTP 错误页面、业务页面自身的 JS 错误与宿主加载超时并非同一概念；若服务器正常返回一个错误页面，业务可自行展示错误。重定向到导航范围外的来源会进入导航拒绝页。

首页新增“宿主管理”入口，对应 `/shell/manage.html`，展示宿主版本、协议、平台、能力、窗口列表和未完成任务。示例窗口使用固定 ID，可直接验证复用。

## 验证

```shell
go test ./...
go vet ./...
node --test scripts/hybrid-client.test.mjs scripts/sdk-core.test.mjs
pnpm --dir frontend run build
wails3 task build
```

自动化覆盖来源授权、直接 methodID/methodName 调用、未开放 runtime 子系统、分块与大小限制、窗口复用、任务归属及退出保护、状态损坏恢复、负坐标/移除显示器/小工作区，以及 SDK 协议、超时、取消和 finally 释放。

原生验收应分别检查：重复启动唤醒、窗口复用与关闭、任务取消退出、hide 模式托盘恢复、无响应地址的超时/重试/返回首页、最大化状态恢复、缩放与高 DPI。Windows 为本次实机验证目标；macOS/Linux 原生行为仍需在相应平台验收。

## 2026-10-24 收尾验证

- 子窗口与 HTML 浮窗继承创建者的权限上限；导航到本地页面也不能扩大权限。窗口关闭后撤销权限注册。
- 任务所属窗口依据原生注入的窗口 ID 确定，不能通过目标窗口名称请求头冒充其他窗口。
- 修复管理页面板在较矮客户区内被压缩裁切的问题；重试过程显示加载反馈。
- 自动化增加本地子窗口权限提升、目标窗口名称伪造、过期加载超时与损坏状态文件等回归检查。
- Windows 原生已验证 SDK 握手、管理页面、固定 ID 创建窗口、加载超时/重试/返回首页、隐藏后进程保留，以及单实例唤醒；完整测试命令见上节。
- 显示器移除和负坐标通过工作区算法测试覆盖；macOS/Linux、物理多显示器热插拔与多 DPI 组合仍需对应设备验收。
- 本轮另外通过 `wails3 dev` 实机验收：开发模式来源 `http://wails.localhost:9245` 能正常握手、查询窗口并登记/释放任务；管理页表格完整显示，页面较长时仅主内容区滚动。发布模式也已构建并启动验证。
