# 第三阶段：维护、升级与数据保护

入口：工作台侧栏的「维护中心」，内置地址 `/shell/maintenance.html`。此阶段增加诊断、数据快照、版本迁移和 Windows 便携版签名更新。所有新增手写方法均有中文注释，业务前端可从完整 `hybrid/client.js` 导入 `Maintenance`。

## 授权与配置

`diagnostics`、`backups`、`updates` 是三个独立能力，默认不授权。参考根目录 `settings.example.json` 在 `desktopCapabilities` 中为内置页面开启；远程页面仍需 `permissions` 显式授权。导出诊断另需 `files`，手动回滚另需 `backups`。备份和更新作用于整个应用数据目录，应只授予可信业务页面。

根目录实际 `settings.json` 未被改写。可将示例复制到独立配置，使用 `HYBRID_SETTINGS` 指定。使用不同 `HYBRID_DATA_DIR` 隔离测试与生产。

```json
{
  "desktopCapabilities": ["files", "diagnostics", "backups", "updates"],
  "updates": {
    "enabled": false,
    "manifestUrl": "https://your-server.example/stable/manifest.json",
    "publicKey": "发布工具输出的32字节Ed25519公钥Base64",
    "channel": "stable",
    "checkOnStartup": false,
    "autoDownload": false
  }
}
```

配置真实地址和公钥后再设 `enabled: true`。`checkOnStartup` 自动检查，`autoDownload` 在自动检查发现版本后下载；**安装与重启始终显式发起**。配置修改重启后生效。只接受 HTTPS，本机集成测试可使用 localhost、127.0.0.1、::1 的 HTTP。重定向限制在同源内，清单与签名中的下载 URL 可以分别使用不同 HTTPS 站点。

## 诊断

`GetDiagnostics()` 返回应用、Go、协议与数据版本、OS/架构、内存、协程、窗口和任务数量，以及最近 128 条事件时间与错误码。结构化事件存于数据目录的 `diagnostics.json`，重启后重载。

`ExportDiagnostics(token)` 接受 `Desktop.SelectFile("save", "hybrid-diagnostics.json")` 返回的当前窗口保存凭证，导出 JSON。不会收集页面 URL、文件路径、命令输出、KV 值、截图、系统凭据或完整异常文本。诊断摘要不是原生崩溃转储，不能替代系统 crash dump。

## 数据快照与迁移

快照保存在数据目录 `backups/<随机ID>.zip`，内容严格限于 `store.json`、`preferences.json`、`window-state.json`、`data-version.json` 和数据库逻辑快照 `business-data.json`。**不包含启动权限配置、系统凭据和日志**。业务 KV 本身可能含敏感信息，快照是本机普通文件，没有额外加密。

`CreateBackup()` 阻塞 Store/设置/数据库 RPC 的磁盘写入后生成快照，数据库自身的后台任务状态通过数据库锁导出一致快照。每个文件最多 16 MiB、总解压内容最多 48 MiB；恢复前验证路径白名单、重复条目、JSON、数据版本、完整条目数和 SHA256。恢复 ID 只能是随机 32 位十六进制，不能传入任意路径。数据库与任务接入及旧备份恢复行为见[第五阶段](phase5-tasks-database.md)。

`StageRestore(id)` 只排队，不修改正在运行的 KV。下次启动先另存当前数据，再恢复全部白名单文件；写入失败时还原旧文件。随后服务才加载业务数据。关闭应用与重新启动之间新增的业务修改会被待恢复快照覆盖。

当前数据版本为 Schema 1。无版本文件的旧用户目录视为 Schema 0，启动时先自动备份再写入版本标记；本次无需改变原有业务数据格式。未来版本文件会阻止旧程序启动，不静默降级。旧项目根目录 `store.json` 仍不会自动覆盖用户目录的数据；本阶段没有导入任意外部 ZIP 的接口。

Wails 单实例检查先于恢复和迁移。数据目录还由系统文件锁独占，进程异常结束也会释放；即使关闭 `singleInstance`，多个进程也必须使用不同数据目录，避免同时写一个 KV。备份暂未提供自动清理策略，应按自身保留要求管理容量。

## 发布签名包

1. 在受控环境离线生成 Ed25519 PKCS8 PEM 私钥，例如安装了 OpenSSL 的环境执行 `openssl genpkey -algorithm ED25519 -out release-private.pem`。私钥应存放在仓库和公开发布目录之外；不要提交或随程序分发。
2. 注入真实版本构建：`wails3 task build VERSION=0.2.0`。
3. 签名并输出发布文件：

```shell
node scripts/package-release.mjs --exe bin/hybrid-infra.exe --version 0.2.0 --channel stable --arch amd64 --url https://your-server.example/stable/hybrid-infra-0.2.0-windows-amd64.exe --key-file C:/private/release-private.pem --out C:/release/hybrid-0.2.0
```

输出 EXE、`manifest.json` 和 `public-key.txt`。工具先校验 PE 架构，再通过 EXE 的 `--hybrid-release-info` 只读入口确认实际版本，避免误签。将 EXE 部署到 `--url` 所指定地址，将清单部署到启动配置的 `manifestUrl`；公钥固定写入用户配置，私钥不上传。

清单采用 `{payload: Base64(JSON原始字节), signature: Base64(Ed25519签名)}`。载荷绑定协议、版本、频道、平台、架构、下载 URL、文件长度、SHA256 和数据兼容范围。版本目前只接受 `major.minor.patch` 三个整数；stable/beta 是发布频道，暂不接受 SemVer 预发布后缀。当前发布工具按目标 EXE 声明数据兼容范围；本版本为 Schema 0–2；后续升级数据格式时需同步审查迁移和清单兼容范围。

Ed25519 是应用更新信任链，不替代 Windows Authenticode。仓库已有 `wails3 task windows:sign`、NSIS/MSIX 打包任务；需要正式证书、对应工具及发布环境自行配置。本次没有申请证书、签名系统安装器或上传正式发布。

## 安装与回退

1. `CheckUpdates()` 验签并检查版本与兼容性。
2. `DownloadUpdate()` 后台流式下载、显示进度，校验大小、SHA256 和 Windows PE 架构。下载计入退出保护任务；可 `CancelUpdate()`。
3. `InstallUpdate()` 在没有业务任务且安装目录可写时准备独立工作进程。工作进程确认父进程映像并发出就绪握手后，原应用才退出。
4. 原进程结束、KV 关闭保存完成后，工作进程独占数据目录，备份最终数据和旧 EXE，原子替换程序，再启动新版本。
5. 新版本主窗口 runtime 在 40 秒内就绪且版本、随机令牌、PID 相符，再存活至少 2 秒，视为启动成功。该检查验证外壳启动，不依赖远程业务站点是否在线，也不保证业务服务的全部功能健康。
6. 新版本启动失败、退出或超时，先终止新进程，重新取得数据锁，再恢复旧 EXE 与更新前数据并启动旧版本。

`RollbackUpdate()` 恢复上一版本和对应的更新前数据，同时为当前程序及数据保留快照。有未完成任务时拒绝安装或回滚。更新结果可通过 `GetUpdateState()` 或数据目录 `updates/result.json` 查看。

当前自动安装面向 **Windows 可写目录的便携 EXE**。系统保护目录、需要 UAC 的安装、MSIX/NSIS 安装器更新、macOS/Linux 的安装和签名没有接入此工作进程，使用对应平台安装器。电源中断或工作进程本身被强制终止尚未实现下一次启动自动恢复；维护目录中会保留 `before.exe`、票据和备份，可用于人工恢复。遇到 `E_ROLLBACK_FAILED` 不要清理这些文件。

## 前端调用

```javascript
import { Maintenance, Hybrid } from '/native/client.js';
const report = await Hybrid.call(() => Maintenance.GetDiagnostics());
const snapshot = await Hybrid.call(() => Maintenance.CreateBackup());
const updates = await Hybrid.call(() => Maintenance.CheckUpdates(), { timeoutMs: 60000 });
// 用户确认后，排队到下次启动恢复。
await Hybrid.call(() => Maintenance.StageRestore(snapshot.id));
```

## 验证记录

- `go test ./...`、`go vet ./...`：迁移/延迟恢复、损坏备份拒绝、签名篡改/平台/版本约束、真实回环 HTTP 下载与取消、诊断脱敏、事务回退。
- Windows 独立进程测试：临时目录中的测试 EXE 验证父进程握手、数据锁、文件替换、健康确认；模拟新进程写入数据后崩溃，确认旧数据和旧程序恢复。不会替换用户实际安装。
- `node --test scripts/package-release.test.mjs`：已构建 Windows EXE 的临时密钥签名、SHA256、公钥和实际版本约束；非 Windows 或未构建 EXE 时跳过。
- `wails3 task build VERSION=0.1.0`：Windows production 构建及全部多页面前端构建。
- 使用 computer-use 技能检查独立原生维护窗口，实际创建快照并确认列表更新；未连接正式更新服务或安装正式版本。
- macOS/Linux 原生运行、平台签名与安装器未验收。上一阶段手动框选截图的原生验收仍待完成，不计入本阶段已通过项目。
