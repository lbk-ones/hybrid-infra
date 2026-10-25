/** 创建字符串输入参数；用户内容会经过 JSON 编码，不会当成 JavaScript 执行。 */
function text(name, value, hint, choices) {
  return { name, kind: "text", value, hint, choices };
}

/** 创建 JSON 输入参数；对象、数组与普通 JSON 值分别校验，避免生成参数类型错误的示例。 */
function json(name, value, hint, kind = "object") {
  return { name, kind, value: JSON.stringify(value, null, 2), hint };
}

/** 创建数值参数，示例生成时检查是否为有限数字。 */
function number(name, value, hint) {
  return { name, kind: "number", value: String(value), hint };
}

/** 创建布尔参数，界面用下拉框明确区分 true 和 false。 */
function boolean(name, value, hint) {
  return { name, kind: "boolean", value: String(value), hint };
}

/** 创建可编辑的函数或表达式模板；输入时仅生成代码，用户点击运行后才编译并作为参数调用。 */
function source(name, value, hint) {
  return { name, kind: "source", value, hint };
}

/** 创建方法示例说明，集中维护参数、准备代码和返回值的处理方式。 */
function example(params = [], details = {}) {
  return { params, ...details };
}

const token = text(
  "token",
  "替换为本窗口取得的文件 token",
  "先用 SelectFile 或 OpenChild 获取，不能使用其他窗口的 token。",
);
const downloadId = text(
  "id",
  "替换为下载任务 ID",
  "从 StartDownload 或 ListDownloads 的结果中取得。",
);
const jobId = text(
  "id",
  "替换为命令任务 ID",
  "从 Shell.start 的结果中取得；run 默认会清理完成记录。",
);
const windowId = text("id", "order-detail", "从 Windows.Open、List 的结果中取得。");
const taskId = text("id", "替换为任务 ID", "从当前窗口 Host.BeginTask 的结果中取得。");
const namespace = text("namespace", "docs-demo", "缓存分组，用来把不同业务的临时数据分开。");
const cacheKey = text("key", "filter", "这一项缓存的名称。");
const global = boolean("global", false, "true 表示跨来源共享，需要 cache.global 权限。");
const cacheValue = json("value", { status: "open" }, "要保存的 JSON 数据。", "json");
const ttl = number("ttlMs", 60000, "缓存有效期，单位毫秒；0 表示本次运行中不过期。");
const storeKey = text("key", "docs.demo.preferences", "持久化数据的键名；默认使用独立演示键。");
const topic = text(
  "topic",
  "docs.demo.orders.changed",
  "发送方和监听方需要使用相同主题；默认使用独立演示主题。",
);
const eventOptions = json(
  "options",
  { scope: "origin" },
  "同来源用 origin；跨来源用 app 并授权 events.global，可加 targetWindow 定向发送。",
);
const commandOptions = json(
  "options",
  { mode: "program", program: "go", args: ["version"], timeoutMs: 5000 },
  "program 必须列在 shellPrograms 白名单中；脚本模式额外需要 shell.script。",
);
const callOptions = json(
  "options",
  { timeoutMs: 30000 },
  "控制等待时长，单位毫秒；AbortSignal 请在业务代码里创建后传入。",
);
const level = number("level", 0, "日志级别：Debug=-4、Info=0、Warning=4、Error=8。");
const listenerCleanup = "// 组件卸载或不再监听时调用：\n// await off();";
const fileNote =
  "请先在本页运行 SelectFile 或 OpenChild，填写返回的 token；文件授权不能跨窗口使用。";

// 为所有有参数的方法提供可填写的示例；无参数方法由 getMethodExample 生成直接调用模板。
const examples = {
  "Devices.CancelRead": example([
    text("id", "替换为连接ID", "只停止当前读取，不关闭硬件或清空队列。"),
  ]),
  "Host.Restart": example([], {
    note: "会真正关闭并重新启动当前应用；未完成任务会阻止重启，未保存草稿不会自动保存。",
  }),
  "Devices.OpenSerial": example([
    json(
      "options",
      {
        port: "COM3",
        baudRate: 9600,
        dataBits: 8,
        parity: "none",
        stopBits: "1",
        dtr: false,
        rts: false,
      },
      "先枚举串口，按设备说明书设置参数。",
    ),
  ]),
  "Devices.OpenTCP": example([
    text("endpoint", "192.168.1.50:9100", "需与 deviceEndpoints 白名单完全一致。"),
  ]),
  "Devices.Read": example([
    text("id", "替换为连接ID", "当前窗口的连接，不能同时与 onData 竞争接收。"),
    number("timeoutMs", 1000, "等待 1–10000 毫秒。"),
  ]),
  "Devices.Write": example([
    text("id", "替换为连接ID", "确认真实设备及指令后发送。"),
    text("data", "REVNTw==", "原始字节 Base64，示例为 DEMO 文本，不是通用设备指令。"),
  ]),
  "Devices.Close": example([
    text("id", "替换为连接ID", "关闭本窗口连接。"),
    boolean("remove", true, "同时移除终态记录。"),
  ]),
  "Devices.encode": example(
    [
      text("text", "1B 40", "文本或精确十六进制字节。"),
      text("format", "hex", "text 为 UTF-8，hex 为十六进制。"),
    ],
    { sync: true },
  ),
  "Devices.writeText": example([
    text("id", "替换为连接ID", "本窗口连接。"),
    text("text", "", "设备协议文本，不提供自动探测命令。"),
  ]),
  "Devices.writeHex": example([
    text("id", "替换为连接ID", "本窗口连接。"),
    text("hex", "", "按真实设备协议填写完整字节。"),
  ]),
  "Devices.onData": example([
    text("id", "替换为连接ID", "先在本页打开连接。"),
    source(
      "callback",
      "async (value) => console.log(value)",
      "函数保留在本页，bytes/text/lines 返回类型不同。",
    ),
    source(
      "options",
      "({mode:'lines',encoding:'utf-8',onError:error=>console.error(error.message)})",
      "跨块解码并按行回调，停止清理按钮可解除。",
    ),
  ]),
  "Devices.onScan": example(
    [
      text("target", "main", "建议改为独立扫码输入区域选择器。"),
      source("callback", "async (code) => console.log(code)", "收到完整条码后回调。"),
      json(
        "options",
        { gapMs: 50, minLength: 3, maxLength: 256, terminator: "Enter", prefix: "" },
        "快速按键仍可能是普通打字，不是 USB 身份证明。",
      ),
    ],
    { sync: true },
  ),
  "Devices.parseWeight": example(
    [
      text("text", "ST,GS,+0012.340kg", "常见文本秤帧，私有协议需要自行解析。"),
      json("options", { defaultUnit: "" }, "无单位时不自动猜测，可显式设置 kg/g/lb/t。"),
    ],
    { sync: true },
  ),
  "Devices.PrintRaw": example([
    text("printer", "替换为已安装打印机名称", "先枚举打印机，会真实提交打印后台。"),
    text("title", "业务标签", "任务名称。"),
    text("data", "", "原始设备指令 Base64，避免未确认就向真实硬件发送示例。"),
  ]),
  "Devices.dispose": example([], {
    sync: true,
    note: "关闭本页全部设备并停止回调，之后需要刷新页面重新连接。",
  }),
  "Documents.OpenPrintPreview": example([
    json(
      "report",
      {
        title: "订单报表",
        headers: ["订单号", "金额"],
        rows: [["001", "128.00"]],
        paper: "A4",
        orientation: "portrait",
      },
      "在系统默认浏览器打开本地预览，由用户点击打印。",
    ),
  ]),
  "Documents.Parse": example([
    text("format", "csv", "csv、json、xlsx 或 zip。"),
    text("data", "aWQsbmFtZQoxLERlbW8K", "Base64 文件内容。"),
  ]),
  "Documents.ImportFile": example([token, text("format", "csv", "实际文件格式。")]),
  "Documents.ExportFile": example([
    token,
    text("format", "csv", "目标格式。"),
    json(
      "table",
      { headers: ["id", "name"], rows: [["001", "示例"]] },
      "单元格为文本；使用保存凭证。",
    ),
  ]),
  "Documents.readFile": example([
    source(
      "file",
      "new File([`id,name\n001,Demo\n`], 'demo.csv', {type:'text/csv'})",
      "用户选取或拖入的标准 File。",
    ),
    text("format", "csv", "可省略时按扩展名推断。"),
  ]),
  "Documents.onDrop": example([
    text("target", "main", "现有拖放容器选择器。"),
    source(
      "handler",
      "async (files) => { console.log(files.map(file => ({name:file.name,size:file.size}))); }",
      "只接收 File[]，不自动读取或上传。",
    ),
    source("options", "({ onError: error => console.error(error.message) })", "回调失败处理。"),
  ]),
  "Documents.preview": example(
    [
      text("target", "main", "预览附到已有容器，停止/清理可移除。"),
      json(
        "report",
        { title: "订单报表", headers: ["订单号", "金额"], rows: [["001", "128.00"]] },
        "报表文字不作为 HTML 执行。",
      ),
    ],
    { sync: true },
  ),
  "Documents.printReport": example([
    json(
      "report",
      {
        title: "订单报表",
        headers: ["订单号", "金额"],
        rows: [["001", "128.00"]],
        paper: "A4",
        orientation: "portrait",
      },
      "打开预览后由用户选择打印或另存 PDF。",
    ),
  ]),
  "Documents.dispose": example([], { sync: true }),
  "Tasks.Create": example([
    text("title", "手动任务", "创建后必须 Poll 领取并持续续租，推荐直接用 run。"),
  ]),
  "Tasks.Poll": example([
    text("id", "任务ID", "从本窗口 Tasks.Create 结果复制；其他页面不能代领。"),
  ]),
  "Tasks.Report": example([
    text("id", "任务ID", "填写本窗口已领取的业务任务。"),
    number("progress", 50, "-1 未知，0–100 表示百分比。"),
    text("message", "已处理一半", "简短进度说明。"),
  ]),
  "Tasks.Finish": example([
    text("id", "任务ID", "仅在函数实际结束后调用。"),
    text("state", "completed", "completed、failed 或 cancelled。"),
    text("message", "处理完成", "结果说明，不要放秘密值。"),
  ]),
  "Tasks.Retry": example([text("id", "任务ID", "本窗口终态原生任务，内存参数和权限必须有效。")]),
  "Tasks.canRetry": example([text("id", "任务ID", "仅判断当前页面是否保留业务函数。")], {
    sync: true,
  }),
  "Tasks.dispose": example([], {
    sync: true,
    note: "清理整个页面的业务客户端，后续 run 需要刷新页面。",
  }),
  "Tasks.run": example([
    text("title", "进度演示", "任务说明。"),
    source(
      "operation",
      "async ({ signal, report }) => { if (signal.aborted) throw new Error('已取消'); await report(100, '演示完成'); return {done:true}; }",
      "函数在本页执行，不传到 Go。",
    ),
    json("options", {}, "可传 signal 和 onProgress。"),
  ]),
  "Tasks.Cancel": example([text("id", "任务ID", "从 Tasks.List 复制。")]),
  "Tasks.retry": example([text("id", "任务ID", "原生参数或本页业务回调仍有效时才能重试。")]),
  "Tasks.Remove": example([text("id", "任务ID", "仅移除终态历史。")]),
  "Tasks.EnqueueShell": example([text("title", "命令演示", "任务说明。"), commandOptions]),
  "Tasks.EnqueueDownload": example([
    text("title", "下载演示", "任务说明。"),
    text("url", "https://example.com/file.zip", "文件下载地址。"),
    token,
  ]),
  "Database.Get": example([
    text("collection", "docs-demo", "集合名称。"),
    text("id", "order-001", "记录 ID。"),
  ]),
  "Database.Query": example([
    json(
      "query",
      { collection: "docs-demo", where: {}, offset: 0, limit: 20, sortBy: "id", descending: false },
      "按 nextOffset 继续分页。",
    ),
  ]),
  "Database.Put": example([
    text("collection", "docs-demo", "演示集合。"),
    text("id", "order-001", "记录 ID。"),
    json("data", { status: "draft", amount: 128 }, "JSON 对象。"),
    number("revision", 0, "0 仅新建，-1 覆盖，正数校验版本。"),
  ]),
  "Database.Delete": example([
    text("collection", "docs-demo", "集合名称。"),
    text("id", "order-001", "记录 ID。"),
    number("revision", 1, "最新版本，避免误删更新。"),
  ]),
  "Database.Batch": example([
    json(
      "changes",
      [
        {
          operation: "put",
          collection: "docs-demo",
          id: "batch-001",
          data: { status: "draft" },
          revision: 0,
        },
      ],
      "最多100项，有一项失败全部回滚。",
      "array",
    ),
  ]),
  "Hybrid.connect": example([
    json(
      "options",
      { requiredCapabilities: ["host", "ui"], timeoutMs: 5000 },
      "只检查当前页面已有权限，不会新增授权。",
    ),
  ]),
  "Hybrid.isDesktopShell": example([], { sync: true }),
  "Hybrid.detectEnvironment": example([
    json("options", { timeoutMs: 1500 }, "向宿主确认运行环境时的等待上限。"),
  ]),
  "Hybrid.call": example(
    [
      source(
        "operation",
        "() => Desktop.GetSettings()",
        "替换为你要调用的函数；函数应返回 Promise。",
      ),
      callOptions,
    ],
    { imports: ["Desktop"] },
  ),
  normalizeError: example(
    [
      json(
        "error",
        { code: "E_PERMISSION_DENIED", message: "当前页面未获授权" },
        "可先用示例错误查看写法，业务中传入 catch 捕获的错误。",
      ),
    ],
    { sync: true },
  ),
  "Desktop.SaveSettings": example(
    [
      {
        ...json(
          "config",
          { splashEnabled: false },
          "这里填写要修改的配置字段，生成代码会先读 saved，再合并为完整配置。",
        ),
        prefix: "{ ...saved, ...",
        suffix: " }",
      },
    ],
    {
      before: "const { saved } = await Desktop.GetSettings();",
      note: "保存配置后，启动参数等改动需重启生效。",
    },
  ),
  "Desktop.SavePreferences": example([
    json(
      "preferences",
      { theme: "system", locale: "zh-CN", notifications: true },
      "主题、语言和系统通知开关。",
    ),
  ]),
  "UI.Notify": example([
    text("text", "保存成功", "通知里显示的纯文本。"),
    json(
      "options",
      { place: "topRight", durationSeconds: 4, draggable: true },
      "位置 place；时长 durationSeconds 单位秒；draggable 控制拖动。",
    ),
  ]),
  "UI.Alert": example([
    json(
      "options",
      { title: "提示", body: "文件已经保存。", destroy: 3, place: "center", draggable: true },
      "destroy 为自动关闭秒数，0 表示等待用户关闭。",
    ),
  ]),
  "UI.Confirm": example(
    [
      json(
        "options",
        {
          title: "继续处理？",
          body: "确认后开始下一步。",
          okText: "继续",
          cancelText: "返回",
          timeoutSeconds: 60,
          draggable: true,
        },
        "确认与取消文案、最长等待秒数以及拖动开关。",
      ),
    ],
    {
      after:
        "if (result === '1') {\n  console.log('用户已确认');\n} else {\n  console.log('用户取消、关闭或超时');\n}",
    },
  ),
  "UI.Show": example([
    json(
      "options",
      {
        body: "<section><h2>任务摘要</h2><p>全部完成</p></section>",
        width: 420,
        height: 240,
        place: "center",
        draggable: true,
      },
      "body 只放可信 HTML，不要拼接未处理的用户输入。",
    ),
  ]),
  "UI.OpenWindowByUrl": example([
    json(
      "options",
      { url: "/", title: "工作台", width: 1000, height: 720 },
      "打开允许导航的页面；字段名使用小写 url。",
    ),
  ]),
  "Desktop.SelectFile": example([
    text("mode", "open", "open 选文件；save 选保存位置；directory 选目录。", [
      "open",
      "save",
      "directory",
    ]),
    text("name", "", "保存时可填建议文件名，例如 report.txt；打开时可留空。"),
  ]),
  "Desktop.ReadFile": example([token], { note: fileNote }),
  "Desktop.WriteFile": example(
    [
      token,
      text(
        "base64",
        "5L2g5aW977yM5qGM6Z2i",
        "文件字节的 Base64，示例为一段 UTF-8 中文；需要可写 token。",
      ),
    ],
    { note: fileNote },
  ),
  "Desktop.ReleaseFile": example([token], { note: fileNote }),
  "Desktop.RevealFile": example([token], { note: fileNote }),
  "Desktop.ListDirectory": example([token], {
    note: "先用 SelectFile('directory', '') 取得目录 token，再在同一业务窗口中调用。",
  }),
  "Desktop.OpenChild": example(
    [token, text("name", "report.txt", "目录中直接子项的名称，不能包含路径分隔符。")],
    { note: "token 必须指向目录；返回子项的新 token，用完后也要 ReleaseFile。" },
  ),
  "Desktop.StartDownload": example(
    [text("url", "https://example.com/report.csv", "要下载的 HTTP(S) 文件地址。"), token],
    { note: "先用 SelectFile('save', 'report.csv') 获取可写 token，再在同一业务窗口调用。" },
  ),
  "Desktop.CancelDownload": example([downloadId]),
  "Desktop.RetryDownload": example([downloadId]),
  "Desktop.OpenDownload": example([
    downloadId,
    boolean("reveal", true, "true 在文件管理器中定位；false 直接打开已完成的文件。"),
  ]),
  "Desktop.RemoveDownload": example([downloadId]),
  "Desktop.ClipboardText": example([
    text("operation", "write", "read 读取；write 写入；clear 清空。", ["read", "write", "clear"]),
    text("text", "已复制到剪贴板", "写入时填写内容；读取或清空时传空字符串。"),
  ]),
  "Desktop.ClipboardImage": example([
    text("base64", "", "读取时留空；写入时填写 PNG 图片的 Base64。"),
    boolean("write", false, "false 读图片；true 写图片。"),
  ]),
  "Desktop.Screenshot": example(
    [
      json(
        "options",
        { mode: "region", x: 0, y: 0, width: 640, height: 360, output: "base64" },
        "坐标用物理像素；手动框选改为 mode: select，可加 displayId；output 支持 base64、clipboard、both。",
      ),
    ],
    { note: "手动框选最长可等待两分钟；业务调用可用 Hybrid.call 把超时设为 125000 毫秒。" },
  ),
  "Desktop.Notify": example([
    text("title", "导出完成", "系统通知的标题。"),
    text("body", "点击返回工作台", "系统通知的正文。"),
    text("route", "/#workspace", "点击通知后进入的站内路由，以 / 开头。"),
  ]),
  "Desktop.SetShortcut": example([
    text("key", "Ctrl+Shift+Alt+H", "支持的组合键格式：Ctrl+Shift+[Alt+] 字母或数字。"),
    text("route", "/#workspace", "触发后进入的站内路由。"),
    boolean("global", true, "true 时切到其他软件也能响应。"),
    boolean("enable", true, "true 注册；false 注销，结束业务时按需注销。"),
  ]),
  "Desktop.ConfigureProtocol": example([
    boolean("enable", true, "true 注册 hybrid-infra:// 协议；false 移除当前安装的注册。"),
  ]),
  "Desktop.Credential": example(
    [
      text("key", "demo-token", "凭据名称；只填示例，避免在共享屏幕时输入真实秘密。"),
      text("value", "", "get 和 delete 留空；set 时填写要保存的值。"),
      text("operation", "get", "get 读取；set 保存；delete 删除。", ["get", "set", "delete"]),
    ],
    { after: "// result 为凭据操作结果；秘密值交给业务使用，不打印到日志。" },
  ),
  "Shell.run": example([
    commandOptions,
    source(
      "callbacks",
      "{\n  onStdout: (chunk) => console.log(chunk),\n  onStderr: (chunk) => console.error(chunk),\n}",
      "回调函数留在 JS 中；可修改输出处理，也可传 {}。",
    ),
  ]),
  "Shell.start": example([commandOptions]),
  "Shell.read": example([
    jobId,
    number("stdoutOffset", 0, "已读取标准输出的字节偏移量，首次为 0。"),
    number("stderrOffset", 0, "已读取错误输出的字节偏移量，首次为 0。"),
  ]),
  "Shell.cancel": example([jobId]),
  "Shell.remove": example([jobId]),
  "Cache.Get": example([namespace, cacheKey, global]),
  "Cache.Set": example([namespace, cacheKey, cacheValue, ttl, global]),
  "Cache.CompareAndSet": example([
    namespace,
    cacheKey,
    cacheValue,
    ttl,
    global,
    number("revision", 0, "使用 Get 返回的 revision；0 表示只有键不存在才允许写入。"),
  ]),
  "Cache.Delete": example([namespace, cacheKey, global]),
  "Cache.Clear": example([namespace, global]),
  "Events.on": example(
    [
      topic,
      source(
        "callback",
        "(data) => {\n  console.log('刷新订单列表', data.id);\n}",
        "消息到来时在当前 JS 页面执行，参数是发送方的 JSON 数据。",
      ),
      eventOptions,
    ],
    { result: "off", after: listenerCleanup },
  ),
  "Events.once": example(
    [
      topic,
      source(
        "callback",
        "(data) => {\n  console.log('只处理这一次消息', data);\n}",
        "回调执行一次后自动取消；也可提前调用返回的 off。",
      ),
      eventOptions,
    ],
    { result: "off", after: listenerCleanup },
  ),
  "Events.emit": example([
    topic,
    json("data", { id: 42 }, "发送给监听方的 JSON 数据。", "json"),
    eventOptions,
  ]),
  "Events.onError": example(
    [
      source(
        "callback",
        "(error) => {\n  console.error(error.code, error.message);\n}",
        "接收监听后台错误；可改为业务错误提示。",
      ),
    ],
    { sync: true, result: "off", after: "// 不再关心后台错误时调用：\n// off();" },
  ),
  "Events.handle": example(
    [
      text("topic", "docs.demo.math.sum", "与请求方使用同一个主题。"),
      source(
        "callback",
        "async ({ a, b }) => {\n  return { total: a + b };\n}",
        "返回 JSON 或 Promise<JSON>；返回值会交给 request 的调用方。",
      ),
      eventOptions,
    ],
    { result: "off", after: listenerCleanup },
  ),
  "Events.request": example([
    text("topic", "docs.demo.math.sum", "先在目标窗口用 Events.handle 注册处理器。"),
    json("data", { a: 2, b: 3 }, "传给请求处理器的 JSON 数据。", "json"),
    json(
      "options",
      { scope: "origin", timeoutMs: 5000 },
      "多个处理器时指定 targetWindow；timeoutMs 最长 30000。",
    ),
  ]),
  "Store.Get": example([storeKey]),
  "Store.Set": example([
    storeKey,
    json(
      "value",
      { density: "comfortable" },
      "要持久保存的 JSON 数据，应用下次启动还能读取。",
      "json",
    ),
  ]),
  "Store.Delete": example([storeKey]),
  "Windows.Open": example([
    json(
      "options",
      {
        name: "order-detail",
        url: "/#workspace",
        title: "订单详情",
        width: 1000,
        height: 720,
        place: "center",
      },
      "同 name 复用窗口；url 必须符合导航规则。",
    ),
  ]),
  "Windows.Get": example([windowId]),
  "Windows.Focus": example([windowId]),
  "Windows.Close": example([windowId]),
  "Hybrid.withTask": example([
    text("label", "同步业务数据", "显示给用户看的未完成任务名称。"),
    source(
      "operation",
      "async () => {\n  // 替换为真实的异步业务工作。\n  return { completed: true };\n}",
      "函数完成或失败后自动解除任务登记，业务取消需自行处理。",
    ),
    callOptions,
  ]),
  "Host.BeginTask": example([
    text("label", "同步业务数据", "手动登记后，要在 finally 中用 EndTask 释放。"),
  ]),
  "Host.EndTask": example([taskId]),
  "Host.Retry": example([boolean("home", false, "false 重试当前目标；true 回到内置首页。")]),
  "Hybrid.openDocs": example([callOptions]),
  "Log.Log": example([
    level,
    text("message", "开始同步业务数据", "要记录的消息，不包含密码或令牌。"),
    json(
      "...args",
      ["module", "workspace", "count", 3],
      "用 JSON 数组填写额外日志字段，生成代码会展开传入；建议键和值成对。",
      "array",
    ),
  ]),
  "Log.SetLogLevel": example([level]),
  "Maintenance.ExportDiagnostics": example([token], {
    note: "先用 SelectFile('save', 'diagnostics.json') 取得可写 token，在同一业务窗口导出。",
  }),
  "Maintenance.StageRestore": example([
    text("id", "替换为备份 ID", "从 CreateBackup 或 ListBackups 取得；下次启动恢复数据。"),
  ]),
};

/** 获取某个方法的编辑模板；只有真正无参数的方法才允许省略显式参数定义。 */
export function getMethodExample(signature) {
  const name = signature.split("(")[0];
  if (examples[name]) return examples[name];
  if (signature.endsWith("()")) return example();
  throw new Error(`${name} 缺少参数示例`);
}

/** 把输入转换为代码文本；不执行函数模板，JSON 与普通文本也不会插入页面 HTML。 */
function argumentCode(field, raw) {
  if (field.kind === "text") return JSON.stringify(raw);
  if (field.kind === "source") {
    if (!raw.trim()) throw new Error(`${field.name} 请填写函数或表达式`);
    return raw.trim();
  }
  if (field.kind === "boolean") {
    if (raw !== "true" && raw !== "false") throw new Error(`${field.name} 请选择 true 或 false`);
    return raw;
  }
  if (field.kind === "number") {
    if (!raw.trim() || !Number.isFinite(Number(raw)))
      throw new Error(`${field.name} 请填写有效数字`);
    return String(Number(raw));
  }
  let value;
  try {
    value = JSON.parse(raw);
  } catch {
    throw new Error(`${field.name} 不是有效的 JSON，请检查双引号、逗号和括号`);
  }
  if (
    field.kind === "object" &&
    (value === null || Array.isArray(value) || typeof value !== "object")
  )
    throw new Error(`${field.name} 需要 JSON 对象，例如 {"timeoutMs": 30000}`);
  if (field.kind === "array" && !Array.isArray(value))
    throw new Error(`${field.name} 需要 JSON 数组`);
  return `${field.prefix || ""}${JSON.stringify(value, null, 2)}${field.suffix || ""}`;
}

/** 根据当前参数生成完整导入和调用示例；只返回字符串，填写参数不会触发任何桌面操作。 */
export function buildMethodExample(signature, values) {
  const name = signature.split("(")[0];
  const spec = getMethodExample(signature);
  const args = spec.params.map((field, index) => {
    const code = argumentCode(field, values?.[index] ?? field.value);
    return field.name.startsWith("...") ? `...${code}` : code;
  });
  const imports = [...new Set([name.split(".")[0], ...(spec.imports || [])])];
  const call = args.length
    ? `${name}(\n${args
        .map((arg) =>
          arg
            .split("\n")
            .map((line) => `  ${line}`)
            .join("\n"),
        )
        .join(",\n")},\n)`
    : `${name}()`;
  return [
    `import { ${imports.join(", ")} } from '/native/client.js';`,
    "",
    ...(spec.note ? [`// ${spec.note}`] : []),
    ...(spec.before ? [spec.before, ""] : []),
    `const ${spec.result || "result"} = ${spec.sync ? "" : "await "}${call};`,
    spec.after ?? "console.log(result);",
  ].join("\n");
}

/** 将表单转换为真实调用参数；只在点击运行时编译函数模板，文本与 JSON 始终按数据处理，末尾数组按可变参数展开。 */
export function readMethodArguments(signature, values, compileSource) {
  const spec = getMethodExample(signature);
  return spec.params.flatMap((field, index) => {
    const raw = values?.[index] ?? field.value;
    argumentCode(field, raw);
    let value;
    if (field.kind === "source") value = compileSource(raw);
    else if (field.kind === "text") value = raw;
    else if (field.kind === "number") value = Number(raw);
    else if (field.kind === "boolean") value = raw === "true";
    else value = JSON.parse(raw);
    return field.name.startsWith("...") ? value : [value];
  });
}
