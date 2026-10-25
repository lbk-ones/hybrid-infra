import { Maintenance, Desktop, Host, Hybrid, normalizeError } from "./host-client.js";
/** 通过固定 ID 查询维护页面控件。 */
const $ = (id) => document.getElementById(id);
let capabilities = new Set();
let busy = false;
let update = {};
let timer;
let polling = false;
const stages = {
  idle: "尚未检查",
  available: "发现新版本",
  up_to_date: "已是最新版本",
  downloading: "正在下载",
  ready: "已校验，可安装",
  installed: "更新成功",
  rolled_back: "启动失败，已自动回滚",
  failed: "更新失败",
};

/** 将危险操作的具体影响放入原生 HTML 对话框，关闭或取消均不执行。 */
function confirmAction(title, text) {
  $("confirm-title").textContent = title;
  $("confirm-text").textContent = text;
  const dialog = $("confirmation");
  return new Promise((resolve) => {
    dialog.addEventListener("close", () => resolve(dialog.returnValue === "confirm"), {
      once: true,
    });
    dialog.returnValue = "cancel";
    dialog.showModal();
  });
}
/** 按真实能力与更新阶段启用按钮，后端仍会独立执行授权和任务保护。 */
function controls() {
  for (const section of document.querySelectorAll("[data-cap]")) {
    const allowed = capabilities.has(section.dataset.cap);
    section.querySelector(".cap-state").textContent = allowed
      ? "已授权"
      : `未授权 · ${section.dataset.cap}`;
    section.classList.toggle("unavailable", !allowed);
    for (const button of section.querySelectorAll("button")) button.disabled = !allowed || busy;
  }
  $("diagnostics-export").disabled ||= !capabilities.has("files");
  $("update-download").disabled ||= update.stage !== "available";
  $("update-install").disabled ||= update.stage !== "ready";
  $("update-cancel").disabled ||= update.stage !== "downloading";
  $("update-check").disabled ||= !update.enabled || update.stage === "downloading";
  $("update-rollback").disabled ||= !capabilities.has("backups") || !update.canRollback;
}
/** 统一显示操作结果及错误码，避免重复提交维护任务。 */
async function run(operation, message = "操作完成") {
  if (busy) return;
  busy = true;
  controls();
  try {
    await operation();
    $("notice").textContent = message;
  } catch (error) {
    const value = normalizeError(error);
    $("notice").textContent = `${value.code} · ${value.message}`;
  } finally {
    busy = false;
    controls();
  }
}
/** 将指标值作为纯文本展示，不使用服务数据拼接 HTML。 */
async function diagnostics() {
  const report = await Hybrid.call(() => Maintenance.GetDiagnostics());
  $("metrics").replaceChildren();
  const metrics = {
    应用版本: report.version,
    数据版本: `Schema ${report.dataSchema}`,
    平台: `${report.platform} / ${report.arch}`,
    内存: `${(report.memoryBytes / 1048576).toFixed(1)} MiB`,
    "窗口 / 任务": `${report.windowCount} / ${report.taskCount}`,
    "Go / 协程": `${report.goVersion} / ${report.goroutines}`,
  };
  for (const [label, value] of Object.entries(metrics)) {
    const card = document.createElement("div"),
      caption = document.createElement("span"),
      text = document.createElement("strong");
    caption.textContent = label;
    text.textContent = value;
    card.append(caption, text);
    $("metrics").append(card);
  }
  $("events").textContent =
    report.events.map((event) => `${event.time}  ${event.code}`).join("\n") || "暂无诊断事件";
}
/** 根据宿主返回的签名更新进度刷新展示，不在前端猜测安装结果。 */
async function updateState() {
  update = await Hybrid.call(() => Maintenance.GetUpdateState());
  $("update-state").textContent =
    `${update.enabled ? stages[update.stage] || update.stage : "更新未启用"}${update.version ? ` · v${update.version}` : ""}${update.code ? ` · ${update.code}` : ""}`;
  $("update-progress").value = update.progress || 0;
  controls();
}
/** 列出完整验证过的备份；恢复前展示对应版本及覆盖范围。 */
async function backups() {
  const list = await Hybrid.call(() => Maintenance.ListBackups());
  $("backups").replaceChildren();
  for (const item of list) {
    const row = document.createElement("article"),
      details = document.createElement("div"),
      title = document.createElement("strong"),
      meta = document.createElement("p"),
      button = document.createElement("button");
    title.textContent = new Date(item.created).toLocaleString();
    meta.textContent = `v${item.version} · Schema ${item.schema} · ${(item.size / 1024).toFixed(1)} KiB · ${item.id.slice(0, 8)}`;
    button.textContent = "下次启动恢复";
    // 使用闭包保留已验证 ID；确认后仅提交 ID，不接受路径或文件名。
    button.addEventListener("click", () =>
      run(async () => {
        if (
          !(await confirmAction(
            "排队恢复快照",
            `下次启动将用 ${title.textContent} 的业务数据覆盖当前 KV、偏好和窗口状态。恢复前会另存当前数据。`,
          ))
        )
          return;
        await Hybrid.call(() => Maintenance.StageRestore(item.id));
        $("backups").prepend(document.createTextNode("恢复已排队，请关闭应用后重新启动。"));
      }),
    );
    details.append(title, meta);
    row.append(details, button);
    $("backups").append(row);
  }
  if (!list.length) $("backups").textContent = "暂无快照";
  controls();
}
/** 绑定按钮并将异常统一交给页面提示处理。 */
function bind(id, operation, message) {
  $(id).addEventListener("click", () => run(operation, message));
}
bind("diagnostics-refresh", diagnostics);
bind("diagnostics-export", async () => {
  const grant = await Hybrid.call(() => Desktop.SelectFile("save", "hybrid-diagnostics.json"), {
    timeoutMs: 300000,
  });
  try {
    await Hybrid.call(() => Maintenance.ExportDiagnostics(grant.token));
  } finally {
    await Hybrid.call(() => Desktop.ReleaseFile(grant.token));
  }
});
bind(
  "backup-create",
  async () => {
    await Hybrid.call(() => Maintenance.CreateBackup());
    await backups();
  },
  "快照已创建",
);
bind("backup-refresh", backups);
bind("update-check", async () => {
  await Hybrid.call(() => Maintenance.CheckUpdates(), { timeoutMs: 60000 });
  await updateState();
});
bind(
  "update-download",
  async () => {
    await Hybrid.call(() => Maintenance.DownloadUpdate());
    await updateState();
  },
  "更新正在后台下载",
);
bind(
  "update-cancel",
  async () => {
    await Hybrid.call(() => Maintenance.CancelUpdate());
    await updateState();
  },
  "已请求取消下载",
);
bind("update-install", async () => {
  if (
    await confirmAction(
      "安装并重启",
      `将安装已校验的 v${update.version}。应用会关闭并重新启动；启动失败自动还原程序和数据。`,
    )
  )
    await Hybrid.call(() => Maintenance.InstallUpdate());
});
bind("update-rollback", async () => {
  if (
    await confirmAction(
      "回滚程序与数据",
      "将恢复上一个版本及其更新前的数据。之后产生的业务数据将另存快照；应用会重启。",
    )
  )
    await Hybrid.call(() => Maintenance.RollbackUpdate());
});

/** 建立连接后只读取已授权区域；隐藏页面停止轮询，卸载后清理计时器。 */
async function initialize() {
  const info = await Hybrid.connect();
  capabilities = new Set(info.capabilities);
  await Hybrid.call(() => Host.Ready());
  controls();
  if (capabilities.has("diagnostics")) await diagnostics();
  if (capabilities.has("backups")) await backups();
  if (capabilities.has("updates")) await updateState();
  // 只轮询更新进度，诊断和备份由用户显式刷新，避免重复读取整个备份文件。
  timer = setInterval(async () => {
    if (document.hidden || polling || busy || !capabilities.has("updates")) return;
    polling = true;
    try {
      await updateState();
    } catch (error) {
      $("notice").textContent = normalizeError(error).message;
    } finally {
      polling = false;
    }
  }, 1500);
}
window.addEventListener("pagehide", () => clearInterval(timer));
controls();
run(initialize, "维护中心已连接；未授权功能请在启动配置中按需开启");
