# 内置开发文档界面

开发文档已经嵌入程序，地址为 `/shell/docs.html`。左侧采用「功能大类 → 功能点」两级折叠菜单，右侧展示介绍、能力要求、接口签名、参数与默认值、可复制示例、返回值及使用边界。页面离线可读，浏览器预览不依赖原生连接。

## 功能分类

| 大类 | 功能点 |
| --- | --- |
| 接入与配置 | 页面来源与本地 HTML、快速接入、环境判断、来源授权、调用超时与取消、配置和偏好 |
| 界面 UI | 轻量通知、消息弹框、确认框、自定义 HTML 窗口、启动屏、动态图标 |
| 桌面能力 | 文件选择和读写、目录浏览、下载、剪贴板、截图、系统通知、快捷键、协议唤醒、凭据库 |
| 命令与通信 | Shell 执行和输出回调、内存缓存与 CAS、事件监听、异步请求返回、持久化 Store |
| 宿主与窗口 | 具名窗口、任务退出保护、加载恢复、托盘和单实例、文档与调试入口、日志 |
| 维护与发布 | 诊断导出、备份恢复、签名更新和回滚、迁移与离线发布 |

搜索支持中文、API 名及权限名，标题和接口匹配优先；`Ctrl+K` 或 `/` 聚焦搜索，Escape 退出搜索。功能页有独立哈希地址，例如 `/shell/docs.html#notify`、`#screenshot`、`#event-requests`。窄窗口使用可展开侧栏，主内容与目录分别滚动。

概览 / 功能地图的「全部权限」列出宿主当前支持的全部 25 项权限、用途以及是否默认提供。「一键复制全部权限」复制完整 JSON 数组，可用于 `permissions` 下对应来源的列表；「复制内置页面配置」复制 `desktopCapabilities` 配置片段，仅列出需要显式开启的扩展权限，与默认基础权限合并后覆盖全部能力。复制不会修改实际配置，修改后重启生效；Shell 程序仍需配置 `shellPrograms` 白名单。测试会与 Go 权限清单对照，防止漏列。

每个方法都提供「运行示例」，按当前表单参数调用真实 SDK，并显示返回值、错误码及回调输出。打开页面、编辑参数和复制代码不会自动调用；点击运行才会产生对应桌面操作。复制依赖用户点击；浏览器拒绝剪贴板时选中代码并提示手动复制。

有参数的方法支持文字、数字、布尔开关、JSON 和可编辑的回调函数，填写时同步生成可复制代码。无参数方法也可直接运行。JSON、数字或布尔格式错误时禁用运行和复制。函数模板在点击运行时编译，语法和执行错误显示到结果面板；这是开发者主动运行的页面代码，与当前文档页具有同样权限，不是隔离沙箱。凭据方法不在结果面板输出秘密值。清空 Store、恢复备份、安装更新、回滚和退出应用会先提示实际影响并确认。

事件 on/once/handle 运行后保留订阅，支持「触发测试事件」或「发起测试请求」，直接查看收到的参数及异步返回。切换功能目录保留活动监听和运行结果，可回到原方法继续查看；「停止 / 清理」或顶部「停止全部示例」释放监听、文件 token、任务登记、示例注册的快捷键，并请求停止后台命令。离页时尽力清理，宿主窗口归属策略继续兜底。Shell.run 的标准输出和错误输出可在当前结果面板中查看。

参数草稿和运行结果仅在当前文档页内存中保留，不写入磁盘，可点击「恢复示例值」重置参数。文件 token 必须先在本页运行 SelectFile/OpenChild 获取，再填入读写示例；命令、下载、备份等 ID 从相应运行结果获取。文件授权不能跨窗口使用。停止清理后旧 token 不再可用。浏览器预览可运行环境判断和错误转换等本地方法，其他示例需要从外壳打开文档。

## 打开方式

1. 工作台侧栏「开发文档」。
2. 开启托盘后，托盘菜单「开发文档」。配置 `trayEnabled: true`，或使用 `closeBehavior: "hide"`。
3. 文档右上角「独立窗口」。
4. 原生页面加载完成后，在 F12 Console 输入 `hybridDocs()`。
5. 业务前端导入 SDK 后调用：

```javascript
const { Hybrid } = await import('/native/client.js');
await Hybrid.openDocs();
```

托盘、控制台和 SDK 入口复用固定的 `hybrid-infra-docs` 窗口，不会重复创建。文档窗口使用与内置工具页相同的能力上限，实际 RPC 权限仍按配置中的来源授权检查；不会自动修改权限配置。默认基础能力可运行 UI、事件、窗口等示例，files、cache、shell 等桌面能力需要配置 desktopCapabilities 或相应 permissions，Shell 程序还需 shellPrograms 白名单。左下角显示实际授权数量，悬停可查看能力列表；未授权调用在结果面板显示 E_PERMISSION_DENIED。SDK 入口还要求真实原生窗口身份。

## F12 与构建方式

配置 `debug: true` 并重启，会启用项目的 F12 快捷键。**Wails v3.0.0-beta.33 的默认 production 构建在编译时移除了开发者工具，因此默认正式 EXE 即使 debug:true，F12 仍不能打开控制台。**

开发时使用 `wails3 dev` 或开发构建。Windows 正式调试包可显式执行：

```shell
wails3 task build DEVTOOLS=true
```

它给生产构建追加 Wails 官方支持的 `devtools` 标签；再搭配 `debug: true` 使用项目 F12 快捷键。默认 `DEVTOOLS=false`，不会改变普通正式包的行为。没有控制台时，使用托盘或工作台入口即可。

`hybridDocs()` 由宿主注入的受控脚本提供，不需要先导入 SDK；它只发送固定的 `open-docs` 意图。Go 端检查原生提供的来源、真实窗口和 `host` 权限后再打开文档，不接收任意 URL、命令或窗口选项。命令返回“已请求打开”表示意图已发送，不是绕过授权的保证。

## 维护文档

- 内容源：`frontend/src/docs-content.js`。新增功能时补充分类、标题、摘要、能力、API、参数、示例、返回说明和限制；在 `methodDescriptions` 中为每个方法补充通俗中文介绍，说明它做什么、什么时候用及关键影响。调用入口按「方法签名 + 中文介绍」逐项展示，搜索也包含这些介绍。
- 页面：`frontend/shell/docs.html`，渲染与交互：`frontend/src/shell-docs.js`，样式：`frontend/public/docs.css`。
- 方法参数示例：`frontend/src/docs-examples.js`，维护每个有参数方法的输入类型、初始示例、提示与必要准备代码；新增入口时同步补充，测试会检查参数顺序和所有生成示例的语法。
- 真实运行与资源清理：`frontend/src/docs-runner.js`。`scripts/docs-runner.test.mjs` 使用测试 SDK 验证参数、回调、流式输出、取消和资源清理，不执行实际系统命令或破坏性操作。
- 原生入口：`go/host/developer_docs.go`、`HostService.OpenDeveloperDocs`、托盘菜单及原生消息分发。
- `scripts/docs-content.test.mjs` 会验证功能 ID、文档结构、API 是否真实存在于导出 SDK，以及 JS/JSON 示例语法。只检查语法，不执行示例。

```shell
pnpm --dir frontend format
pnpm --dir frontend format:check
node scripts/generate-hybrid.mjs
node --test scripts/docs-content.test.mjs scripts/docs-runner.test.mjs scripts/sdk-core.test.mjs
go test ./...
go vet ./...
wails3 task build
```

Windows 原生验收包含：概览和两级目录、功能详情、中文搜索、固定文档窗口打开与复用、真实运行 Events.on 并触发事件观察回调与停止清理、独立文档窗口的配置授权和 UI.Notify 真实返回通知窗口 ID，以及带 devtools 标签的正式调试包打开 F12。控制台命令使用独立 JS 上下文验证固定意图；原生 Console 输入遇到 DevTools 粘贴保护，因此未绕过保护继续执行。默认正式包不提供 F12。托盘菜单已接入，但未进行原生点击验收；macOS/Linux 原生界面尚未验收。
