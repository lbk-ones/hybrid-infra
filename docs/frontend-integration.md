# 前端接入说明

## 两种接入方式

**内置前端**：使用仓库的 `frontend/`，从 `frontend/bindings` 导入服务，用 `@wailsio/runtime` 导入事件、窗口等 API。构建后资源嵌入可执行文件，默认 `url: "/"`。可以用自己的构建产物替换 `frontend/dist`，但需要保留 Alert.html、Confirm.html、Show.html 及其 `/wails/runtime.js` 引用，原生弹框依赖它们。SPA history 路由需要自行配置资源回退，或使用 hash 路由。

**外部前端**：网页独立部署，外壳 `settings.json` 的 `url` 指向其 HTTP(S) 地址。前端复制完整的 `hybrid/` 作为静态文件，不需要依赖本仓库的 Vite、目录结构或框架。弹框仍由外壳自带的 HTML 承载。

例如外壳配置：

```json
{
  "title": "业务系统",
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

生成包保留 Wails 原始 `setTransport` 接口。适配器必须处理响应、取消调用及窗口路由；前端事件接收也需要正确交给 Wails 的事件分发机制。这里没有内置 WebSocket 服务或跨网络宿主发现机制。`setTransport` 面向 runtime RPC，流式传输等独立通道应另行验证。

## 信任边界

Alert/Confirm 按纯文本显示，Show 明确接受受信任 HTML；JSON 序列化防止 HTML 字符串打断外层脚本，但不会清洗 Show 中的 HTML 或事件处理器。

CORS 按配置允许跨源请求，支持 runtime 的分块上传请求头；它不是用户认证、导航控制或按服务授权。打开的远程页面属于宿主信任范围，勿将不可信网页当作隔离沙箱。远程页面若使用 CSP，需要允许所用模块路径与连接地址。不要同时加载不同版本或重复实例的 Wails runtime，以免事件接收器被覆盖。

以上 HTTP 桥接配置面向桌面 WebView。Wails 的 `server` 构建具有独立来源校验和通信约定，不能把本项目桌面 CORS 配置当作远程服务部署方案。
