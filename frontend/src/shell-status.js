import { Host, Hybrid, normalizeError } from "./host-client.js";
const title = document.getElementById("status-title");
const message = document.getElementById("status-message");
const buttons = [...document.querySelectorAll("button")];
/** 将加载或通信错误转换为可读提示，显示错误码并停止加载动画。 */
function errorState(error) {
  const value = normalizeError(error);
  title.textContent = "暂时无法打开工作空间";
  message.textContent = value.message;
  document.getElementById("status-code").textContent = value.code;
  document.getElementById("loader").hidden = true;
}
/** 从宿主读取当前窗口的真实加载状态，并在地址无响应或加载失败时显示恢复入口。 */
async function refresh() {
  try {
    const state = await Hybrid.call(() => Host.GetLoadState(), {
      timeoutMs: 5000,
    });
    document.getElementById("status-address").textContent = state.url;
    if (state.error) errorState(state.error);
  } catch (error) {
    errorState(error);
  }
}
/** 重试当前目标页，或在 home 为 true 时返回内置首页；成功后保持忙碌状态，等待新页面接管界面。 */
async function navigate(home) {
  buttons.forEach((button) => {
    button.disabled = true;
  });
  title.textContent = home ? "正在返回内置首页" : "正在重新连接工作空间";
  message.textContent = "页面加载中，请稍候。";
  document.getElementById("status-code").textContent = "";
  document.getElementById("loader").hidden = false;
  try {
    await Hybrid.call(() => Host.Retry(home), { timeoutMs: 5000 });
  } catch (error) {
    errorState(error);
    buttons.forEach((button) => {
      button.disabled = false;
    });
  }
}
document
  .getElementById("retry")
  .addEventListener("click", () => navigate(false));
document.getElementById("home").addEventListener("click", () => navigate(true));
refresh();
