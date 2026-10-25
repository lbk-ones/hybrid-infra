# 前端接入说明

## 三种页面来源

配置 `pageMode` 明确区分入口来源：

| pageMode | url 示例 | 页面来自哪里 |
| --- | --- | --- |
| `builtin` | `/`、`/shell/docs.html` | 编译进 EXE 的资源 |
| `http` | `https://app.example.com/` | 已运行的 HTTP(S) 网站，不读取 EXE 旁 HTML |
| `local` | `/index.html` | `localRoot` 指定的磁盘网页目录，由外壳自己提供，无需另开 HTTP 服务 |

旧配置不写 `pageMode` 时，HTTP(S) 地址按 `http`，以 `/` 开头的路径按 `builtin`，保持原行为。模式与地址不匹配时明确报错，例如 `http` 不能填 `/index.html`。只有 `local` 可配置 `localRoot`。

## EXE 同目录的 HTML

文件可以按下面摆放：

```text
app/
  hybrid-infra.exe
  settings.json
  index.html
  style.css
  native/              ← 复制整个 hybrid/ 的内容
    client.js
    runtime.js
    bindings/...
```

配置：

```json
{
  "pageMode": "local",
  "localRoot": ".",
  "url": "/index.html",
  "desktopCapabilities": ["clipboard"]
}
```

`localRoot` 相对 **EXE 所在目录** 解析，与启动工作目录、配置文件位置无关；也可以填绝对本地目录。留空默认读取 EXE 旁的 `web/`。如果网页放在 `web/`，把 `localRoot` 改为 `web`，HTML、CSS、SDK 都放进该目录。

实际页面地址是 `http://wails.localhost/local/index.html`（平台实际协议以 WebView 为准）。HTML 中用 `./style.css`、`./native/client.js` 等相对路径；`/native/client.js` 是外壳根路径，不代表本地目录中的 native。子目录页面需按位置使用 `../native/client.js`。修改 HTML/JS/CSS 后按 F5 重读磁盘，不需要重新打包；修改模式或目录配置需要重启。

本地页面的最小调用：

```html
<button id="notify">显示通知</button>
<script type="module">
  import { Hybrid, UI } from './native/client.js';
  await Hybrid.connect();
  document.getElementById('notify').onclick = () => UI.Notify('本地 HTML 已接入外壳');
</script>
```

可直接参考仓库 `examples/local-page/` 的完整演示，复制页面和配置到 EXE 旁，再把整个 `hybrid/` 复制为 `native/`。

外置资源只挂载在 `/local/` 下，不替换 `/shell/`、弹框和 Wails 运行时。不会列出目录，也不会回退缺失的文件为内置首页；文件越界和根目录外符号链接被拒绝。只公开网页资源类型：HTML、JS/MJS、CSS、图片、字体、WASM 和常见音视频；JSON、日志、EXE 等文件不作为静态资源开放，避免目录设为 EXE 同级时暴露应用配置。业务 JSON 可放在 JS 模块中导出，或通过业务 HTTP 接口加载。入口 HTML 或根目录缺失时，启动会明确报错。

`local` 使用本地 `wails.localhost` 来源授权，默认基础能力与内置页面相同；扩展能力仍需 `desktopCapabilities` 或该来源的 `permissions`，不会因为文件在磁盘上就自动开放全部权限。HTTP 页面依旧按实际网站来源配置 `permissions`。

推荐本地 SPA 使用 hash 路由，例如 `url: "/index.html#workspace"`；当前没有自动 history 回退。协议、通知和快捷键唤醒的 `/` 路由在 local 模式指向 `/local/`；用 `#workspace` 保留配置的入口文件。主动调用 `Windows.Open` 时用 `/local/details.html` 打开外置子页面，用 `/shell/docs.html` 打开内置页面。

配置文件仍遵循原查找顺序：`HYBRID_SETTINGS`、用户数据目录、EXE 同目录、开发项目。若用户数据目录已有设置，它会优先于 EXE 旁的 `settings.json`。

## 内置页面与 HTTP 网站

**内置前端**：使用仓库的 `frontend/`，从 `frontend/bindings` 导入服务，用 `@wailsio/runtime` 导入事件、窗口等 API。构建后资源嵌入可执行文件，默认 `url: "/"`。使用自己的构建产物时，需要保留 Alert.html、Confirm.html、Show.html、dialog.css，以及 shell/status.html、shell/manage.html 和它们引用的资源。SPA history 路由需要自行配置资源回退，或使用 hash 路由。

**外部前端**：网页独立部署，外壳 `settings.json` 的 `url` 指向其 HTTP(S) 地址。前端复制完整的 `hybrid/` 作为静态文件，不需要依赖本仓库的 Vite、目录结构或框架。弹框仍由外壳自带的 HTML 承载。

例如外壳配置：

```json
{
  "title": "业务系统",
  "pageMode": "http",
  "url": "https://app.example.com/portal/",
  "debug": false,
  "allowedOrigins": ["http://localhost:5173"]
}
```

配置的主页面 origin `https://app.example.com` 自动允许；额外窗口使用其他来源且需要调用桥接时，显式添加对应 origin。列表只接受 `scheme://host[:port]`，不接受 `*`、路径或结尾斜杠。

## 导出与部署

```shell
# 首次生成前需要 dist，使 Go embed 能找到资源。
pnpm --dir frontend install --frozen-lockfile
pnpm --dir frontend run build
wails3 task hybrid
```

生成器验证 CLI、Go、npm runtime 版本一致，在临时目录生成，再更新 `hybrid/`。`manifest.json` 保存 Wails 版本和 bindings 内容哈希。修改服务后重新生成，并把外壳与该次导出包一起交付；方法 ID 不能保证跨任意服务改名后仍兼容。

将 `hybrid/` 内容放在前端 `public/native/`：

```html
<script type="module">
  import { UI, Store, Runtime, configureHybrid } from '/native/client.js';
  configureHybrid({ origin: 'http://wails.localhost' });
  await UI.Alert('通知', '连接成功', 2, 'bottomRight');
</script>
```

React/Vue 等构建工具中，可使用浏览器原生动态导入静态 URL，或把整个目录作为模块源码交给构建工具。使用静态资源 URL 时避免让打包器把 URL 当作 npm 依赖解析；Vite 可用 `import(/* @vite-ignore */ clientURL)`。

导入模块前，也可设置 `window.__HYBRID_RUNTIME_ORIGIN__`。默认规则：当前 hostname 为 `wails.localhost` 时保留当前 origin，否则使用 `http://wails.localhost`。macOS/Linux 的 WebView 协议、HTTPS 页面跨协议访问、CSP、代理策略需按目标环境验证；必要时配置正确的原生 origin 或自定义 transport。此次未在这些系统完成原生 WebView 验收。

该目录是浏览器端代码，SSR 应在客户端挂载后动态导入。普通浏览器没有桌面宿主，仅复制 JS 不会创建本地服务。

## 服务 API

```javascript
import { UI, Store, Runtime } from '/native/client.js';

await UI.Alert('标题', '纯文字，支持引号和换行\n第二行', 3, 'center');
const result = await UI.Confirm({
  title: '确认', body: '是否继续？',
  okText: '继续', cancelText: '返回',
  place: 'center', timeoutSeconds: 60
});

// HTML 必须来自受信任的应用代码，可以包含应用自己的事件处理器。
await UI.Show({ body: '<div>自定义内容</div>', width: 400, height: 200, place: 'topRight' });

await UI.OpenWindowByUrl({
  url: 'https://app.example.com/details', title: '详情',
  width: 1000, height: 700, place: 'center',
  isFullscreen: false, f5Reload: true, f12DevTools: false
});

const off = Runtime.Events.On('time', event => console.log(event.data));
// 组件卸载时：
off();
```

`Store` 提供的具体方法以 `hybrid/bindings/github.com/wailsapp/wails/v3/pkg/services/kvstore/kvstoreservice.js` 为准；该文件包含生成的 JSDoc。

窗口位置支持 `center`、`topLeft`、`topRight`、`bottomLeft`、`bottomRight`。空值或未知位置居中；普通子窗口宽高非正数时回退为 300×150。角落定位使用主显示器工作区的逻辑坐标；截图使用物理像素。

`Alert.destroy` 的单位是秒，<=0 不自动关闭。`Confirm` 返回字符串 `"1"` / `"0"`，关闭窗口、超时、宿主退出都视为取消；timeoutSeconds <=0 默认 300 秒，大于一天按一天计算。主动取消 JS promise 时遵循 Wails 的 CancelError 语义，服务端同时停止等待并清理窗口，不保证调用方收到 `"0"`。

```javascript
const pending = UI.Confirm({ body: '等待确认' });
const handled = pending.catch(error => {
  if (!(error instanceof Runtime.CancelError)) throw error;
});
// 页面切换等不再需要确认时：
await pending.cancel();
await handled;
```

## 自定义通信

```javascript
configureHybrid({
  transport: {
    async call(objectID, method, windowName, args) {
      // 连接你自己的 IPC / 网络适配器并返回实际调用结果。
      return yourAdapter.call(objectID, method, windowName, args);
    }
  }
});
```

生成包保留 Wails 原始 `setTransport` 接口。适配器必须处理响应、取消调用、窗口路由与等效授权；前端事件接收也需要正确交给 Wails 的事件分发机制。这里没有内置 WebSocket 服务或跨网络宿主发现机制。`setTransport` 面向 runtime RPC，流式传输等独立通道应另行验证。

## 信任边界

Alert/Confirm 按纯文本显示，Show 明确接受受信任 HTML；JSON 序列化防止 HTML 字符串打断外层脚本，但不会清洗 Show 中的 HTML 或事件处理器。

第一阶段新增独立于 CORS 的运行时来源与能力授权，直接调用 bindings 也受检查。通过 permissions 可为来源覆盖 host、windows、ui、ui.html、store、log、lifecycle、events。单次 RPC 最大 1 MiB，分块请求明确拒绝。原始 Wails Application、Clipboard、Dialog、Browser 等子系统暂不开放，使用宿主接口完成退出和外链操作。完整 SDK、导航策略与兼容变化见[第一阶段说明](phase1-host.md)。这不是用户认证或任意网页隔离沙箱。远程页面若使用 CSP，需要允许所用模块路径与连接地址。不要同时加载不同版本或重复实例的 Wails runtime，以免事件接收器被覆盖。

以上 HTTP 桥接配置面向桌面 WebView。Wails 的 `server` 构建具有独立来源校验和通信约定，不能把本项目桌面 CORS 配置当作远程服务部署方案。

## 第二阶段桌面接口

`hybrid/client.js` 新增 `Desktop` 导出，支持文件凭证、目录、下载、剪贴板、原生通知、快捷键、协议、设置和系统凭据。所有能力默认关闭，按来源显式授权；方法、示例及平台限制见 [第二阶段接入](phase2-desktop.md)。

`Shell`、`Cache`、`Events` 和 `Hybrid.detectEnvironment` 的配置、回调语义及请求响应示例见 [业务桥接](business-bridge.md)。新的 Events 以会话和来源隔离消息，不依赖 Runtime.Events 的全窗口广播。
