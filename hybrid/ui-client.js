/** 创建 UI 门面，保留旧四参数 Alert，同时为消息框和通知提供易用的对象参数与拖拽开关。 */
export function createUI(bindings) {
  /** 显示消息框；支持 Alert({title,body,destroy,place,draggable}) 或旧四参数加第五个选项对象。 */
  function Alert(title, body, destroy = 0, place = "center", options) {
    if (title && typeof title === "object") return bindings.AlertWithOptions(title);
    if (options !== undefined)
      return bindings.AlertWithOptions({
        title,
        body,
        destroy,
        place,
        draggable: options.draggable,
      });
    return bindings.Alert(title, body, destroy, place);
  }
  /** 显示纯文本通知，支持 Notify({text,...}) 或 Notify(text,{place,durationSeconds,draggable})，默认右上角四秒。 */
  function Notify(text, options = {}) {
    return bindings.Notify(typeof text === "string" ? { ...options, text } : text);
  }
  return { ...bindings, Alert, Notify };
}
