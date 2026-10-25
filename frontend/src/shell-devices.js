import { Devices, Hybrid, Host } from "./host-client.js";

/** 取得设备中心的固定控件。 */
const $ = (id) => document.getElementById(id);
let capabilities = new Set(),
  offData,
  offScan,
  disposed = false;
const received = [];
/** 显示真实状态，不把设备内容作为 HTML 执行。 */
function notice(text, error = false) {
  $("notice").textContent = text;
  $("notice").classList.toggle("error", error);
}
/** 只保留最近一百条接收数据，避免持续称重或扫码占满页面内存。 */
function log(value) {
  if (disposed) return;
  received.push(`${new Date().toLocaleTimeString()}  ${value}`.slice(0, 4096));
  if (received.length > 100) received.shift();
  $("received").textContent = received.join("\n");
  $("received").scrollTop = $("received").scrollHeight;
}
/** 为下拉框填入真实枚举结果，保留仍有效的当前选择。 */
function options(id, items, empty) {
  const select = $(id),
    current = select.value;
  select.replaceChildren();
  if (!items.length) {
    const option = document.createElement("option");
    option.value = "";
    option.textContent = empty;
    select.append(option);
  }
  for (const [value, label] of items) {
    const option = document.createElement("option");
    option.value = value;
    option.textContent = label;
    select.append(option);
  }
  if (items.some(([value]) => value === current)) select.value = current;
}
/** 统一处理用户主动操作，等待时禁用当前按钮，避免重复发送或重复创建连接。 */
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
/** 读取本窗口连接，未连接时保持空状态，不使用模拟端口。 */
async function refresh() {
  const list = await Devices.ListConnections();
  options(
    "connection",
    list.map((item) => [item.id, `${item.kind} · ${item.name} · ${item.state}`]),
    "尚未连接",
  );
}
/** 取得用户当前选择的连接，没有选择时拒绝发送。 */
function connection() {
  if (!$("connection").value) throw new Error("请先连接并选择一个设备");
  return $("connection").value;
}
/** 按当前选择启动回调监听，文本秤帧只在识别成功时更新重量，不对未知协议猜测。 */
function listen() {
  const id = connection();
  offData?.();
  offData = Devices.onData(
    id,
    async (value) => {
      if (value instanceof Uint8Array) {
        log(Array.from(value, (byte) => byte.toString(16).padStart(2, "0")).join(" "));
        return;
      }
      log(value);
      if ($("receive-mode").value === "lines") {
        try {
          $("weight-result").textContent = JSON.stringify(Devices.parseWeight(value), null, 2);
        } catch {}
      }
    },
    {
      mode: $("receive-mode").value,
      encoding: $("encoding").value,
      onError: (error) => {
        notice(`${error.code || "E_DEVICE"} · ${error.message}`, true);
        void refresh().catch(() => {});
      },
    },
  );
  notice("已开始接收真实设备数据，回调在当前页面执行。");
}
/** 新连接成功后自动选中并监听，仅接收，不自动发送设备指令。 */
async function connected(value) {
  await refresh();
  $("connection").value = value.id;
  listen();
}
action("ports", async () => {
  options(
    "port",
    (await Devices.ListPorts()).map((name) => [name, name]),
    "未发现串口",
  );
  notice("串口枚举完成，尚未打开设备。");
});
action("serial-open", async () =>
  connected(
    await Devices.OpenSerial({
      port: $("port").value,
      baudRate: Number($("baud").value),
      dataBits: Number($("bits").value),
      parity: $("parity").value,
      stopBits: $("stop").value,
      dtr: $("dtr").checked,
      rts: $("rts").checked,
    }),
  ),
);
action("tcp-open", async () => connected(await Devices.OpenTCP($("endpoint").value.trim())));
action("connections", refresh);
action("listen", async () => listen());
action("stop-listen", async () => {
  offData?.();
  offData = undefined;
  notice("已停止回调监听；需要释放设备时请关闭连接。");
});
action("close", async () => {
  offData?.();
  offData = undefined;
  await Devices.Close(connection(), true);
  await refresh();
  notice("设备已关闭并移除。");
});
action("send", async () => {
  const written = await Devices.Write(
    connection(),
    Devices.encode($("payload").value, $("send-mode").value),
  );
  notice(`已向设备写入 ${written} 字节；这不是设备业务处理成功回执。`);
});
action("scanner-start", async () => {
  offScan?.();
  offScan = Devices.onScan($("scan-input"), async (code) => {
    $("scan-result").textContent = `收到条码：${code}`;
    $("scan-input").value = "";
  });
  $("scan-input").focus();
  notice("扫码监听已开启，请保持扫码输入框聚焦。");
});
action("scanner-stop", async () => {
  offScan?.();
  offScan = undefined;
  notice("扫码监听已停止。");
});
action("weight-parse", async () => {
  $("weight-result").textContent = JSON.stringify(
    Devices.parseWeight($("weight-frame").value),
    null,
    2,
  );
});
action("printers", async () => {
  options(
    "printer",
    (await Devices.ListPrinters()).map((name) => [name, name]),
    "未发现已安装打印机",
  );
  notice("已读取打印机名称，未发送打印任务。");
});
action("label-sample", async () => {
  $("print-data").value = "^XA\n^FO30,30^A0N,32,32^FDDEMO-001^FS\n^XZ\n";
  $("print-mode").value = "text";
  notice("已填入 ZPL 文本示例，尚未发送；设备必须支持 ZPL。");
});
action("raw-print", async () => {
  if (!$("printer").value) throw new Error("请先枚举并选择已安装打印机");
  const data = Devices.encode($("print-data").value, $("print-mode").value);
  if (!window.confirm(`将原始指令发送到“${$("printer").value}”并可能实际出纸，确认提交？`)) return;
  const id = await Devices.PrintRaw($("printer").value, $("print-title").value, data);
  notice(`已提交打印后台，作业 ID：${id}；请在设备确认实际打印结果。`);
});

/** 检查现有权限后启用对应区域，不自动连接、扫描网络、打开串口或打印测试标签。 */
async function initialize() {
  try {
    const info = await Hybrid.connect();
    capabilities = new Set(info.capabilities);
    await Host.Ready();
    for (const [cap, ids] of [
      ["devices.serial", ["ports", "serial-open"]],
      ["devices.network", ["tcp-open"]],
      ["devices.print", ["printers", "raw-print"]],
    ])
      for (const id of ids) $(id).disabled = !capabilities.has(cap);
    if (!capabilities.has("devices.serial") && !capabilities.has("devices.network"))
      for (const id of ["connections", "listen", "close", "send"]) $(id).disabled = true;
    notice(
      "宿主已连接。按需开启 devices.serial / devices.network / devices.print；设备操作需要用户点击。",
    );
  } catch (error) {
    notice(`宿主连接失败：${error.message}`, true);
    for (const id of [
      "ports",
      "serial-open",
      "tcp-open",
      "connections",
      "listen",
      "close",
      "send",
      "printers",
      "raw-print",
    ])
      $(id).disabled = true;
  }
}
// 离页停止键盘和设备回调，并尽力释放本页创建的连接；宿主窗口清理继续兜底。
window.addEventListener("pagehide", () => {
  disposed = true;
  offData?.();
  offScan?.();
  Devices.dispose();
});
void initialize();
