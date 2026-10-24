# hybrid-infra

基于 Go + Wails3 的桌面外壳，为内置网页或独立部署的前端提供窗口、弹框、事件和本地 KV 存储能力。前端不限定 Vue、React 或其他框架。

当前固定版本：**Wails Go / CLI v3.0.0-beta.33、@wailsio/runtime 3.0.0-beta.33**。Wails3 仍为预发布版本，升级时需要同步这三处。

## 开发与构建

要求 Go 1.26+、Node.js、pnpm；Windows 需要 WebView2。macOS/Linux 的原生系统依赖见 [Wails 安装文档](https://v3.wails.io/getting-started/installation/)。

```shell
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.33
pnpm --dir frontend install --frozen-lockfile
# 首次拉取先生成资源，main.go 会嵌入 frontend/dist。
pnpm --dir frontend run build
wails3 task dev
```

```shell
wails3 task build
wails3 task package
```

也可安装 Go Task CLI 后使用 `task dev`、`task build`、`task hybrid`；不必单独安装 Task 即可使用 `wails3 task ...`。

`wails3 dev` 也可直接运行。开发任务将 Vite 固定监听 `127.0.0.1`，并使用 Wails 分配的端口；pnpm 脚本传参不要额外加 `--`，否则端口选项可能失效，导致 Wails 一直等待前端。

## 接入已有前端

```shell
wails3 task hybrid
# 等价：node scripts/generate-hybrid.mjs
```

将生成的整个 `hybrid/` 复制到独立前端的静态资源目录，例如 `public/native/`，即可通过 `/native/client.js` 导入。生成器使用相对模块路径，不再要求部署在 `/hybrid/`。

```javascript
// 普通 HTML 的 <script type="module"> 中可直接使用。
import { UI, Store, Runtime, configureHybrid } from '/native/client.js';

configureHybrid({ origin: 'http://wails.localhost' });
await UI.Alert('提示', '操作成功', 3, 'bottomRight');
const answer = await UI.Confirm({ title: '确认', body: '继续操作？', timeoutSeconds: 60 });
console.log(answer); // "1" 或 "0"
```

外壳配置 `url` 为前端实际地址，其 origin 会自动加入允许列表。额外地址通过 `allowedOrigins` 配置。运行时地址可配置，`Runtime.setTransport` 或 `configureHybrid({ transport })` 可接入自定义通信。

详细用法与限制：[前端接入说明](docs/frontend-integration.md)。

## 配置与数据

参考 [settings.example.json](settings.example.json)。查找顺序：

1. 环境变量 `HYBRID_SETTINGS` 指定的文件；不存在或无效时明确报错。
2. 用户数据目录中的 `settings.json`。
3. 可执行文件同目录的 `settings.json`。
4. 开发时，含 `go.mod` 的当前项目根目录中的 `settings.json`。
5. 均未配置时使用默认值。

用户数据目录默认是 `os.UserConfigDir()/hybrid-infra`，可用 `HYBRID_DATA_DIR` 覆盖。Windows 通常为 `%APPDATA%/hybrid-infra`，macOS 为 `~/Library/Application Support/hybrid-infra`，Linux 为 `$XDG_CONFIG_HOME/hybrid-infra` 或 `~/.config/hybrid-infra`。

KV 写入该目录的 `store.json`，不再写入启动工作目录。**原根目录的 store.json 不会自动迁移或覆盖**；保留数据的方法见升级文档。

## 验证

```shell
go test ./...
go vet ./...
node --test scripts/hybrid-client.test.mjs scripts/sdk-core.test.mjs scripts/bridge-client.test.mjs scripts/ui-client.test.mjs
wails3 task build
```

Go 弹框边界测试会调用 Node，因此运行 Go 测试也需要 Node.js。

- [架构与维护约定](docs/architecture.md)
- [前端接入说明](docs/frontend-integration.md)
- [第一阶段宿主能力、配置与验收](docs/phase1-host.md)
- [第二阶段桌面能力、授权与 SDK 接入](docs/phase2-desktop.md)
- [Shell、全局缓存、事件回调与环境检测](docs/business-bridge.md)
- [通知堆叠、拖拽开关与区域截图](docs/ui-notifications-capture.md)
- [本次升级、兼容变化与验证记录](docs/upgrade-2026-10-20.md)
