import { System } from "/wails/runtime.js";

let data;
let start;
let ended = false;
let imageReady = false;

/** 读取宿主注入的冻结画面，背景只在本地 WebView 内使用，不发送到其他业务窗口或网络。 */
function configure() {
  if (data) return;
  const next = window.__HYBRID_CAPTURE_DATA__;
  if (!next) return;
  data = next;
  const image = document.getElementById("capture-background");
  image.onload = () => {
    imageReady = true;
  };
  image.onerror = cancel;
  image.src = data.image;
}

/** 将鼠标位置限制在当前遮罩 viewport 中，不使用屏幕 CSS 坐标直接猜测物理像素。 */
function point(event) {
  return {
    x: Math.max(0, Math.min(innerWidth, event.clientX)),
    y: Math.max(0, Math.min(innerHeight, event.clientY)),
  };
}

/** 以起终点计算正向矩形，允许从任意方向拖动。 */
function rectangle(end) {
  return {
    x: Math.min(start.x, end.x),
    y: Math.min(start.y, end.y),
    width: Math.abs(start.x - end.x),
    height: Math.abs(start.y - end.y),
  };
}

/** 更新选区边框和物理像素尺寸提示，实际换算仍由 Go 根据显示器信息核验。 */
function draw(end) {
  const rect = rectangle(end);
  const element = document.getElementById("capture-region");
  element.hidden = false;
  element.style.left = `${rect.x}px`;
  element.style.top = `${rect.y}px`;
  element.style.width = `${rect.width}px`;
  element.style.height = `${rect.height}px`;
  document.getElementById("capture-shade").hidden = true;
  document.getElementById("capture-size").textContent =
    `${Math.round((rect.width * data.width) / innerWidth)} × ${Math.round((rect.height * data.height) / innerHeight)}`;
}

/** 原生消息带随机会话凭证，只提交选择意图；来源和原生窗口由 Go 重新核对。 */
function send(type, fields = {}) {
  if (!data || ended) return;
  ended = true;
  System.invoke("hybrid:" + JSON.stringify({ type, token: data.token, ...fields }));
}

/** 取消当前框选，关闭和数据清理由宿主统一处理。 */
function cancel() {
  send("capture-cancel");
}

window.addEventListener("hybrid:capture-config", configure);
document.addEventListener("pointerdown", (event) => {
  if (event.button !== 0 || !imageReady || ended) return;
  start = point(event);
  document.body.setPointerCapture(event.pointerId);
  draw(start);
});
document.addEventListener("pointermove", (event) => {
  if (start && !ended) draw(point(event));
});
document.addEventListener("pointerup", (event) => {
  if (!start || ended) return;
  const rect = rectangle(point(event));
  if (rect.width < 2 || rect.height < 2) {
    start = undefined;
    document.getElementById("capture-region").hidden = true;
    document.getElementById("capture-shade").hidden = false;
    return;
  }
  send("capture-select", { ...rect, viewportWidth: innerWidth, viewportHeight: innerHeight });
});
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape") cancel();
});
document.addEventListener("contextmenu", (event) => {
  event.preventDefault();
  cancel();
});
configure();
