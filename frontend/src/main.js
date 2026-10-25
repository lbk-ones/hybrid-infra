import { isDesktopShell } from "./environment.js";
const paths = {
  grid: '<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
  activity: '<path d="M3 12h4l3-7 4 14 3-7h4"/>',
  position:
    '<rect x="4" y="4" width="16" height="16" rx="3"/><path d="M12 1v6m0 10v6M1 12h6m10 0h6"/>',
  bell: '<path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9M10 21h4M12 2V1"/>',
  check: '<circle cx="12" cy="12" r="9"/><path d="m8 12 3 3 5-6"/>',
  layers: '<path d="m12 3 10 5-10 5L2 8l10-5Zm-10 9 10 5 10-5M2 16l10 5 10-5"/>',
  window:
    '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 9h18M6 6.5h.01M9 6.5h.01m4 9 4-4m-4 0h4v4"/>',
};
document.querySelectorAll("[data-icon]").forEach((el) => {
  el.innerHTML = `<svg viewBox="0 0 24 24" aria-hidden="true">${paths[el.dataset.icon]}</svg>`;
});

/** 根据当前页面锚点更新导航高亮和无障碍当前位置标记。 */
function updateNavigation() {
  const current = location.hash === "#activity" ? "#activity" : "#workspace";
  document.querySelectorAll(".nav-item").forEach((link) => {
    const active = link.getAttribute("href") === current;
    link.classList.toggle("active", active);
    if (active) link.setAttribute("aria-current", "location");
    else link.removeAttribute("aria-current");
  });
}
window.addEventListener("hashchange", updateNavigation);
updateNavigation();

// 只在桌面宿主中加载原生绑定，普通浏览器使用页面内弹框预览。
const isDesktop = isDesktopShell();
document.getElementById("environment").textContent = isDesktop ? "正在连接宿主" : "浏览器预览模式";
if (isDesktop) {
  import("./host-client.js")
    .then(async ({ Hybrid, Host }) => {
      const info = await Hybrid.connect();
      await Hybrid.call(() => Host.Ready(), { timeoutMs: 5000 });
      document.getElementById("environment").textContent = `宿主 ${info.version} 已连接`;
    })
    .catch((error) => {
      document.getElementById("environment").textContent = "宿主连接失败";
      log(`宿主连接失败：${error.message}`, true);
    });
}
let servicePromise;
/** 按需加载并缓存原生弹框绑定；加载失败后清除缓存，允许下一次操作重新尝试。 */
const services = () =>
  (servicePromise ??= import("../bindings/hybrid-infra/go/services/uiservice.js").catch((error) => {
    servicePromise = undefined;
    throw error;
  }));
const list = document.getElementById("activity-list");
const emptyState = list.innerHTML;
const clearLog = document.getElementById("clear-log");
let recordCount = 0;
/** 将操作结果安全写入日志区域，更新累计次数，并只保留最近 30 条可见记录。 */
function log(message, error = false) {
  if (!recordCount) list.replaceChildren();
  const row = document.createElement("div");
  row.className = `log-row${error ? " error" : ""}`;
  const dot = document.createElement("span");
  dot.className = "status-dot";
  const text = document.createElement("span");
  text.textContent = message;
  const time = document.createElement("time");
  time.dateTime = new Date().toISOString();
  time.textContent = new Date().toLocaleTimeString("zh-CN", { hour12: false });
  row.append(dot, text, time);
  list.prepend(row);
  if (list.children.length > 30) list.lastElementChild.remove();
  document.getElementById("activity-count").textContent = String(++recordCount);
  clearLog.disabled = false;
}
clearLog.addEventListener("click", () => {
  list.innerHTML = emptyState;
  recordCount = 0;
  document.getElementById("activity-count").textContent = "0";
  clearLog.disabled = true;
});

const dialog = document.getElementById("preview-dialog");
/** 在普通浏览器中模拟弹框；confirm 控制取消按钮，timeout 为自动关闭的毫秒数，关闭后返回选择结果。 */
function preview(title, body, { confirm = false, timeout = 0 } = {}) {
  return new Promise((resolve) => {
    document.getElementById("preview-title").textContent = title;
    document.getElementById("preview-body").textContent = body;
    document.getElementById("preview-cancel").hidden = !confirm;
    dialog.returnValue = "0";
    const place = document.getElementById("position").value;
    dialog.style.margin = {
      center: "auto",
      topLeft: "20px auto auto 20px",
      topRight: "20px 20px auto auto",
      bottomLeft: "auto auto 20px 20px",
      bottomRight: "auto 20px 20px auto",
    }[place];
    let timer;
    // 弹框关闭后清理自动关闭计时器，并结束本次预览的等待。
    const onClose = () => {
      clearTimeout(timer);
      resolve(dialog.returnValue);
    };
    dialog.addEventListener("close", onClose, { once: true });
    dialog.showModal();
    if (timeout) timer = setTimeout(() => dialog.close("1"), timeout);
  });
}
document.getElementById("preview-ok").addEventListener("click", () => dialog.close("1"));
document.getElementById("preview-cancel").addEventListener("click", () => dialog.close("0"));
// 按 Escape 时使用浏览器 dialog 的取消行为，并把本次预览结果设为取消。
dialog.addEventListener("cancel", () => {
  dialog.returnValue = "0";
});

let pending = false;
const buttons = [...document.querySelectorAll(".card-button")];
/** 为能力按钮注册操作：阻止重复触发，传入选择的位置，统一记录错误并恢复按钮状态。 */
function action(id, label, run) {
  document.getElementById(id).addEventListener("click", async () => {
    if (pending) return;
    pending = true;
    buttons.forEach((button) => {
      button.disabled = true;
    });
    try {
      await run(document.getElementById("position").value);
    } catch (error) {
      log(`${label}失败：${error?.message || String(error)}`, true);
    } finally {
      pending = false;
      buttons.forEach((button) => {
        button.disabled = false;
      });
    }
  });
}
const mode = isDesktop ? "" : "（浏览器预览）";
action("alert", "消息提示", async (place) => {
  if (isDesktop)
    await (
      await services()
    ).Alert("操作成功", "这是一条来自 Hybrid 的消息，2 秒后自动关闭。", 2, place);
  else
    await preview("操作成功", "这是一条来自 Hybrid 的消息，2 秒后自动关闭。", {
      timeout: 2000,
    });
  log(`消息提示已发送${mode}`);
});
action("confirm", "操作确认", async (place) => {
  const result = isDesktop
    ? await (
        await services()
      ).Confirm({
        title: "继续此操作？",
        body: "点击确认继续，或取消返回工作台。",
        okText: "确认继续",
        cancelText: "取消",
        place,
      })
    : await preview("继续此操作？", "点击确认继续，或取消返回工作台。", {
        confirm: true,
      });
  log(`操作确认：${result === "1" ? "已确认" : "已取消"}${mode}`);
});
action("show", "自定义浮窗", async (place) => {
  if (isDesktop)
    await (
      await services()
    ).Show({
      body: '<div class="sample-card"><span class="sample-avatar">H</span><div><strong>你好，Hybrid</strong><p>让网页与桌面，自然连接。</p></div><button class="sample-close" aria-label="关闭浮窗" onclick="closeWindow()">×</button></div>',
      place,
      width: 340,
      height: 112,
    });
  else await preview("你好，Hybrid", "让网页与桌面，自然连接。");
  log(`自定义浮窗已展示${mode}`);
});
action("openBaidu", "独立窗口", async (place) => {
  if (isDesktop) {
    await (
      await services()
    ).OpenWindowByUrl({
      name: "demo-window",
      url: "/",
      title: "Hybrid · 示例工作空间",
      isFullscreen: false,
      alwaysOnTop: false,
      hiddenOnTaskbar: false,
      place,
      frameLess: false,
      width: 1000,
      height: 720,
    });
    log("示例窗口已打开，同一窗口 ID 会自动复用");
  } else {
    const newWindow = window.open("/", "hybrid-demo-window");
    if (!newWindow) throw new Error("浏览器拦截了新窗口，请允许弹出窗口后重试");
    newWindow.opener = null;
    log("示例页面已在新标签页打开（浏览器预览）");
  }
});
