import { Hybrid, Host, Desktop, Documents, Tasks } from "./host-client.js";

/** 根据固定 ID 查找文档中心控件。 */
const $ = (id) => document.getElementById(id);
let capabilities = new Set(),
  clearPreview,
  offDrop,
  disposed = false,
  loading = false;
const sample = {
  headers: ["订单号", "客户", "金额", "状态"],
  rows: [
    ["SO-001", "示例客户 A", "128.00", "待发货"],
    ["SO-002", "示例客户 B", "256.50", "已完成"],
  ],
};

/** 展示真实结果与错误，全部使用纯文本，不把导入内容当作页面脚本。 */
function notice(message, error = false) {
  $("notice").textContent = message;
  $("notice").classList.toggle("error", error);
}

/** 读取用户编辑的表格 JSON；最终结构和容量仍由宿主重新校验。 */
function table() {
  return JSON.parse($("table-json").value);
}

/** 组合当前报表参数，分页预览仅截显示，实际打印和导出始终使用全表。 */
function report() {
  return {
    ...table(),
    title: $("report-title").value,
    paper: $("paper").value,
    orientation: $("orientation").value,
    footer: "由 Hybrid 业务文档中心生成",
  };
}

/** 替换本页报表预览并清理旧 DOM，只展示前 100 行以便大表格仍能流畅编辑。 */
function preview() {
  const value = report();
  if (!Array.isArray(value.rows)) throw new TypeError("rows 应为数组");
  clearPreview?.();
  clearPreview = Documents.preview($("report-preview"), {
    ...value,
    rows: value.rows.slice(0, 100),
  });
  $("preview-count").textContent = `${value.rows.length} 行 · ${value.headers.length} 列`;
}

/** 显示一份校验通过的导入结果和保护说明，保留可编辑 JSON，不写入业务数据库。 */
function showTable(value, name) {
  if (disposed) return;
  $("table-json").value = JSON.stringify({ headers: value.headers, rows: value.rows }, null, 2);
  $("table-summary").textContent =
    `${name} · ${value.rows.length} 行 · ${value.headers.length} 列${value.warnings?.length ? " · " + value.warnings.join("；") : ""}`;
  preview();
  notice(`已载入 ${name}，检查后可导出或打印。`);
}

/** 依次校验最多十个用户文件，保留每个文件的结果入口，不自动合并不同表头。 */
async function filesSelected(files) {
  if (loading) throw new Error("正在处理上一批文件，请稍候");
  if (files.length > 10) throw new Error("一次最多 10 个文件");
  loading = true;
  $("file-list").replaceChildren();
  try {
    for (const file of files) {
      if (disposed) break;
      const button = document.createElement("button"),
        li = document.createElement("li");
      button.textContent = `${file.name} · 正在校验`;
      button.disabled = true;
      li.append(button);
      $("file-list").append(li);
      try {
        const value = await Documents.readFile(file);
        if (disposed) break;
        button.textContent = `${file.name} · ${value.rows.length} 行`;
        button.disabled = false;
        button.addEventListener("click", () => showTable(value, file.name));
        showTable(value, file.name);
      } catch (error) {
        button.textContent = `${file.name} · ${error.message}`;
        button.className = "error";
        notice(error.message, true);
      }
    }
  } finally {
    loading = false;
  }
}

/** 绑定主动操作，处理等待和错误；权限变化由启动后固定的授权状态决定，不自动增加权限。 */
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

/** 使用宿主解析器重新校验编辑内容，更新预览并保留公式样式文本的警告。 */
async function validate() {
  const bytes = new TextEncoder().encode(JSON.stringify(table()));
  if (bytes.length > 384 * 1024) throw new RangeError("表格 JSON 超过 384 KiB");
  let binary = "";
  for (let i = 0; i < bytes.length; i += 32768)
    binary += String.fromCharCode(...bytes.subarray(i, i + 32768));
  const value = await Documents.Parse("json", btoa(binary));
  showTable(value, "编辑数据");
  return value;
}

/** 用户选定位置后导出；有任务能力时接入队列进度，结束或失败均释放保存凭证。 */
async function exportTable() {
  const value = await validate(),
    format = $("format").value;
  const grant = await Desktop.SelectFile("save", `report.${format}`);
  try {
    let result;
    if (capabilities.has("tasks")) {
      const task = await Tasks.run(
        `导出 ${format.toUpperCase()} 表格`,
        async ({ signal, report }) => {
          await report(10, "正在生成文件");
          const result = await Hybrid.call(() => Documents.ExportFile(grant.token, format, value), {
            signal,
            timeoutMs: 60000,
          });
          await report(100, "文件已保存");
          return result;
        },
      );
      result = task.value;
    } else
      result = await Hybrid.call(() => Documents.ExportFile(grant.token, format, value), {
        timeoutMs: 60000,
      });
    notice(`导出完成：${format.toUpperCase()} · ${result.bytes} 字节`);
  } finally {
    await Desktop.ReleaseFile(grant.token);
  }
}

action("sample", async () => showTable(sample, "示例数据"));
action("preview", async () => {
  preview();
  notice("预览已更新");
});
action("validate", validate);
action("export", exportTable);
action("print", async () => {
  await validate();
  await Documents.printReport(report());
  notice("已打开系统默认浏览器，请在报表页点击“打印 / 另存为 PDF”。");
});
action("native-import", async () => {
  const grant = await Desktop.SelectFile("open", "");
  try {
    const format = grant.name.split(".").pop().toLowerCase();
    showTable(await Documents.ImportFile(grant.token, format), grant.name);
  } finally {
    await Desktop.ReleaseFile(grant.token);
  }
});
// 标准文件选择与拖放走同一条校验路径；组件卸载后不再更新页面。
$("file-input").addEventListener("change", () => {
  const files = Array.from($("file-input").files || []);
  void filesSelected(files).catch((error) => notice(error.message, true));
  $("file-input").value = "";
});
$("drop-zone").addEventListener("keydown", (event) => {
  if (event.target === $("drop-zone") && (event.key === "Enter" || event.key === " ")) {
    event.preventDefault();
    $("file-input").click();
  }
});

/** 初始化连接和权限提示，普通预览可离线使用；导入、导出和原生打印仍需各自授权。 */
async function start() {
  showTable(sample, "示例数据");
  try {
    const info = await Hybrid.connect();
    capabilities = new Set(info.capabilities);
    await Host.Ready();
    const documents = capabilities.has("documents"),
      files = capabilities.has("files");
    for (const id of ["validate", "file-input"]) $(id).disabled = !documents;
    $("native-import").disabled = !documents || !files;
    $("export").disabled = !documents || !files;
    $("print").disabled = !documents || !capabilities.has("printing");
    if (documents)
      offDrop = Documents.onDrop($("drop-zone"), filesSelected, {
        onError: (error) => notice(error.message, true),
      });
    notice(
      `宿主已连接 · 文件处理 ${documents ? "已授权" : "需 documents"} · 打印 ${capabilities.has("printing") ? "已授权" : "需 printing"} · 原生文件选择 ${files ? "已授权" : "需 files"}`,
    );
  } catch (error) {
    notice(`连接失败：${error.message}`, true);
    for (const id of ["native-import", "export", "print", "validate", "file-input"])
      $(id).disabled = true;
  }
}
window.addEventListener("pagehide", () => {
  disposed = true;
  offDrop?.();
  clearPreview?.();
  Documents.dispose();
});
void start();
