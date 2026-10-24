import { Host, Windows, Hybrid, normalizeError } from "./host-client.js";
const notice = document.getElementById("notice");
const windows = document.getElementById("windows");
let demoTask;
let currentWindowID;
let busy = false;
/** 用纯文本创建表格单元格，避免把页面地址、标题等动态内容当成 HTML 执行。 */
function cell(text) {
  const element = document.createElement("td");
  element.textContent = text;
  return element;
}
/** 把管理操作失败转换为带统一错误码的可见提示。 */
function showError(error) {
  const value = normalizeError(error);
  notice.textContent = `${value.code} · ${value.message}`;
  notice.className = "notice error";
}
/** 统一执行管理操作，阻止重复触发，并在成功或失败后恢复按钮和示例任务入口状态。 */
async function run(operation) {
  if (busy) return;
  busy = true;
  const buttons = [...document.querySelectorAll("button")];
  buttons.forEach((button) => {
    button.disabled = true;
  });
  try {
    await operation();
    notice.textContent = "";
    notice.className = "";
  } catch (error) {
    showError(error);
  } finally {
    busy = false;
    buttons.forEach((button) => {
      button.disabled = false;
    });
    document.getElementById("begin-task").disabled = Boolean(demoTask);
    document.getElementById("end-task").disabled = !demoTask;
  }
}
/** 并行读取宿主窗口和任务列表，再用真实数据重绘管理区域；只为当前窗口所属任务提供结束入口。 */
async function refresh() {
  const [items, tasks] = await Promise.all([
    Hybrid.call(() => Windows.List()),
    Hybrid.call(() => Host.GetPendingTasks()),
  ]);
  windows.replaceChildren();
  for (const item of items) {
    const row = document.createElement("tr");
    const name = cell(item.id);
    const subtitle = document.createElement("small");
    subtitle.textContent = item.title || "未命名窗口";
    name.append(subtitle);
    row.append(
      name,
      cell(item.url),
      cell(
        {
          ready: "就绪",
          loading: "加载中",
          starting: "启动中",
          error: "加载失败",
        }[item.status] || item.status,
      ),
    );
    const actions = cell("");
    actions.className = "actions";
    for (const [label, operation] of [
      ["聚焦", () => Windows.Focus(item.id)],
      ["关闭", () => Windows.Close(item.id)],
    ]) {
      const button = document.createElement("button");
      button.textContent = label;
      button.addEventListener("click", () =>
        run(async () => {
          await Hybrid.call(operation);
          await refresh();
        }),
      );
      actions.append(button);
    }
    row.append(actions);
    windows.append(row);
  }
  const taskList = document.getElementById("tasks");
  taskList.replaceChildren();
  if (!tasks.length) {
    const empty = document.createElement("p");
    empty.className = "panel-description";
    empty.textContent = "暂无未完成任务";
    taskList.append(empty);
  }
  for (const task of tasks) {
    const row = document.createElement("div");
    row.className = "task-row";
    const label = document.createElement("span");
    label.textContent = `${task.label} · ${task.windowId} · ${new Date(task.createdAt).toLocaleTimeString()}`;
    row.append(label);
    if (task.windowId === currentWindowID) {
      const end = document.createElement("button");
      end.textContent = "结束任务";
      end.addEventListener("click", () =>
        run(async () => {
          await Hybrid.call(() => Host.EndTask(task.id));
          if (demoTask?.id === task.id) demoTask = undefined;
          await refresh();
        }),
      );
      row.append(end);
    }
    taskList.append(row);
  }
}
document
  .getElementById("refresh")
  .addEventListener("click", () => run(refresh));
document.getElementById("open-window").addEventListener("click", () =>
  run(async () => {
    await Hybrid.call(() =>
      Windows.Open({
        name: "demo-window",
        title: "示例工作空间",
        url: "/",
        width: 1000,
        height: 760,
      }),
    );
    await refresh();
  }),
);
document.getElementById("begin-task").addEventListener("click", () =>
  run(async () => {
    demoTask = await Hybrid.call(() => Host.BeginTask("宿主管理示例任务"));
    await refresh();
  }),
);
document.getElementById("end-task").addEventListener("click", () =>
  run(async () => {
    await Hybrid.call(() => Host.EndTask(demoTask.id));
    demoTask = undefined;
    await refresh();
  }),
);
run(async () => {
  const info = await Hybrid.connect({
    requiredCapabilities: ["host", "windows", "lifecycle"],
  });
  currentWindowID = (await Hybrid.call(() => Host.GetLoadState())).id;
  document.getElementById("host-version").textContent = info.version;
  document.getElementById("host-platform").textContent =
    `v${info.protocolVersion} / ${info.platform}`;
  document.getElementById("host-close").textContent =
    info.closeBehavior === "hide" ? "隐藏到托盘" : "退出应用";
  document.getElementById("host-single").textContent = info.singleInstance
    ? "已启用"
    : "已关闭";
  document.getElementById("connection-state").textContent = "宿主已连接";
  const capabilities = document.getElementById("capabilities");
  for (const name of info.capabilities) {
    const badge = document.createElement("span");
    badge.textContent = name;
    capabilities.append(badge);
  }
  await refresh();
});
