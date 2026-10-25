# 第五阶段：统一任务中心与本地数据库

## 开启和入口

在现有 `desktopCapabilities` 中增加 `tasks`、`database`。只给需要全局管理任务的可信页面增加 `tasks.manage`；普通页面只使用 `tasks`。不改用户现有配置，也不默认扩大权限，保存后重启生效。远程来源仍需在 `permissions` 中显式授权。

```json
{"desktopCapabilities":["tasks","tasks.manage","database"]}
```

工作台导航、托盘「任务与数据中心」都可以打开 `/shell/tasks.html`。页面包含真实进度演示、命令和下载排队、任务筛选，以及可填参数的数据库操作。开发文档增加对应分类、方法说明和可执行示例；全部权限复制目前为 33 项（第五阶段为 28 项）。

## 业务任务

```javascript
import { Tasks } from '/native/client.js';

const result = await Tasks.run('同步订单', async ({ signal, report }) => {
  if (signal.aborted) throw new Error('已取消');
  // 将 signal 继续传给 fetch 等支持取消的实际操作。
  await report(50, '已处理一半');
  await report(100, '处理完成');
  return { count: 2 };
}, { onProgress: task => console.log(task.state, task.progress) });
console.log(result.task.id, result.value);
```

回调在 JS 中执行，不序列化传给 Go。SDK 自动创建任务、领取执行槽、续租、上报成功或失败、释放退出保护。`run` 返回 `{task,value}`，其中业务返回值只返回当前调用方，不存入历史。`options.signal` 可以由业务主动取消；其他窗口通过任务中心取消时，SDK 下次心跳会中止回调的 `signal`。

取消是协作式的：函数必须检查信号并清理自己的资源，不能强行停止任意 JS。未退出的函数仍占用任务槽；心跳失联超过 30 秒才标记 interrupted。阻塞 JS 主线程也会使心跳失联，耗时 CPU 工作应放到 Web Worker。回调在页面刷新后丢失，无法跨刷新继续执行。

```javascript
const list = await Tasks.List();
await Tasks.Cancel(list[0].id);
// 失败、取消或中断后，原窗口和执行参数仍存在时重新提交：
// await Tasks.retry(list[0].id);
// 已结束后只移除历史，不删除实际文件：
// await Tasks.Remove(list[0].id);
```

任务队列最多并发 2 项，退出保护最多 100 项；保留最近 500 条历史，达到上限只清除最早的终态。任务绑定来源及窗口，`tasks.manage` 允许全局查看、取消和清理终态历史，不能代其他窗口领取函数、报告进度或重试。当前版本没有可配置并发数和定时调度。

任务状态：queued → ready（仅业务函数）→ running → completed / failed / cancelled / interrupted。执行中取消先进入 cancelling。任务 ID 是随机值，不能当业务记录 ID 或命令任务 ID 使用。

高级封装可以直接使用 `Tasks.Create(title)`、`Tasks.Poll(id)`、`Tasks.Report(id,progress,message)`、`Tasks.Finish(id,state,message)`；`Poll` 必须在领取前和执行期间持续调用，进度为 -1 或 0–100，结束状态为 completed、failed、cancelled。常规接入使用 `Tasks.run` 即可。`Tasks.canRetry(id)` 仅判断本页是否仍持有回调，原生任务看列表的 `canRetry`；`Tasks.dispose()` 用于整页卸载，不用于单个组件，调用后客户端不能再提交业务函数。

## 原生任务

```javascript
import { Tasks, Desktop } from '/native/client.js';

// 仍需 shell 权限和 shellPrograms 白名单；脚本另需 shell.script。
const command = await Tasks.EnqueueShell('检查版本', {
  mode:'program', program:'go', args:['version'], timeoutMs:5000,
});

// 需 downloads、files；用户选择保存位置后才取得可写 token。
const file = await Desktop.SelectFile('save', 'report.zip');
const download = await Tasks.EnqueueDownload('下载报表', 'https://example.com/report.zip', file.token);
// 等 List 显示 download 已进入终态后再 ReleaseFile(file.token)。
```

队列复用原来的进程和下载管理器，窗口关闭会中断本窗口任务。旧 `Shell.start/run`、`Desktop.StartDownload` 仍按原来的即时执行规则运行，并自动镜像到统一历史；不受新增两项队列并发数强行调度，但活动任务会占用队列调度容量。旧接口若因历史数据库故障不能登记，不阻断原执行。

排队任务结束后清理其底层命令/下载记录，下载产物不会删除。任务中心不保存命令输出；需要输出回调时继续使用 `Shell.run`。历史不保存程序参数、脚本、下载地址、文件 token 或函数代码，因此重启不会重放。原生重试重新检查原窗口的能力与凭证，业务回调只能在原页面仍持有函数时重试；重试可能重复已经产生的业务副作用，调用方应保证幂等。

## 数据库

```javascript
import { Database } from '/native/client.js';

const created = await Database.Put('orders', 'order-001', { status:'draft', amount:128 }, 0);
const paid = await Database.Put('orders', created.id, { status:'paid', amount:128 }, created.revision);
const current = await Database.Get('orders', paid.id);
const page = await Database.Query({
  collection:'orders', where:{ status:'paid' }, limit:20,
  offset:0, sortBy:'updatedAt', descending:true,
});
console.log(current.data, current.revision, page.items, page.total, page.nextOffset, page.hasMore);
```

数据位于当前应用数据目录的 `business.sqlite`，使用纯 Go SQLite 驱动，无需 CGO。应用目录依 `appId` 隔离，集合和记录进一步按来源隔离，同来源不同窗口共享。网页不能指定数据库路径、执行任意 SQL 或跨来源读取。

集合名和 ID 为 1–80 位字母数字、点、下划线或连字符。数据必须是 JSON 对象，单条 64 KiB，全部来源合计最多 8 MiB JSON、20000 条。时间为 Unix 毫秒，`revision` 从 1 递增：写入时 0 仅创建，-1 无条件覆盖，正数要求当前版本完全一致，否则报 `E_CONFLICT`。删除也支持版本校验。

```javascript
await Database.Batch([
  { operation:'put', collection:'orders', id:'new', data:{ status:'draft' }, revision:0 },
  { operation:'delete', collection:'orders', id:'old', revision:3 },
]);
```

批处理最多 100 项，任何失败整批回滚，返回其中成功写入记录的数组（删除不产生返回条目）。`where` 最多 10 个顶层字段标量精确匹配，不支持嵌套路径或模糊查询；排序只支持 updatedAt、createdAt、id。每页默认 20、最多 100，响应接近 512 KiB 会提前截页；以返回的 `nextOffset` 继续。多次分页之间没有跨调用快照，其他窗口修改数据可能改变分页结果。

数据库锁串行化写事务与逻辑备份，采用 SQLite FULL 同步策略；当前没有业务关系表、全量 SQL 接口和全文检索。数据库与普通备份没有额外加密，长期秘密继续存系统凭据库。

## 备份与验证

数据版本升到 2；首次升级仍在迁移前自动备份。快照新增 `business-data.json`，在数据库锁内导出文档及任务元数据，避免直接复制正在写入的数据库。恢复前检查字段、容量、重复主键和哈希；启动时通过事务导入，任一记录不合法则回滚。恢复没有此文件的旧快照会清空新增数据库，不能把旧 KV 和新业务记录混用。旧版本程序不能直接降级写入 Schema 2 目录，更新回滚使用既有的更新前数据快照。

验证命令：`go test ./...`、`node --test scripts/*.test.mjs`、`pnpm --dir frontend build`、格式化检查。新增用例覆盖真实 SQLite 持久化和恢复、跨来源权限、版本冲突和批处理回滚、两项并发、取消、失联、重启历史、真实测试子进程和本地 HTTP 下载，以及 SDK 回调和卸载清理。Windows 当前环境未启用 CGO，`go test -race` 无法运行；不将普通测试结果当作竞态检测结果。
