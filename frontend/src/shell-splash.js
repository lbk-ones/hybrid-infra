import { Host, Hybrid } from "./host-client.js";

const title = document.getElementById("splash-title");
const message = document.getElementById("splash-message");
const version = document.getElementById("splash-version");
const skip = document.getElementById("splash-skip");
let timer;
let polling = false;
let leaving = false;

/** 只更新真实加载阶段，不模拟百分比；通信失败仍保留动画，由宿主的独立兜底计时器负责展示主窗口。 */
async function refresh() {
  if (polling || leaving) return;
  polling = true;
  try {
    const state = await Hybrid.call(() => Host.GetStartupState(), { timeoutMs: 2000 });
    title.textContent = state.title || "Hybrid";
    version.textContent = `v${state.version}`;
    message.textContent =
      {
        starting: "正在准备桌面环境",
        loading: "正在加载工作空间",
        ready: "一切就绪，即将进入",
        error: "正在打开加载恢复页面",
      }[state.status] || "正在连接工作空间";
  } catch {
    message.textContent = "正在连接桌面宿主";
  } finally {
    polling = false;
  }
}

/** 用户跳过时立即交由 Go 显示主窗口，重复点击和离开页面不会再发起请求。 */
async function dismiss() {
  if (leaving || skip.disabled) return;
  skip.disabled = true;
  try {
    await Hybrid.call(() => Host.DismissSplash(), { timeoutMs: 3000 });
  } catch {
    skip.disabled = false;
    message.textContent = "暂时无法切换，请稍候";
  }
}

/** 停止状态轮询，避免窗口销毁后仍访问绑定或更新 DOM。 */
function cleanup() {
  leaving = true;
  clearInterval(timer);
}

skip.addEventListener("click", dismiss);
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape") void dismiss();
});
window.addEventListener("pagehide", cleanup, { once: true });
void refresh();
timer = setInterval(refresh, 400);
