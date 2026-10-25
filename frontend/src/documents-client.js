/** 创建文档客户端；表格解析和文件写入由宿主授权，拖放回调与报表 DOM 始终保留在当前页面。 */
export function createDocuments(service) {
  const previews = new Set();

  /** 校验报表内容并创建文本节点，业务值不会当作 HTML、脚本或样式执行。 */
  function reportNode(report) {
    if (
      !report ||
      !Array.isArray(report.headers) ||
      !report.headers.length ||
      report.headers.length > 100 ||
      !Array.isArray(report.rows) ||
      report.rows.length > 5000
    )
      throw new TypeError("报表需要 1–100 列，最多 5000 行");
    if (new TextEncoder().encode(JSON.stringify(report)).length > 384 * 1024)
      throw new RangeError("报表 JSON 不能超过 384 KiB");
    const root = document.createElement("article");
    root.className = "hybrid-report";
    for (const [tag, value] of [
      ["h1", report.title || "业务报表"],
      ["p", report.subtitle || ""],
    ]) {
      const node = document.createElement(tag);
      node.textContent = String(value);
      root.append(node);
    }
    const table = document.createElement("table"),
      head = document.createElement("thead"),
      body = document.createElement("tbody");
    for (const [index, row] of [report.headers, ...report.rows].entries()) {
      if (
        !Array.isArray(row) ||
        row.length !== report.headers.length ||
        row.some((cell) => typeof cell !== "string")
      )
        throw new TypeError("表头与每行必须是等长的字符串数组");
      const tr = document.createElement("tr");
      for (const value of row) {
        const cell = document.createElement(index === 0 ? "th" : "td");
        cell.textContent = value;
        tr.append(cell);
      }
      (index === 0 ? head : body).append(tr);
    }
    table.append(head, body);
    root.append(table);
    const footer = document.createElement("p");
    footer.className = "hybrid-report-footer";
    footer.textContent = String(report.footer || "");
    root.append(footer);
    return root;
  }

  /** 安装页面预览样式；纸张与方向先校验，实际打印布局由宿主的固定模板生成。 */
  function reportStyle(paper = "A4", orientation = "portrait") {
    if (!["A4", "Letter"].includes(paper) || !["portrait", "landscape"].includes(orientation))
      throw new TypeError("纸张仅支持 A4/Letter，方向为 portrait/landscape");
    const style = document.createElement("style");
    style.textContent = `.hybrid-report{background:#fff;color:#172a20;padding:24px;font:13px/1.6 'Segoe UI','Microsoft YaHei',sans-serif}.hybrid-report h1{font-size:23px;margin:0 0 8px}.hybrid-report p{margin:8px 0;color:#596b60;white-space:pre-wrap}.hybrid-report table{width:100%;border-collapse:collapse;table-layout:fixed}.hybrid-report th,.hybrid-report td{border:1px solid #cbd8cf;padding:8px;text-align:left;overflow-wrap:anywhere;white-space:pre-wrap}.hybrid-report th{background:#edf4ef}.hybrid-report-footer{margin-top:16px!important}`;
    return style;
  }

  /** 渲染可见报表预览，返回只清理本次预览的函数；不修改业务数据，不弹出系统对话框。 */
  function preview(target, report) {
    const parent = typeof target === "string" ? document.querySelector(target) : target;
    if (!parent?.append) throw new TypeError("预览目标不存在");
    const node = reportNode(report),
      style = reportStyle(report.paper, report.orientation);
    parent.append(style, node);
    const clear = () => {
      node.remove();
      style.remove();
      previews.delete(clear);
    };
    previews.add(clear);
    return clear;
  }

  /** 打开默认 HTML 查看程序中的本地报表，用户再点击打印或另存 PDF；请求成功不代表实际完成。 */
  async function printReport(report) {
    await service.OpenPrintPreview(report);
    return { requested: true, mode: "external-preview" };
  }

  /** 从用户拖入或选中的 File 读取最多 512 KiB，按扩展名识别格式，不依赖虚构的本地路径。 */
  async function readFile(file, format) {
    if (!file || typeof file.arrayBuffer !== "function" || file.size < 1 || file.size > 512 * 1024)
      throw new RangeError("文件需要 1 字节至 512 KiB");
    format ??= file.name?.split(".").pop()?.toLowerCase();
    if (!["csv", "json", "xlsx", "zip"].includes(format))
      throw new TypeError("仅支持 CSV、JSON、XLSX 和表格 ZIP");
    const bytes = new Uint8Array(await file.arrayBuffer());
    if (bytes.length > 512 * 1024) throw new RangeError("文件超过 512 KiB");
    let binary = "";
    for (let offset = 0; offset < bytes.length; offset += 32768)
      binary += String.fromCharCode(...bytes.subarray(offset, offset + 32768));
    return service.Parse(format, btoa(binary));
  }

  /** 监听指定元素的文件拖入；每次最多 10 个，仅交给本页回调，不自动上传或读取，返回解除监听函数。 */
  function onDrop(target, handler, { onError = () => {} } = {}) {
    const element = typeof target === "string" ? document.querySelector(target) : target;
    if (!element?.addEventListener || typeof handler !== "function")
      throw new TypeError("拖放目标和回调无效");
    let closed = false;
    /** 仅阻止文件拖放的默认导航，保留普通文本拖放行为。 */
    const over = (event) => {
      if (Array.from(event.dataTransfer?.types || []).includes("Files")) {
        event.preventDefault();
        // 固定 Wails 运行时在祖先元素处理拖放并设置禁止光标；当前区域自己处理文件，不向原生路径通道转发。
        event.stopPropagation();
        event.dataTransfer.dropEffect = "copy";
      }
    };
    /** 文件列表只存在于本页；回调失败交给 onError，解除监听后不再投递新回调。 */
    const drop = (event) => {
      const files = Array.from(event.dataTransfer?.files || []);
      if (!files.length) return;
      event.preventDefault();
      event.stopPropagation();
      if (closed) return;
      void Promise.resolve()
        .then(() => {
          if (closed) return;
          if (files.length > 10) throw new RangeError("一次最多拖入 10 个文件");
          return handler(files);
        })
        .catch((error) => {
          if (!closed) {
            try {
              onError(error);
            } catch {}
          }
        });
    };
    element.addEventListener("dragenter", over);
    element.addEventListener("dragover", over);
    element.addEventListener("drop", drop);
    return () => {
      closed = true;
      element.removeEventListener("dragenter", over);
      element.removeEventListener("dragover", over);
      element.removeEventListener("drop", drop);
    };
  }

  /** 整页卸载时清理本客户端创建的预览 DOM；拖放监听由其返回函数清理，不强制关闭用户的外部查看程序。 */
  function dispose() {
    for (const clear of previews) clear();
  }
  return { ...service, preview, printReport, readFile, onDrop, dispose };
}
