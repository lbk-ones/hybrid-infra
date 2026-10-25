import { Hybrid, Host, Tasks, Database, Desktop } from "./host-client.js";

/** 根据固定 ID 获取页面控件。 */
const $ = (id) => document.getElementById(id);
const terminal = new Set(["completed", "failed", "cancelled", "interrupted"]);
const labels = {
  queued: "排队中",
  ready: "待领取",
  running: "执行中",
  cancelling: "正在取消",
  completed: "已完成",
  failed: "失败",
  cancelled: "已取消",
  interrupted: "已中断",
};
let capabilities = new Set(),
  busy = false,
  disposed = false,
  timer,
  offset = 0,
  nextOffset = 0;
const grants = new Map();

/** 在顶部显示真实调用结果；错误使用纯文本，避免把业务内容解释成 HTML。 */
function notice(message, error = false) {
  $("notice").textContent = message;
  $("notice").classList.toggle("error", error);
}

/** 包装用户主动操作，临时禁用按钮并统一展示失败原因。 */
function action(id, operation) {
  $(id).addEventListener("click", async () => {
    $(id).disabled = true;
    try {
      await operation();
    } catch (error) {
      notice(error.message || String(error), true);
    } finally {
      if (!disposed) $(id).disabled = false;
    }
  });
}

/** 为任务添加可用操作；重试只作用于仍持有执行参数或本页回调的记录。 */
function taskButton(row, label, operation) {
  const button = document.createElement("button");
  button.textContent = label;
  button.addEventListener("click", async () => {
    button.disabled = true;
    try {
      await operation();
      await refresh();
    } catch (error) {
      notice(error.message, true);
    } finally {
      button.disabled = false;
    }
  });
  row.append(button);
}

/** 获取并按筛选条件绘制真实任务，串行刷新，页面隐藏时停止轮询但不取消后台任务。 */
async function refresh() {
  if (busy || disposed || !capabilities.has("tasks")) return;
  busy = true;
  try {
    const list = await Tasks.List();
    if (disposed) return;
    $("task-count").textContent =
      `${list.filter((task) => !terminal.has(task.state)).length} 项进行中 / ${list.length} 条历史`;
    for (const task of list)
      if (terminal.has(task.state) && grants.has(task.id)) {
        await Desktop.ReleaseFile(grants.get(task.id));
        grants.delete(task.id);
      }
    const visible = list.filter(
      (task) =>
        (!$("kind").value || task.kind === $("kind").value) &&
        (!$("state").value ||
          ($("state").value === "active"
            ? !terminal.has(task.state)
            : task.state === $("state").value)),
    );
    $("tasks").replaceChildren();
    if (!visible.length) {
      const empty = document.createElement("p");
      empty.className = "hint";
      empty.textContent = "当前筛选下暂无任务";
      $("tasks").append(empty);
    }
    for (const task of visible) {
      const row = document.createElement("article");
      row.className = "task";
      const body = document.createElement("div"),
        title = document.createElement("h3"),
        state = document.createElement("p"),
        progress = document.createElement("progress"),
        meta = document.createElement("small"),
        buttons = document.createElement("div");
      title.textContent = task.title;
      state.textContent = `${labels[task.state] || task.state} · ${task.kind} · ${task.progress < 0 ? "进度未知" : task.progress + "%"}${task.message ? " · " + task.message : ""}`;
      progress.max = 100;
      if (task.progress >= 0) progress.value = task.progress;
      progress.setAttribute("aria-label", `${task.title}进度`);
      meta.textContent = `${new Date(task.createdAt).toLocaleString()} · 尝试 ${task.attempt} · ${task.id}`;
      body.append(title, state, progress, meta);
      if (task.error) {
        const error = document.createElement("p");
        error.className = "error";
        error.textContent = `${task.error.code}: ${task.error.message}`;
        body.append(error);
      }
      buttons.className = "toolbar";
      if (task.canCancel) taskButton(buttons, "取消", () => Tasks.Cancel(task.id));
      if (task.canRetry || Tasks.canRetry(task.id))
        taskButton(buttons, "重试", () => Tasks.retry(task.id));
      if (terminal.has(task.state)) taskButton(buttons, "移除记录", () => Tasks.Remove(task.id));
      row.append(body, buttons);
      $("tasks").append(row);
    }
  } catch (error) {
    notice(error.message, true);
  } finally {
    busy = false;
  }
}

/** 运行可配合取消的进度演示，只更新任务状态，不执行外部命令或修改业务数据。 */
async function demo() {
  notice("演示已提交，可以继续提交其他任务或在列表中取消。");
  const result = await Tasks.run($("task-title").value, async ({ signal, report }) => {
    for (let step = 1; step <= 20; step++) {
      await new Promise((resolve) => setTimeout(resolve, 200));
      if (signal.aborted) throw new Error("演示已取消");
      await report(step * 5, `处理第 ${step} / 20 项`);
    }
    return { processed: 20 };
  });
  notice(`任务完成：${result.value.processed} 项`);
  await refresh();
}

/** 把数据库返回值显示到结果框；单条读取和保存后同步最新版本，便于下一次安全更新。 */
function databaseResult(value, record = false) {
  $("database-result").textContent = JSON.stringify(value, null, 2);
  if (record) {
    $("document").value = JSON.stringify(value.data, null, 2);
    $("revision").value = value.revision;
  }
  notice("数据库操作成功");
}

/** 按当前查询条件读取一页，使用真实 nextOffset 翻页，避免因响应大小限制漏记录。 */
async function query() {
  const result = await Database.Query({
    collection: $("collection").value,
    where: JSON.parse($("where").value),
    offset,
    limit: Number($("limit").value),
    sortBy: "id",
    descending: false,
  });
  nextOffset = result.nextOffset;
  $("next").disabled = !result.hasMore;
  databaseResult(result);
}

/** 排队下载前取得当前窗口的保存凭证；完成后释放，失败提交时立即释放。 */
async function queueDownload() {
  const file = await Desktop.SelectFile("save", "download.bin");
  if (!file?.token) return;
  try {
    const task = await Tasks.EnqueueDownload(
      $("task-title").value,
      $("download-url").value,
      file.token,
    );
    grants.set(task.id, file.token);
    await refresh();
  } catch (error) {
    await Desktop.ReleaseFile(file.token);
    throw error;
  }
}

/** 启动时只读连接信息，根据已有授权启用区域；能力配置保持由应用管理员显式控制。 */
async function start() {
  try {
    const info = await Hybrid.connect();
    capabilities = new Set(info.capabilities);
    await Host.Ready();
    for (const section of document.querySelectorAll("[data-cap]"))
      if (!capabilities.has(section.dataset.cap)) {
        for (const control of section.querySelectorAll("button,input,textarea,select"))
          control.disabled = true;
        const hint = document.createElement("p");
        hint.className = "error";
        hint.textContent = `请在配置中授权 ${section.dataset.cap} 后重启。`;
        section.prepend(hint);
      }
    $("queue-shell").disabled = !capabilities.has("tasks") || !capabilities.has("shell");
    $("queue-download").disabled = !["tasks", "downloads", "files"].every((cap) =>
      capabilities.has(cap),
    );
    notice(
      `宿主已连接 · ${capabilities.has("tasks.manage") ? "全局任务视图" : "当前窗口任务视图"} · 数据按来源隔离`,
    );
    await refresh();
    timer = setInterval(() => {
      if (!document.hidden) void refresh();
    }, 1000);
  } catch (error) {
    notice(`连接失败：${error.message}`, true);
    for (const button of document.querySelectorAll("main button")) button.disabled = true;
  }
}
// 每次点击都提交独立演示，允许连续点击观察并发上限和排队，不阻塞其他页面操作。
$("demo").addEventListener("click", () => {
  void demo().catch((error) => notice(error.message, true));
});
action("refresh", refresh);
action("queue-shell", async () => {
  await Tasks.EnqueueShell($("task-title").value, JSON.parse($("command").value));
  notice("命令已排队");
  await refresh();
});
action("queue-download", queueDownload);
action("put", async () =>
  databaseResult(
    await Database.Put(
      $("collection").value,
      $("record-id").value,
      JSON.parse($("document").value),
      Number($("revision").value),
    ),
    true,
  ),
);
action("get", async () =>
  databaseResult(await Database.Get($("collection").value, $("record-id").value), true),
);
action("query", async () => {
  offset = 0;
  await query();
});
// 下一页按钮由查询结果决定可用状态，不能使用会无条件重新启用按钮的 action 包装。
$("next").addEventListener("click", async () => {
  try {
    offset = nextOffset;
    await query();
  } catch (error) {
    notice(error.message, true);
  }
});
action("delete", async () => {
  const dialog = $("confirm");
  dialog.returnValue = "";
  const answer = new Promise((resolve) =>
    dialog.addEventListener("close", () => resolve(dialog.returnValue), { once: true }),
  );
  dialog.showModal();
  if ((await answer) !== "yes") return;
  await Database.Delete($("collection").value, $("record-id").value, Number($("revision").value));
  $("revision").value = "0";
  databaseResult({ deleted: true });
});
$("kind").addEventListener("change", refresh);
$("state").addEventListener("change", refresh);
// 离页取消本页 JS 回调与排队下载；文件 token 由下载终态或宿主窗口关闭后回收，避免过早撤销写入凭证。
window.addEventListener("pagehide", () => {
  disposed = true;
  clearInterval(timer);
  Tasks.dispose();
  for (const id of grants.keys()) void Tasks.Cancel(id).catch(() => {});
});
void start();
