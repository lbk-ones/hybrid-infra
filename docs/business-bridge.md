# Shell、共享缓存、事件回调与环境检测

入口：工作台“业务桥接”，或桌面能力页的“命令 / 缓存 / 事件”。对应 `/shell/bridge.html`。独立前端复制整个 `hybrid/` 目录，导入 `client.js` 的 `Shell`、`Cache`、`Events`、`Hybrid`。

## 配置与权限

合并新增字段进自己的完整配置，不要覆盖已有能力列表，重启后生效：

```json
{
  "shellPrograms": ["go", "git"],
  "desktopCapabilities": ["cache", "shell"],
  "permissions": {
    "https://app.example.com": ["host", "events", "cache", "shell"]
  }
}
```

`desktopCapabilities` 只作用于内置页面；远程页面使用 `permissions` 完整列表。`shellPrograms` 在启动时解析为固定绝对路径，未安装的程序不启用，正式环境优先填写绝对路径。示例配置不会默认开启 shell 或 shell.script。

| 权限 | 作用 |
| --- | --- |
| shell | 命令入口，程序模式受 shellPrograms 白名单限制 |
| shell.script | 额外允许完整脚本，Windows 用系统 PowerShell，Unix 用 /bin/sh |
| cache | 同来源窗口共享内存缓存 |
| cache.global | 额外允许全应用、跨来源的缓存空间 |
| events | 同来源事件及请求响应，沿用第一阶段能力 |
| events.global | 额外允许跨来源消息，发送与接收双方都必须授权并显式选择全应用空间 |
| host | SDK 握手和环境确认 |

命令能力只用于受信任业务页面。白名单限制程序入口，不限制程序自身功能；允许解释器或支持执行子命令的工具及任意参数，仍可能获得完整系统操作能力。shell.script 控制脚本 API 模式，不是针对程序参数的安全沙箱。

## 命令执行与输出回调

```javascript
import { Shell } from '/native/client.js';
const controller = new AbortController();
const result = await Shell.run(
  { mode: 'program', program: 'go', args: ['version'], timeoutMs: 30000 },
  {
    signal: controller.signal,
    encoding: 'utf-8',
    onStdout: (text) => appendOutput(text),
    onStderr: (text) => appendError(text),
  },
);
console.log(result.state, result.exitCode, result.stdout, result.stderr);
// 停止：controller.abort()
```

回调留在前端，SDK 每约 150 ms 按字节游标读取输出。解码支持跨块字符；输出为 GBK 时可用 `encoding:'gbk'`。完整脚本使用 `{mode:'script',script:'Write-Output "你好"'}`，须额外授权 shell.script。

终态返回 completed / failed / cancelled / timed_out、退出码、error、truncated。非零退出作为结果返回，业务自行判断；调用失败、AbortSignal 和输出回调异常会抛错并请求取消，错误带 jobId。

默认 30 秒，最多 10 分钟；并发最多 4 个，历史 100 条，stdout/stderr 合计保留 256 KiB，超出仍消费但不保存以免堵住管道。命令自动登记退出保护，必须关联原生窗口；普通浏览器不能创建无归属命令。

Windows 使用 Job Object，Unix 使用进程组；取消、窗口关闭和退出会终止受管理进程树。Windows 作业在 Start 后附加，不承诺防止恶意程序逃逸；父程序退出也清理其受管理后代，不适合常驻守护程序。

手动任务使用 Shell.start / read / cancel / remove；read 返回 Base64 字节和新字节游标。run 自动解码并默认清理结束记录，keepHistory:true 可保留。取消后清理最多等三秒，失败保留记录，可用 jobId 查询。网络断开时不保证取消送达，仍依赖宿主超时和窗口生命周期收尾。

## 共享缓存

```javascript
import { Cache } from '/native/client.js';
await Cache.Set('orders', 'filter', { status: 'pending' }, 60000, false);
const old = await Cache.Get('orders', 'filter', false);
await Cache.CompareAndSet('orders', 'filter', { status: 'done' }, 60000, false, old.revision);
await Cache.Delete('orders', 'filter', false);
await Cache.Clear('orders', false); // 只清理指定命名空间。
```

最后一个空间参数 false 表示同来源，true 表示显式全应用空间，须 cache.global。值为 JSON，最多 64 KiB；进程合计 4096 项、16 MiB。TTL 单位毫秒，0 表示进程内不过期，最多七天。返回 found / value / revision / expiresAt，未命中与 JSON null 可区分。

CompareAndSet 在同一 Go 锁内检查修订号并更新，竞争返回 E_CONFLICT，业务重新读取后重试；revision=0 要求键不存在。多个窗口共享同一 Go 进程内数据，重启后清空；持久化使用 Store，秘密使用系统凭据。

## 事件与本地函数回调

```javascript
import { Events } from '/native/client.js';
const disposeError = Events.onError((error) => showError(error.code));
const off = await Events.on('orders:changed', (data, meta) => {
  refreshOrder(data.orderId);
  console.log(meta.sender);
});
await Events.emit('orders:changed', { orderId: 123 }, { targetWindow: 'main-window' });
await off(); // 只移除自己的监听，不影响同主题其他监听。
disposeError();
```

**回调函数保留在前端闭包中，跨进程只传 JSON。** Go 保存会话和监听编号，SDK 的一条长轮询批量取回消息，按编号调用本地函数。数据不允许函数、Symbol、BigInt 或循环引用。此通道不依赖原生全窗口广播，普通浏览器在正确配置来源并连接 HTTP 桥后也可使用。

on / once / handle 为异步，返回 Promise<取消函数>，需先 await 再发送。once 在第一次分发前移除本地监听。emit 返回匹配监听数，0 表示当前没有监听；不等待回调完成、不收集返回值。

默认 scope:'origin'；跨来源须双方拥有 events.global 并使用 scope:'app'。targetWindow 仅用于发送，不绕过来源空间。meta 包含 topic、sender、kind、requestId；发送窗口由 Go 识别，不能由消息体冒充。

### 请求与异步返回值

```javascript
const stop = await Events.handle('orders:summary', async (data) => {
  return { orderId: data.orderId, summary: await loadSummary(data.orderId) };
});
const response = await Events.request('orders:summary', { orderId: 123 }, {
  targetWindow: 'summary-window', timeoutMs: 5000, signal: abortController.signal,
});
await stop();
```

随机 requestId 关联结果，只有被选中的会话和处理器可以响应。无处理器 E_NO_HANDLER，多处理器 E_CONFLICT（需指定唯一目标）；回调异常或返回不可 JSON 化的值为 E_HANDLER_ERROR。默认等待十秒、最多三十秒，超时 E_TIMEOUT、取消 E_CANCELLED，处理器移除、窗口关闭或会话替换为 E_HANDLER_GONE。

超时和取消清理等待资源及尚未取走的请求，不能中止已在另一页面执行的 JS 业务函数，也不会撤回已完成副作用；迟到响应被拒绝。

原生窗口使用一个 SDK 事件会话，新页面会话替换旧会话，避免刷新后的旧处理器重复匹配。组件共享导出的 Events，不要在同一窗口创建多个独立客户端。普通浏览器同来源最多八会话，应用合计 128；每会话最多 64 监听、128 消息且总计 256 KiB，单消息最多 64 KiB，待响应请求最多 256。队列满 E_BACKPRESSURE，广播先检查全部容量再统一入队。

组件主动取消监听，整页可 Events.close()；页面卸载时 SDK 尽力释放，超过两分钟无活动的会话在后续宿主操作时回收。网络错误退避重试；会话失效或权限拒绝通过 onError 报告并清空监听，业务重新订阅。

事件不持久化、不补发，断网或卸载时不保证可靠送达；可靠消息需另外持久化、确认与幂等。兼容接口 Runtime.Events 仍保留，但不具备新总线的逐来源接收隔离，敏感业务使用新 Events。

## 外壳环境判断

```javascript
import { Hybrid } from '/native/client.js';
const hint = Hybrid.isDesktopShell(); // 同步特征初判。
const environment = await Hybrid.detectEnvironment({ timeoutMs: 1500 });
if (environment.connected && environment.isDesktopShell) { /* 桌面体验 */ }
```

类型为 server（SSR）、browser、browser-connected、desktop-shell、desktop-unavailable（存在桥接特征但握手失败）。异步结果含连接、协议、版本、平台，失败返回 errorCode。同步对象可能被伪造或属于其他 WebView，异步 Host.GetEnvironment 结合宿主保存的窗口身份确认。环境检测用于体验切换，不是用户认证、进程鉴权或能力授权凭证，操作始终由 Go 校验。

当前固定版本 CLI 导出的 runtime 有一处未保护的 WML 初始化，导出脚本会为它增加 DOM 守卫；完整 client.js 可在无 window/document 的 SSR 环境直接导入。修复在生成器中完成，不手改产物，也不通过创建虚假 window 掩盖环境。

## 验证范围

Go 测试覆盖缓存来源隔离、TTL、并发 CAS；事件定向路由、全局授权、伪造响应拒绝、超时与关闭清理；真实子进程输出、退出码、白名单、脚本权限拒绝、取消、超时、截断。Node 测试覆盖闭包、Once、取消监听、异步返回与异常、不可 JSON 化返回值、流式解码和环境判断。

原生 Windows 窗口已验证事件回调、async 请求返回、取消全部监听、缓存写读及 TTL 显示，环境握手返回 desktop-shell。命令通过真实子进程集成测试验证。Unix 进程组尚未实机验收；当前 CGO 关闭，Go race 检测不可用。
