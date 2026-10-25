# 应用图标与运行时替换

新图标以深青色外壳承载白色、薄荷绿色的 H，中央连接表达 Web 与桌面的桥接。矢量母版为 `frontend/public/app-icon.svg`，桌面工作台品牌区和 favicon 使用同一设计。

## 替换范围

| 位置 | 实现 | 配置动态替换 |
| --- | --- | --- |
| 系统托盘 | Wails `SystemTray.SetIcon`，浅色和深色主题使用同一套配置 | 支持，无需重启 |
| Windows 标题栏、Alt+Tab、运行中的窗口图标 | 宿主向原生窗口发送 `WM_SETICON`，同时设置大、小图标 | 支持，无需重启 |
| Windows 任务栏按钮 | 使用窗口图标；默认构建也嵌入新品牌资源 | 普通运行窗口支持；已固定快捷方式、分组显示可能继续使用快捷方式资源和系统缓存 |
| EXE 在资源管理器中的文件图标 | `build/windows/icon.ico` 编译进 EXE | 需重新构建，配置不会改写 EXE |
| macOS 打包图标 | `build/darwin/icons.icns` | 打包资源已更新；运行时走 Wails `App.SetIcon`，本次未做 macOS 原生验收 |
| Linux 应用图标 | Wails `App.SetIcon` | 已接适配，桌面环境显示效果未做原生验收 |

当前锁定的 Wails v3.0.0-beta.33 中，Windows `App.SetIcon` 的底层实现是空函数，不能仅调用它就宣称任务栏图标已改变。因此本项目单独实现了 Windows 窗口图标适配。

Windows 接口依据：[WM_SETICON](https://learn.microsoft.com/zh-cn/windows/win32/winmsg/wm-seticon)、[微软关于 PNG 图标资源的说明](https://devblogs.microsoft.com/oldnewthing/20231025-00/?p=108925)。不修改已经固定的快捷方式，不清空用户的图标缓存。

## 配置示例

将以下字段合入正在使用的配置文件，例如通过 `HYBRID_SETTINGS` 指定的 JSON：

```json
{
  "icons": {
    "application": "assets/company.png",
    "tray": "https://static.example.com/brand/tray.png"
  }
}
```

- `application`：应用窗口图标。支持本机绝对路径、相对路径和 HTTPS URL。
- `tray`：托盘单独使用的图标。配置了 `application` 而 `tray` 留空时，托盘跟随应用图标；两项均为空时，使用各自内置品牌图标。
- 相对路径以**实际配置文件所在目录**为基准，与启动工作目录无关。没有配置文件时，以 EXE 所在目录为基准。
- 图像只接受 **16–1024 像素的正方形 PNG，编码文件不超过 2 MiB**。透明背景推荐用于图标。ICO 和 SVG 是构建素材格式，不直接作为运行时配置输入。
- 公网使用 HTTPS；本机测试允许 localhost、127.0.0.1、::1 的 HTTP。拒绝 URL 用户名/密码、片段、UNC 网络路径及 `file:`、`data:` 等地址。重定向最多 3 次，不能跨来源或降级协议。
- 每张远程图标最多等待 8 秒；不主动轮询 URL 或监视磁盘文件。两个资源都读取并校验成功才替换，失败保留当前图标。启动配置加载失败时保留内置图标并记录错误码。

实际根目录 `settings.json` 不会因本次改动被覆盖，完整参考示例见 `settings.example.json`。

## 无需重启重新加载

1. 修改配置文件的 `icons` 字段并保存；也可在「桌面能力 → 偏好与启动设置 → 启动配置」中编辑后先点击「校验并保存启动配置」。
2. 点击「重新加载应用与托盘图标」。调用窗口需要 `settings` 权限；该接口不接受任意地址参数，而是读取固定配置文件。
3. 新图标立即用于现有宿主管理窗口和托盘。之后新建的宿主窗口会继承当前图标。配置其他字段仍遵循原来的重启生效规则。

```javascript
import { Desktop, Hybrid } from '/native/client.js';
const snapshot = await Hybrid.call(() => Desktop.ReloadIcons(), { timeoutMs: 30000 });
console.log(snapshot.active.icons); // 最近成功应用的图标配置
```

`GetSettings()` 的 `active.icons` 反映已应用配置，`saved.icons` 反映磁盘配置；只修改图标并成功重载时，不会再因图标差异提示需要重启。清空两项并重载可恢复内置品牌图标。

默认品牌也替换了前端侧栏与 favicon，但运行时 `icons.application` 只更改原生图标，不会修改远程业务页面的 DOM、favicon 或 EXE 资源。

## 更新品牌素材

编辑矢量母版后，在项目根目录执行：

```shell
pnpm --dir frontend format
pnpm --dir frontend icons
wails3 task common:generate:icons
wails3 task build
```

生成器使用锁定版本的 Sharp，把母版生成：

- `build/appicon.png`：1024 像素构建图标。
- `frontend/public/app-icon.png`：256 像素内置应用图标。
- `frontend/public/tray-icon.png`：64 像素内置托盘图标。
- `frontend/public/favicon.svg`：母版副本。

Wails 随后生成 Windows 多尺寸 ICO 和 macOS ICNS。不要直接修改这些生成文件来更新品牌，应修改母版并重生成。

## 资源与验证

Windows 大、小 HICON 各自创建，现有窗口全部切换后才释放旧句柄；不会销毁 Wails 自带的借用资源。窗口后续加载时应用当前句柄，应用退出时先解除窗口引用再统一释放。

已验证：本地相对路径、托盘跟随和独立覆盖、默认恢复、损坏/超大 PNG 拒绝、回环 HTTP 下载、错误响应、跨源重定向拒绝、取消、Windows HICON 创建和释放，以及原生窗口运行中从默认配色切换到临时测试配色。坏地址返回 `E_ICON_LOAD` 并保留当前图标，清空后可恢复默认；独立测试数据放在 `.task`，不改写用户配置。托盘通过 Wails 动态图标接口更新，固定任务栏快捷方式的缓存表现未做验收。
