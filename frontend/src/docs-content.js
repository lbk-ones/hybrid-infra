export const categories = [
  { id: "start", title: "接入与配置", caption: "CONNECT", icon: "01" },
  { id: "ui", title: "界面 UI", caption: "INTERFACE", icon: "02" },
  { id: "desktop", title: "桌面能力", caption: "DESKTOP", icon: "03" },
  { id: "bridge", title: "命令与通信", caption: "BRIDGE", icon: "04" },
  { id: "host", title: "宿主与窗口", caption: "HOST", icon: "05" },
  { id: "maintenance", title: "维护与发布", caption: "MAINTAIN", icon: "06" },
];

// 与 Go 宿主 Capabilities 保持一致；默认基础能力与需显式开启的能力分别标识，配置示例不自动写入用户设置。
export const permissionCatalog = [
  {
    name: "host",
    description: "连接外壳、查询环境与加载状态、打开开发文档。",
    defaultGranted: true,
  },
  { name: "windows", description: "打开、查询、聚焦和关闭受管理窗口。", defaultGranted: true },
  { name: "ui", description: "显示轻量通知、消息框和确认框。", defaultGranted: true },
  { name: "ui.html", description: "用可信 HTML 展示自定义小窗口。", defaultGranted: true },
  { name: "store", description: "读写整个应用的持久化业务数据。", defaultGranted: true },
  { name: "log", description: "写入宿主日志、查询和调整日志级别。", defaultGranted: true },
  {
    name: "lifecycle",
    description: "登记未完成任务、解除任务保护和请求退出。",
    defaultGranted: true,
  },
  {
    name: "events",
    description: "在同来源窗口间发消息、监听回调和请求返回。",
    defaultGranted: true,
  },
  { name: "files", description: "选择文件或目录、读取写入、浏览目录和定位文件。" },
  { name: "downloads", description: "创建下载任务、查看进度、取消和重试下载。" },
  { name: "clipboard", description: "读取、写入文本和图片剪贴板。" },
  { name: "notifications", description: "发送进入操作系统通知中心的通知。" },
  { name: "shortcuts", description: "注册和取消窗口或全局快捷键。" },
  { name: "settings", description: "读写应用配置和界面偏好、重新加载图标。" },
  { name: "credentials", description: "在系统凭据库中保存、读取或删除秘密值。" },
  { name: "protocol", description: "注册或移除 hybrid-infra:// 专用链接。" },
  { name: "shell", description: "运行白名单程序、读取输出和取消命令任务。" },
  { name: "shell.script", description: "额外允许 Shell 脚本模式，需同时具有 shell。" },
  { name: "cache", description: "读写同来源窗口共享的内存缓存。" },
  { name: "cache.global", description: "额外允许跨来源共享缓存，需同时具有 cache。" },
  { name: "events.global", description: "额外允许跨来源发送事件和请求，需同时具有 events。" },
  { name: "screenshot", description: "按坐标截图、手动框选和读取显示器信息。" },
  { name: "diagnostics", description: "查看并导出版本、环境和运行状态诊断信息。" },
  { name: "updates", description: "检查、下载、安装更新以及回滚旧版本。" },
  { name: "backups", description: "创建、查询业务数据备份和安排恢复。" },
];

export const permissionSnippets = {
  all: JSON.stringify(
    permissionCatalog.map((item) => item.name),
    null,
    2,
  ),
  local: JSON.stringify(
    {
      desktopCapabilities: permissionCatalog
        .filter((item) => !item.defaultGranted)
        .map((item) => item.name),
    },
    null,
    2,
  ),
};

// 按方法名维护通俗说明；同一方法出现在多个功能页时共用介绍，避免说法不一致。
export const methodDescriptions = {
  "Hybrid.connect": "先和桌面外壳打个招呼，确认版本和当前页面能用哪些能力；业务页面初始化时调用。",
  "Host.Ready": "告诉外壳：这个页面已经准备好了，可以结束加载状态了；页面初始化完成后调用。",
  "Hybrid.isDesktopShell":
    "快速看一眼当前页面是不是可能在桌面外壳里，适合决定先显示哪套界面；需要准确信息时再用 detectEnvironment。",
  "Hybrid.detectEnvironment": "实际向外壳确认运行环境，分清普通浏览器、服务端渲染和原生桌面窗口。",
  "Host.GetEnvironment":
    "直接向宿主询问当前窗口的原生环境信息；通常用更方便的 Hybrid.detectEnvironment 即可。",
  "Hybrid.call": "给一次桌面调用加上超时、取消和统一错误处理，避免页面一直傻等结果。",
  normalizeError: "把各种调用错误整理成统一的错误码和提示文字，方便页面显示失败原因。",
  "Desktop.GetSettings":
    "查看现在正在用的配置、磁盘上保存的配置和界面偏好，判断哪些改动还需要重启。",
  "Desktop.SaveSettings":
    "保存一份完整的应用配置；先读取再修改，窗口和权限等设置通常要重启后才生效。",
  "Desktop.SavePreferences": "记住用户选择的主题、语言和通知开关，下次打开程序继续沿用。",
  "Desktop.ResetPreferences": "把主题、语言和通知等界面偏好恢复为默认值。",
  "UI.Notify": "弹出一条会自动消失的小提示，例如“保存成功”；可指定位置、停留时间和是否允许拖动。",
  "UI.Alert": "弹出一个需要用户点确定的消息框，适合提醒用户认真看完一段说明。",
  "UI.Confirm": "弹出确认框并等用户做选择，适合在删除、覆盖等操作前问一句“确定吗”。",
  "UI.Show": "把你传入的 HTML 内容放进一个独立小窗口，适合做自定义提示或轻量工具面板。",
  "UI.OpenWindowByUrl":
    "通过 UI 接口打开指定地址的窗口，适合把已有页面单独展示出来；地址仍要符合宿主的导航规则。",
  "Desktop.ReloadIcons": "按已保存的图标配置重新加载窗口和托盘图标，换图标时不用重启整个程序。",
  "Desktop.SelectFile":
    "弹出系统选择框，让用户选文件、选目录或决定保存位置；返回的 token 是后续操作这份文件的凭证。",
  "Desktop.ReadFile": "用选文件时拿到的 token 读取小文件，返回 Base64 内容，适合导入文本或小图片。",
  "Desktop.WriteFile": "把 Base64 内容写到用户授权的保存位置，适合导出小文件；需要可写的 token。",
  "Desktop.ReleaseFile": "用完文件后交还这张 token，清理当前窗口不再需要的文件授权。",
  "Desktop.RevealFile": "打开系统文件管理器并定位这份文件或目录，方便用户找到它。",
  "Desktop.ListDirectory": "看看用户选中的目录里有哪些直接子文件和子目录，适合制作文件列表。",
  "Desktop.OpenChild":
    "根据目录 token 和子项名称取得子项 token，之后再用它读文件或继续浏览目录；不会直接打开文件内容。",
  "Desktop.StartDownload":
    "把一个网址的文件下载到用户选好的位置，立即拿到任务信息，后续可以查进度。",
  "Desktop.ListDownloads": "查看下载任务的进度和结果，用来刷新下载列表或进度条。",
  "Desktop.CancelDownload": "停止指定的下载任务；之后需要重新下载时可以调用 RetryDownload。",
  "Desktop.RetryDownload": "把失败或已取消的下载从头再下一遍，不会接着上次的进度续传。",
  "Desktop.OpenDownload": "下载完成后打开文件，或者在文件管理器里定位它，由 reveal 参数决定。",
  "Desktop.RemoveDownload": "从下载列表里清掉一条记录，不会把已经下载的文件一起删掉。",
  "Desktop.ClipboardText": "读取、写入或清空系统剪贴板里的文字，适合做“复制内容”和“粘贴文字”按钮。",
  "Desktop.ClipboardImage": "从剪贴板读取图片，或把 Base64 编码的 PNG 放进去，供其他软件粘贴。",
  "Desktop.CaptureDisplays": "查看有哪些显示器以及各自的物理坐标，截图前用它确定要截哪块屏幕。",
  "Desktop.Screenshot":
    "截取指定坐标区域，或让用户手动框选；结果可返回 Base64，也可直接放入剪贴板。",
  "Desktop.Notify": "发一条操作系统通知，适合程序在后台时提醒用户；点击通知可回到指定的应用页面。",
  "Desktop.SetShortcut":
    "注册或取消一个快捷键，让用户按组合键就能回到指定页面；设为全局时，切到其他软件也能触发。",
  "Desktop.ConfigureProtocol":
    "注册或移除本程序的专用链接，让系统能用 hybrid-infra:// 链接唤醒应用并跳到指定页面。",
  "Desktop.Credential":
    "在系统凭据库里保存、读取或删除令牌等秘密值，避免把它们塞进普通 JSON 配置。",
  "Shell.run":
    "执行一项后台命令，边跑边通过回调接收输出，最后等到结果；一般业务优先用这个省事的接口。",
  "Shell.start": "只启动命令并拿到任务 ID，不等它跑完；适合你自己管理进度和结果。",
  "Shell.read": "用任务 ID 和已读偏移量取回新增输出与当前状态，适合自己轮询刷新命令日志。",
  "Shell.cancel": "请求停止一项还在运行的命令，用来实现任务的“取消”按钮。",
  "Shell.remove": "清理已经结束的命令记录和保留输出；运行中的任务要先取消或等它结束。",
  "Cache.Get": "从指定缓存分组里取一个值，同时查看是否存在、什么时候过期以及当前版本号。",
  "Cache.Set": "临时记住一份 JSON 数据，可设置多久后过期；同来源的窗口可以共享，退出程序后会清空。",
  "Cache.CompareAndSet": "只有缓存版本还和你上次读取的一样才更新，防止两个窗口同时修改时互相覆盖。",
  "Cache.Delete": "删除缓存分组里的某一个键，适合让一项临时数据立即失效。",
  "Cache.Clear": "清空指定分组的缓存，不会把其他分组一起清掉。",
  "Events.on": "订阅一个业务事件，消息来了就运行你传入的回调；返回取消函数，组件卸载时记得调用。",
  "Events.once": "只接收下一次匹配事件，回调执行一次后自动取消监听。",
  "Events.emit":
    "向匹配的窗口广播一条消息，例如“订单已更新”；只负责发送，不等监听回调执行，也不收集返回值。",
  "Events.close": "关闭当前页面的事件会话，清掉本页全部监听；之后重新订阅会创建新会话。",
  "Events.onError":
    "接收事件监听过程中的后台错误，方便统一提示或记录日志；返回函数可取消这项错误监听。",
  "Events.handle":
    "给一个请求主题安排处理函数，收到请求后可以异步处理并返回 JSON；返回取消函数可撤下这个处理器。",
  "Events.request":
    "向另一个处理器发请求并等它回话，适合跨窗口取数据；需要拿到返回值时用它，而不是 emit。",
  "Store.Get": "按键名读取已经保存的业务数据，例如上次选中的工作区。",
  "Store.Set": "按键名保存一份 JSON 业务数据，自动写入用户数据目录，下次启动还能读到。",
  "Store.Delete": "移除某一个已保存的业务数据项，不影响其他键。",
  "Store.Clear": "清空整个应用的 Store 数据，调用前确认业务确实需要全部清除。",
  "Store.Save": "主动把当前 Store 写入磁盘；平时已有自动保存，一般不用每次 Set 后再调用。",
  "Store.Load": "重新从磁盘载入 Store，替换当前内存中的数据；普通页面初始化通常不需要手动调用。",
  "Windows.Open": "打开一个具名窗口；同名窗口已经存在时会直接唤醒它，适合订单详情等独立业务页。",
  "Windows.List": "查看当前受宿主管理的窗口列表，适合做窗口切换菜单。",
  "Windows.Get": "按窗口 ID 查看这一个窗口的地址、加载状态等信息。",
  "Windows.Focus": "把指定窗口显示到前台并聚焦，让用户继续在那个窗口操作。",
  "Windows.Close": "请求关闭指定窗口；如果还有受保护的任务没完成，会拒绝直接关闭。",
  "Hybrid.withTask": "把一段异步业务包成受保护任务，执行期间防止误退出，结束或报错后自动解除登记。",
  "Host.BeginTask":
    "手动登记“我还有事情没做完”，返回任务 ID；适合无法直接用 withTask 包起来的工作。",
  "Host.EndTask": "用任务 ID 结束本窗口的任务登记，告诉外壳这项工作已经不需要退出保护了。",
  "Host.GetPendingTasks": "查看还有哪些受保护任务没结束，用来展示当前为什么不能退出。",
  "Host.Quit": "请求退出整个应用；外壳会先检查未完成任务，避免工作做到一半被关掉。",
  "Host.GetLoadState": "查看当前页面加载到哪一步、是否失败，适合做加载状态和错误提示。",
  "Host.Retry": "让当前窗口重新加载目标页面；home 为 true 时回内置首页，false 时重试当前目标。",
  "Hybrid.openDocs": "打开这份内置开发文档；已经开着时直接切到原来的文档窗口。",
  "Host.OpenDeveloperDocs":
    "直接通过宿主服务打开同一份开发文档；业务代码通常调用更方便的 Hybrid.openDocs 即可。",
  "Log.Log": "按指定级别写一条宿主日志，适合记录业务过程和排查问题；不要写入密码或令牌。",
  "Log.LogLevel": "查看当前日志级别，了解哪些级别的消息会被记录。",
  "Log.SetLogLevel": "调整当前运行期间的日志级别，排查问题时可临时增加输出。",
  "Maintenance.GetDiagnostics": "收集版本、环境和运行状态等诊断信息，适合在问题反馈界面展示。",
  "Maintenance.ExportDiagnostics": "把诊断信息导出到用户选好的文件位置，方便用户提交问题时附上。",
  "Maintenance.CreateBackup": "给应用业务数据做一份备份，返回备份 ID，方便以后恢复。",
  "Maintenance.ListBackups": "列出已有备份的时间、版本和大小，让用户挑选要恢复的那一份。",
  "Maintenance.StageRestore": "把选中的备份安排到下次启动时恢复，不会立刻替换正在使用的数据。",
  "Maintenance.GetUpdateState":
    "查看更新进行到了哪一步，包括检查结果、下载进度和是否已准备好安装。",
  "Maintenance.CheckUpdates": "按配置中的发布地址检查有没有可用新版本，并验证更新清单。",
  "Maintenance.DownloadUpdate":
    "下载已经检查到的新版程序并校验，完成后等待安装，不会自动替用户重启。",
  "Maintenance.CancelUpdate": "取消当前更新下载，适合做更新页面的“暂不下载”按钮。",
  "Maintenance.InstallUpdate": "安装已经准备好的更新并重启应用；还有受保护任务时不能直接安装。",
  "Maintenance.RollbackUpdate":
    "回到保留的旧版程序，并恢复更新前的数据快照；排查新版问题时要确认用户接受数据回退。",
};

/** 统一文档条目的结构；内容作为纯数据供页面和一致性测试共用，运行按钮另行调用真实 SDK。 */
function doc(category, id, title, summary, details) {
  return {
    category,
    id,
    title,
    summary,
    params: [],
    notes: [],
    returns: "Promise；失败时抛出宿主错误。",
    language: "javascript",
    ...details,
  };
}

export const features = [
  doc(
    "start",
    "quickstart",
    "快速接入",
    "把完整 SDK 放入业务前端的静态目录，用同一套接口连接桌面宿主。",
    {
      api: ["Hybrid.connect(options)", "Host.Ready()"],
      caps: ["host"],
      params: [
        ["SDK 路径", "静态目录", "/native/client.js", "复制整个 hybrid/，保留相对目录结构。"],
        ["requiredCapabilities", "string[]", "[]", "检查已有授权，不会向宿主申请新权限。"],
        ["timeoutMs", "number", "5000", "握手超时，单位毫秒。"],
      ],
      code: `import { Hybrid, Host, UI } from '/native/client.js';\n\nconst info = await Hybrid.connect({\n  requiredCapabilities: ['host', 'ui'],\n});\nawait Host.Ready();\nawait UI.Notify('桌面连接已就绪');\nconsole.log(info.version, info.capabilities);`,
      returns: "握手返回版本、协议、平台和当前调用窗口实际拥有的能力。",
      notes: [
        "修改 Go 服务后执行 node scripts/generate-hybrid.mjs，宿主与导出的 SDK 一起交付。",
        "普通浏览器仅复制 SDK 不会自动拥有桌面能力；内置页面可直接使用仓库的 host-client.js。",
      ],
    },
  ),
  doc(
    "start",
    "environment",
    "外壳环境判断",
    "区分快速环境提示、普通浏览器、SSR 与真正的原生桌面窗口。",
    {
      api: [
        "Hybrid.isDesktopShell()",
        "Hybrid.detectEnvironment(options)",
        "Host.GetEnvironment()",
      ],
      caps: ["host"],
      params: [["timeoutMs", "number", "1500", "原生环境探测的等待上限。"]],
      code: `import { Hybrid } from '/native/client.js';\n\nconst hint = Hybrid.isDesktopShell();\nconst environment = await Hybrid.detectEnvironment();\nif (environment.isDesktopShell) {\n  console.log('原生窗口', environment.platform);\n}\nconsole.log(hint, environment.kind);`,
      returns: "异步探测返回 kind、isDesktopShell、connected 等环境信息；同步方法只是快速提示。",
      notes: [
        "安全判断使用异步原生证明，不仅检查 window 或当前网址。",
        "SSR 中可安全导入 SDK；连接失败返回环境状态。",
      ],
    },
  ),
  doc(
    "start",
    "permissions",
    "来源与能力授权",
    "通过来源、原生窗口身份和能力上限约束每一次桌面调用。",
    {
      api: [],
      caps: [],
      language: "json",
      code: `{
  "url": "https://app.example.com/",
  "desktopCapabilities": ["files", "clipboard", "screenshot"],
  "permissions": {
    "https://app.example.com": ["host", "ui", "windows", "files", "events"]
  }
}`,
      params: [
        ["desktopCapabilities", "string[]", "[]", "仅给内置页面追加桌面能力。"],
        ["permissions", "来源 → 能力[]", "{}", "显式列表覆盖该来源默认授权；空数组拒绝全部。"],
        ["allowedOrigins", "string[]", "[]", "补充可信来源，不等于授予所有桌面能力。"],
      ],
      returns: "配置校验成功后在下次启动应用生效。",
      notes: [
        "默认基础能力：host、windows、ui、ui.html、store、log、lifecycle、events。敏感桌面能力需显式开启。",
        "来源只写 scheme://host[:port]，不带路径、末尾斜杠或通配符。",
        "新窗口继承调用者的能力上限；直接调用生成绑定也不能绕过检查。单次 RPC 最大 1 MiB。",
      ],
    },
  ),
  doc(
    "start",
    "calls",
    "超时、取消与错误",
    "统一包装宿主调用，保留稳定错误码，支持 AbortSignal 和调用超时。",
    {
      api: ["Hybrid.call(operation, options)", "normalizeError(error)"],
      caps: [],
      params: [
        ["operation", "() => Promise", "必填", "发起一次宿主调用。"],
        ["timeoutMs", "number", "30000", "SDK 等待超时，毫秒。"],
        ["signal", "AbortSignal", "可选", "请求取消等待及底层调用；业务副作用不保证撤销。"],
      ],
      code: `import { Hybrid, Desktop, normalizeError } from '/native/client.js';\n\nconst controller = new AbortController();\ntry {\n  const grant = await Hybrid.call(\n    () => Desktop.SelectFile('open', ''),\n    { timeoutMs: 300000, signal: controller.signal },\n  );\n  console.log(grant.name);\n} catch (error) {\n  const value = normalizeError(error);\n  console.log(value.code, value.message);\n}`,
      notes: [
        "文件对话框、截图框选等用户交互适当提高等待时长。",
        "常见错误：E_PERMISSION_DENIED、E_INVALID_ARGUMENT、E_BUSY、E_CANCELLED、E_UNSUPPORTED。",
      ],
    },
  ),
  doc(
    "start",
    "settings",
    "配置与界面偏好",
    "读取运行中配置和磁盘配置，保存主题、语言、通知偏好。",
    {
      api: [
        "Desktop.GetSettings()",
        "Desktop.SaveSettings(config)",
        "Desktop.SavePreferences(preferences)",
        "Desktop.ResetPreferences()",
      ],
      caps: ["settings"],
      params: [
        ["config", "完整配置对象", "必填", "从 saved 快照修改后保存，不只提交一个字段。"],
        ["theme", "system | light | dark", "system", "业务界面偏好。"],
        ["locale", "zh-CN | en-US", "zh-CN", "界面语言偏好。"],
        ["notifications", "boolean", "true", "是否启用系统通知。"],
      ],
      code: `import { Desktop } from '/native/client.js';\n\nconst snapshot = await Desktop.GetSettings();\nawait Desktop.SavePreferences({\n  theme: 'system', locale: 'zh-CN', notifications: true,\n});\nconst next = { ...snapshot.saved, splashEnabled: false };\nconst saved = await Desktop.SaveSettings(next);\nconsole.log(saved.restartRequired);`,
      returns: "SettingsSnapshot：active、saved、preferences、restartRequired。",
      notes: [
        "优先读取 HYBRID_SETTINGS，其次用户目录、程序目录，开发环境最后查找仓库配置。",
        "窗口、安全来源、启动屏等设置重启生效；图标可显式热重载。系统凭据不写入普通偏好。",
      ],
    },
  ),
  doc("ui", "notify", "轻量通知", "带顶部倒计时进度条的独立通知，支持位置、自动堆叠和拖拽。", {
    api: ["UI.Notify(text, options)"],
    caps: ["ui"],
    params: [
      ["text", "string", "必填", "纯文本，1–4096 字节。也支持对象形式。"],
      [
        "place",
        "位置枚举",
        "topRight",
        "topLeft / topRight / topCenter / bottomLeft / bottomRight / bottomCenter / center。",
      ],
      ["durationSeconds", "number", "4", "0.1–3600 秒；省略或 0 使用 4 秒。"],
      ["draggable", "boolean", "true", "false 时禁止网页区域拖拽。"],
    ],
    code: `import { UI } from '/native/client.js';\n\nawait UI.Notify('数据已保存');\nawait UI.Notify('导出完成', {\n  place: 'bottomRight', durationSeconds: 6, draggable: false,\n});`,
    returns: "通知请求完成后返回；不会等待四秒倒计时结束。",
    notes: [
      "同位置通知自动避让；上方位置向下堆叠，下方位置向上堆叠。",
      "拖离堆叠区后通知独立定位；来源窗口关闭时清理其通知。与系统通知中心是两个不同接口。",
    ],
  }),
  doc("ui", "alert", "消息弹框", "展示纯文本标题与正文，支持自动关闭，兼容旧的四参数调用。", {
    api: ["UI.Alert(options)"],
    caps: ["ui"],
    params: [
      ["title / body", "string", "必填", "按纯文本显示，支持引号和换行。"],
      ["destroy", "number", "0", "自动关闭秒数；≤0 不自动关闭。"],
      ["place", "string", "center", "支持角落位置，未知值回退到居中。"],
      ["draggable", "boolean", "true", "是否允许拖拽。"],
    ],
    code: `import { UI } from '/native/client.js';\n\nawait UI.Alert({\n  title: '操作完成', body: '文件已经保存。',\n  destroy: 3, place: 'center', draggable: true,\n});\n// 旧调用仍然兼容。\nawait UI.Alert('提示', '保存成功', 3, 'topRight');`,
    notes: ["Alert 的 destroy 与 Notify 的 durationSeconds 都是秒，名称不同。"],
  }),
  doc("ui", "confirm", "确认对话框", "等待用户确认，支持自定义按钮、超时、取消等待和关闭清理。", {
    api: ["UI.Confirm(options)"],
    caps: ["ui"],
    params: [
      ["title / body", "string", "可选", "纯文本内容。"],
      ["okText / cancelText", "string", "默认按钮文本", "确认与取消按钮文案。"],
      ["timeoutSeconds", "number", "300", "≤0 使用默认值，最多一天。"],
      ["draggable", "boolean", "true", "是否允许拖拽。"],
    ],
    code: `import { UI } from '/native/client.js';\n\nconst answer = await UI.Confirm({\n  title: '继续处理？', body: '确认后开始下一步。',\n  okText: '继续', cancelText: '返回', timeoutSeconds: 60,\n});\nif (answer === '1') console.log('用户已确认');`,
    returns: "字符串 '1' 表示确认；'0' 表示取消、关闭或超时。",
    notes: [
      "返回值是字符串，不要按布尔值直接判断。",
      "原始 Wails Promise 支持 cancel()；主动取消后按 CancelError 处理。",
    ],
  }),
  doc("ui", "html", "自定义 HTML 窗口", "用可信 HTML 展示自定义内容，配置独立窗口的尺寸与位置。", {
    api: ["UI.Show(options)", "UI.OpenWindowByUrl(options)"],
    caps: ["ui.html", "windows"],
    params: [
      ["body", "string", "Show 必填", "只接收可信 HTML，可包含应用自己的处理器。"],
      ["width / height", "number", "窗口默认值", "逻辑像素。"],
      ["url", "string", "打开网页时必填", "受来源导航策略控制。"],
      ["draggable", "boolean", "true", "Show 的内容是否可拖拽。"],
    ],
    code: `import { UI } from '/native/client.js';\n\nawait UI.Show({\n  body: '<section><h2>任务摘要</h2><p>全部完成</p></section>',\n  width: 420, height: 240, place: 'center', draggable: true,\n});\nawait UI.OpenWindowByUrl({\n  url: '/', title: '工作台', width: 1000, height: 720,\n});`,
    notes: [
      "Show 需要 ui.html，打开网址需要 windows，按实际调用分别授权。",
      "Show 不清洗 HTML，不要直接放入用户输入或第三方网页内容。",
    ],
  }),
  doc(
    "ui",
    "splash",
    "启动屏与加载动画",
    "独立启动屏、旋转细环与流动加载条，主页面就绪后自动交接。",
    {
      api: [],
      caps: [],
      language: "json",
      code: `{ "splashEnabled": true, "loadTimeoutSeconds": 30 }`,
      params: [
        ["splashEnabled", "boolean", "true", "false 时不创建独立启动屏，重启生效。"],
        ["loadTimeoutSeconds", "number", "30", "业务页面加载超时，失败后显示恢复页。"],
      ],
      returns: "启动屏最短展示 600ms，完成后淡出；不会用动画模拟真实百分比。",
      notes: [
        "Escape、Alt+F4、右上角按钮或托盘唤醒可跳过启动屏。",
        "启动屏自身 4 秒未就绪会展示主窗口；业务加载仍使用独立超时。",
        "Host.GetStartupState / DismissSplash 仅供内置启动屏，不是普通业务页面的控制入口。",
      ],
    },
  ),
  doc(
    "ui",
    "icons",
    "品牌与动态图标",
    "配置应用和托盘 PNG，通过重新加载即时更新运行中的窗口图标。",
    {
      api: ["Desktop.ReloadIcons()"],
      caps: ["settings"],
      params: [
        ["icons.application", "本地路径 / HTTPS", "内置图标", "相对路径以配置文件目录为基准。"],
        ["icons.tray", "本地路径 / HTTPS", "跟随应用图标", "可单独设置；两项均空恢复内置资源。"],
      ],
      code: `import { Desktop } from '/native/client.js';\n\nconst { saved } = await Desktop.GetSettings();\nawait Desktop.SaveSettings({\n  ...saved, icons: { application: 'assets/app.png', tray: '' },\n});\nawait Desktop.ReloadIcons();`,
      notes: [
        "PNG 必须为 16–1024 像素正方形，≤2 MiB。远程地址最多等待 8 秒。",
        "两个资源校验完成后才替换，失败保留原图标；不自动监视配置文件。",
        "Windows 运行窗口可动态更新，EXE 文件和固定快捷方式仍受打包资源及系统缓存影响。",
      ],
    },
  ),
  doc(
    "desktop",
    "files",
    "文件选择与读写",
    "用户选择文件后取得当前窗口的访问凭证，按 token 读取和保存。",
    {
      api: [
        "Desktop.SelectFile(mode, name)",
        "Desktop.ReadFile(token)",
        "Desktop.WriteFile(token, base64)",
        "Desktop.ReleaseFile(token)",
        "Desktop.RevealFile(token)",
      ],
      caps: ["files"],
      params: [
        ["mode", "open | save | directory", "必填", "打开普通文件、选择保存位置或选择目录。"],
        ["name", "string", "可选", "保存对话框的建议文件名。"],
        ["token", "string", "必填", "当前来源、当前窗口取得的文件凭证。"],
        ["base64", "string", "写入时必填", "二进制字节经 Base64 编码，单文件读写最多 512 KiB。"],
      ],
      code: `import { Desktop, Hybrid } from '/native/client.js';\n\nconst grant = await Hybrid.call(\n  () => Desktop.SelectFile('save', 'hello.txt'), { timeoutMs: 300000 },\n);\ntry {\n  const bytes = new TextEncoder().encode('你好，桌面');\n  const base64 = btoa(String.fromCharCode(...bytes));\n  await Desktop.WriteFile(grant.token, base64);\n} finally { await Desktop.ReleaseFile(grant.token); }`,
      returns:
        "FileGrant 包含 token、name、path、directory、writable；path 用于显示，操作仍传 token。",
      notes: [
        "open 不隐式授予写权限；save 会授权覆盖用户选择的目标。",
        "大文件使用下载中心，不通过小文件 RPC 传输。用户取消对话框会返回错误，请在业务端处理。",
      ],
    },
  ),
  doc(
    "desktop",
    "directories",
    "目录浏览与定位",
    "浏览用户选中的目录，逐项打开直接子文件，并在系统文件管理器中定位。",
    {
      api: [
        "Desktop.ListDirectory(token)",
        "Desktop.OpenChild(token, name)",
        "Desktop.RevealFile(token)",
      ],
      caps: ["files"],
      params: [
        ["token", "string", "目录选择凭证", "从 SelectFile('directory', '') 获得。"],
        ["name", "string", "直接子项名称", "不允许路径分隔符、目录跳转或符号链接。"],
      ],
      code: `import { Desktop, Hybrid } from '/native/client.js';\n\nconst directory = await Hybrid.call(\n  () => Desktop.SelectFile('directory', ''), { timeoutMs: 300000 },\n);\ntry {\n  const entries = await Desktop.ListDirectory(directory.token);\n  console.log(entries);\n  await Desktop.RevealFile(directory.token);\n} finally { await Desktop.ReleaseFile(directory.token); }`,
      notes: ["一次最多列出 500 个条目。OpenChild 不会额外授予写权限，子项凭证也应单独释放。"],
    },
  ),
  doc(
    "desktop",
    "downloads",
    "下载管理",
    "后台下载到用户选择的位置，查询进度，支持取消、重试和打开结果。",
    {
      api: [
        "Desktop.StartDownload(url, token)",
        "Desktop.ListDownloads()",
        "Desktop.CancelDownload(id)",
        "Desktop.RetryDownload(id)",
        "Desktop.OpenDownload(id, reveal)",
        "Desktop.RemoveDownload(id)",
      ],
      caps: ["downloads", "files"],
      params: [
        ["url", "HTTP(S) URL", "必填", "受下载地址与响应限制检查。"],
        ["token", "保存文件凭证", "必填", "当前窗口通过 save 对话框选择的目标。"],
        ["id", "string", "任务 ID", "由 StartDownload 返回。"],
        ["reveal", "boolean", "调用时指定", "true 定位文件，false 打开文件。"],
      ],
      code: `import { Desktop, Hybrid } from '/native/client.js';\n\nconst destination = await Hybrid.call(\n  () => Desktop.SelectFile('save', 'report.csv'), { timeoutMs: 300000 },\n);\nconst job = await Desktop.StartDownload(\n  'https://example.com/report.csv', destination.token,\n);\nconsole.log(job.id, await Desktop.ListDownloads());`,
      notes: [
        "最多同时 4 个下载任务，单文件最多 2 GiB；任务期间受到退出保护。",
        "取消或失败后可从头重试，不是断点续传。RemoveDownload 清理记录，不删除文件。",
      ],
    },
  ),
  doc("desktop", "clipboard", "文本与图片剪贴板", "读写系统文本剪贴板，以及 PNG 图像和兼容位图。", {
    api: ["Desktop.ClipboardText(operation, text)", "Desktop.ClipboardImage(base64, write)"],
    caps: ["clipboard"],
    params: [
      ["operation", "read | write | clear", "必填", "读取、写入或清空文本。"],
      ["text", "string", "读取时为空", "文本最多 512 KiB。"],
      ["base64", "string", "写入时必填", "PNG 编码的 Base64，普通图像 RPC 上限 512 KiB。"],
      ["write", "boolean", "必填", "true 写入图片，false 读取。"],
    ],
    code: `import { Desktop } from '/native/client.js';\n\nawait Desktop.ClipboardText('write', '已复制到剪贴板');\nconst text = await Desktop.ClipboardText('read', '');\nconst pngBase64 = await Desktop.ClipboardImage('', false);\nconsole.log(text, pngBase64.length);`,
    notes: [
      "剪贴板被其他程序占用时明确报错。Windows 已实现 PNG 和常见 DIB 兼容，其他平台按实现能力返回 E_UNSUPPORTED。",
      "截图可直接通过 Screenshot 的 output 写入剪贴板，不必绕经小图片 RPC。",
    ],
  }),
  doc(
    "desktop",
    "screenshot",
    "区域截屏与手动框选",
    "传入物理坐标截图，或冻结指定显示器供用户拖动框选。",
    {
      api: ["Desktop.CaptureDisplays()", "Desktop.Screenshot(options)"],
      caps: ["screenshot"],
      params: [
        ["mode", "region | select", "region", "坐标截图或手动框选。"],
        ["x / y / width / height", "number", "region 时填写", "虚拟桌面物理像素，支持多屏负坐标。"],
        ["displayId", "string", "select 时可选", "从 CaptureDisplays 返回的 ID 选择显示器。"],
        ["output", "base64 | clipboard | both", "base64", "写剪贴板额外需要 clipboard。"],
      ],
      code: `import { Desktop, Hybrid } from '/native/client.js';\n\nconst image = await Hybrid.call(() => Desktop.Screenshot({\n  mode: 'select', output: 'base64',\n}), { timeoutMs: 125000 });\nconst dataURL = 'data:image/png;base64,' + image.base64;\nconsole.log(image.region, dataURL.length);`,
      returns: "region、mime、base64（选择时返回）与 clipboard 标志；PNG 默认不落磁盘。",
      notes: [
        "最多 16 Mi 像素、PNG 最多 20 MiB。手动选择最长等待 2 分钟；Escape、右键或父窗口关闭取消。",
        "坐标使用物理像素，不是浏览器 CSS 像素；先读取 CaptureDisplays。手动框选请在目标桌面验证系统权限与 DPI。",
      ],
    },
  ),
  doc(
    "desktop",
    "system-notify",
    "系统通知",
    "发送到操作系统通知中心，点击后唤醒应用并打开指定站内路由。",
    {
      api: ["Desktop.Notify(title, body, route)"],
      caps: ["notifications"],
      params: [
        ["title / body", "string", "必填", "通知标题及正文。"],
        ["route", "站内路由", "/", "以 / 开头，不传外部系统命令。"],
      ],
      code: `import { Desktop } from '/native/client.js';\n\nawait Desktop.Notify('导出完成', '点击返回工作台', '/#workspace');`,
      notes: [
        "由操作系统决定显示和保留方式；系统勿扰模式可能影响展示。",
        "与 UI.Notify 不同：此接口进入系统通知中心，不提供 UI 通知的堆叠、拖拽和倒计时设置。",
      ],
    },
  ),
  doc(
    "desktop",
    "shortcuts",
    "窗口与全局快捷键",
    "注册本次运行期间的快捷键，唤醒应用并跳转站内页面。",
    {
      api: ["Desktop.SetShortcut(key, route, global, enable)"],
      caps: ["shortcuts"],
      params: [
        ["key", "组合键", "必填", "Ctrl+Shift+[Alt+] 字母或数字。"],
        ["route", "string", "站内路由", "被触发后打开的应用路由。"],
        ["global", "boolean", "必填", "true 时应用未聚焦也响应。"],
        ["enable", "boolean", "必填", "true 注册，false 注销。"],
      ],
      code: `import { Desktop } from '/native/client.js';\n\nawait Desktop.SetShortcut('Ctrl+Shift+Alt+H', '/#workspace', true, true);\n// 组件或业务结束时按需注销。\nawait Desktop.SetShortcut('Ctrl+Shift+Alt+H', '', true, false);`,
      notes: ["快捷键冲突会报错，注册不会跨进程重启自动保留。"],
    },
  ),
  doc(
    "desktop",
    "protocol",
    "URL 协议唤醒",
    "注册当前用户的 hybrid-infra 协议，通过单实例机制转交站内路由。",
    {
      api: ["Desktop.ConfigureProtocol(enable)"],
      caps: ["protocol"],
      params: [["enable", "boolean", "必填", "true 注册当前安装，false 移除当前安装的协议注册。"]],
      code: `import { Desktop } from '/native/client.js';\n\nawait Desktop.ConfigureProtocol(true);\n// 可交给系统打开的链接：\nconst link = 'hybrid-infra://open?route=%2F%23workspace';\nconsole.log(link);`,
      notes: [
        "Windows 当前用户注册，不在每次启动时自动改写注册表。",
        "开发构建路径变化后先移除旧注册。协议只路由页面，不执行任意 shell 命令。",
      ],
    },
  ),
  doc(
    "desktop",
    "credentials",
    "系统凭据库",
    "按应用数据目录及页面来源隔离秘密值，避免写入普通 JSON 配置。",
    {
      api: ["Desktop.Credential(key, value, operation)"],
      caps: ["credentials"],
      params: [
        ["key", "string", "必填", "凭据名称。"],
        ["value", "string", "set 时填写", "秘密值最多 2560 字节；读取和删除传空字符串。"],
        ["operation", "get | set | delete", "必填", "读取、保存或移除系统凭据。"],
      ],
      code: `import { Desktop } from '/native/client.js';\n\nawait Desktop.Credential('demo-token', 'example-value', 'set');\nconst secret = await Desktop.Credential('demo-token', '', 'get');\n// 使用秘密值完成请求，不打印到日志或普通配置。\nawait Desktop.Credential('demo-token', '', 'delete');`,
      notes: [
        "Windows 凭据管理器已实现；其他平台需确认原生实现。",
        "备份不包含系统凭据。这里演示的字符串只是示例，不应硬编码真实密钥。",
      ],
    },
  ),
  doc(
    "bridge",
    "shell",
    "Shell 命令与输出回调",
    "以白名单程序执行后台任务，逐块接收标准输出、错误输出并支持取消。",
    {
      api: [
        "Shell.run(options, callbacks)",
        "Shell.start(options)",
        "Shell.read(id, stdoutOffset, stderrOffset)",
        "Shell.cancel(id)",
        "Shell.remove(id)",
      ],
      caps: ["shell"],
      params: [
        [
          "program / args",
          "string / string[]",
          "程序模式必填",
          "program 必须列于启动配置 shellPrograms，参数单独传递。",
        ],
        ["mode", "program | script", "program", "完整脚本额外需要 shell.script。"],
        ["timeoutMs", "number", "30000", "最长 600000 毫秒。"],
        ["onStdout / onStderr", "function", "可选", "回调保留在 JS，接收增量解码后的文本。"],
      ],
      code: `import { Shell } from '/native/client.js';\n\nconst result = await Shell.run({\n  mode: 'program', program: 'go', args: ['version'], timeoutMs: 5000,\n}, {\n  onStdout: (chunk) => console.log(chunk),\n  onStderr: (chunk) => console.error(chunk),\n});\nconsole.log(result.exitCode, result.truncated);`,
      returns: "run 返回任务状态、退出码及保留的 stdout / stderr；非零退出码请检查结果。",
      notes: [
        "配置示例：shellPrograms: ['go', 'git']，并显式授予 shell。",
        "最多同时 4 个任务，保留输出合计 256 KiB。任务由来源与窗口拥有，窗口关闭会取消其受管理进程。",
        "run 默认清理完成记录；keepHistory:true 时可手动 remove。脚本调用不可拼接不可信输入。",
      ],
    },
  ),
  doc(
    "bridge",
    "cache",
    "进程缓存与全局共享",
    "同来源窗口共享 JSON 内存缓存，支持 TTL、版本号和原子比较更新。",
    {
      api: [
        "Cache.Get(namespace, key, global)",
        "Cache.Set(namespace, key, value, ttlMs, global)",
        "Cache.CompareAndSet(namespace, key, value, ttlMs, global, revision)",
        "Cache.Delete(namespace, key, global)",
        "Cache.Clear(namespace, global)",
      ],
      caps: ["cache"],
      params: [
        ["namespace / key", "string", "必填", "缓存命名空间和键名。"],
        ["ttlMs", "number", "0", "0 表示本次进程内不过期，最长 7 天。"],
        ["global", "boolean", "显式传入", "true 跨来源共享，额外要求 cache.global。"],
        ["revision", "number", "CAS 必填", "使用读取结果的修订号，0 表示要求键不存在。"],
      ],
      code: `import { Cache } from '/native/client.js';\n\nawait Cache.Set('workspace', 'filter', { status: 'open' }, 60000, false);\nconst current = await Cache.Get('workspace', 'filter', false);\nif (current.found) {\n  await Cache.CompareAndSet('workspace', 'filter',\n    { status: 'closed' }, 60000, false, current.revision);\n}`,
      returns: "CacheValue：found、value、revision、expiresAt。",
      notes: ["进程退出后清空；持久化使用 Store。单项最多 64 KiB，总计 16 MiB、4096 项。"],
    },
  ),
  doc(
    "bridge",
    "events",
    "事件监听与触发",
    "跨窗口发布 JSON 事件，JS 回调本地保留，支持一次监听和取消订阅。",
    {
      api: [
        "Events.on(topic, callback, options)",
        "Events.once(topic, callback, options)",
        "Events.emit(topic, data, options)",
        "Events.close()",
        "Events.onError(callback)",
      ],
      caps: ["events"],
      params: [
        ["topic", "string", "必填", "业务事件主题。"],
        ["callback", "function", "监听时必填", "函数留在当前 JS 页面，不序列化传给 Go。"],
        ["scope", "origin | app", "origin", "app 跨来源分发，额外需要 events.global。"],
        ["targetWindow", "string", "可选", "按真实窗口名定向发送。"],
      ],
      code: `import { Events } from '/native/client.js';\n\nconst off = await Events.on('orders.changed', (data) => {\n  console.log('刷新订单列表', data.id);\n});\nawait Events.emit('orders.changed', { id: 42 });\n// 组件卸载时解除本次监听。\nawait off();`,
      returns: "on / once 返回 Promise<取消函数>，需要 await 后再使用；emit 返回匹配监听数量。",
      notes: [
        "事件数据必须为 JSON。emit 不等待回调执行，也不收集回调返回值。",
        "关闭窗口或页面会清理监听会话；可显式 Events.close()，后台错误通过 onError 观察。",
      ],
    },
  ),
  doc(
    "bridge",
    "event-requests",
    "事件请求与异步返回",
    "以主题向唯一处理器发请求，允许处理器通过异步回调返回 JSON。",
    {
      api: ["Events.handle(topic, callback, options)", "Events.request(topic, data, options)"],
      caps: ["events"],
      params: [
        ["callback", "(data) => JSON / Promise<JSON>", "处理器必填", "执行位置仍在监听窗口的 JS。"],
        ["timeoutMs", "number", "10000", "1–30000 毫秒。"],
        ["targetWindow", "string", "存在多个处理器时指定", "避免请求目标歧义。"],
        ["signal", "AbortSignal", "可选", "取消请求等待。"],
      ],
      code: `import { Events } from '/native/client.js';\n\nconst off = await Events.handle('math.sum', async ({ a, b }) => {\n  return { total: a + b };\n});\nconst answer = await Events.request('math.sum', { a: 2, b: 3 },\n  { timeoutMs: 5000 });\nconsole.log(answer.total);\nawait off();`,
      notes: [
        "多个匹配处理器时需指定唯一目标。返回函数、循环引用或非 JSON 数据会报错。",
        "推荐 Events SDK 管理请求和回调；原始 Runtime.Events 是另一套全局事件机制。",
      ],
    },
  ),
  doc(
    "bridge",
    "store",
    "持久化键值存储",
    "把业务 JSON 自动保存在用户数据目录的 store.json，供下次启动使用。",
    {
      api: [
        "Store.Get(key)",
        "Store.Set(key, value)",
        "Store.Delete(key)",
        "Store.Clear()",
        "Store.Save()",
        "Store.Load()",
      ],
      caps: ["store"],
      params: [
        ["key", "string", "必填", "业务数据键名，Store 是整个应用级存储。"],
        ["value", "JSON 值", "Set 必填", "结构化业务数据；秘密值改用系统凭据库。"],
      ],
      code: `import { Store } from '/native/client.js';\n\nawait Store.Set('workspace.preferences', { density: 'comfortable' });\nconst preferences = await Store.Get('workspace.preferences');\nconsole.log(preferences);`,
      notes: [
        "已启用自动保存；不以当前工作目录作为保存目录。Store 不按来源隔离，谨慎授予远程页面。",
        "Clear 会清空整个 Store。Load 会重新读盘，通常无需在普通业务流程主动调用。",
      ],
    },
  ),
  doc(
    "host",
    "windows",
    "具名窗口管理",
    "创建或复用窗口，查询状态、聚焦窗口，并遵循退出任务保护。",
    {
      api: [
        "Windows.Open(options)",
        "Windows.List()",
        "Windows.Get(id)",
        "Windows.Focus(id)",
        "Windows.Close(id)",
      ],
      caps: ["windows"],
      params: [
        ["name", "string", "可自动生成", "同 ID 复用窗口；宿主保留名称不可使用。"],
        ["url", "string", "必填", "小写字段 url，接受本地路径和允许导航来源。"],
        ["width / height", "number", "1000 / 720", "逻辑像素，支持记忆位置与最大化状态。"],
        ["frameLess / place", "boolean / string", "可选", "窗口外观与初始位置。"],
      ],
      code: `import { Windows } from '/native/client.js';\n\nconst detail = await Windows.Open({\n  name: 'order-detail', url: '/#workspace', title: '订单详情',\n  width: 1000, height: 720, place: 'center',\n});\nawait Windows.Focus(detail.id);\nconsole.log(await Windows.Get(detail.id));`,
      notes: [
        "同名窗口只唤醒现有窗口，不会用新参数静默替换其网址。最多同时 50 个普通受管窗口。",
        "新窗口继承调用者能力上限，不能借本地页面扩大权限；有未完成任务时拒绝直接关闭。",
      ],
    },
  ),
  doc(
    "host",
    "lifecycle",
    "任务保护与退出",
    "登记未完成的业务任务，防止关闭或更新重启时丢失工作。",
    {
      api: [
        "Hybrid.withTask(label, operation, options)",
        "Host.BeginTask(label)",
        "Host.EndTask(id)",
        "Host.GetPendingTasks()",
        "Host.Quit()",
      ],
      caps: ["lifecycle"],
      params: [
        ["label", "string", "必填", "面向用户的任务说明。"],
        [
          "operation",
          "async function",
          "withTask 必填",
          "执行实际业务，负责自己的取消和错误处理。",
        ],
        ["id", "string", "EndTask 必填", "只能结束当前窗口拥有的任务。"],
      ],
      code: `import { Hybrid } from '/native/client.js';\n\nawait Hybrid.withTask('同步业务数据', async () => {\n  // 替换为自己的真实业务工作。\n  const response = await fetch('/api/sync');\n  if (!response.ok) throw new Error('同步失败');\n  return response.json();\n});`,
      notes: [
        "withTask 在 finally 中释放登记，失败路径也会清理。登记本身不会自动实现业务取消。",
        "下载、命令、截图框选等内置长任务已有相应的退出保护。",
      ],
    },
  ),
  doc(
    "host",
    "navigation",
    "加载、重试与恢复",
    "统一控制网页加载超时、导航边界、错误页以及已就绪通知。",
    {
      api: ["Host.GetLoadState()", "Host.Retry(home)", "Host.Ready()"],
      caps: ["host"],
      params: [
        ["home", "boolean", "必填", "true 回到内置首页，false 重试当前目标。"],
        ["externalLinks", "browser | block", "browser", "外链交给浏览器或拒绝。"],
        ["navigationOrigins", "string[]", "[]", "额外允许站内导航的来源，不自动授予 RPC 权限。"],
      ],
      code: `import { Host } from '/native/client.js';\n\nconst state = await Host.GetLoadState();\nconsole.log(state.status, state.error);\nawait Host.Ready();\n// 用户点击重试时再调用。\n// await Host.Retry(false);`,
      notes: [
        "配置加载超时默认为 30 秒。错误页提供重试与返回首页，已有任务会阻止不安全重载。",
        "跨屏位置恢复会限制到可见工作区，支持显示器移除、DPI 和系统唤醒后的校正。",
      ],
    },
  ),
  doc(
    "host",
    "tray",
    "托盘与单实例",
    "关闭时可隐藏到托盘，重复启动唤醒原实例，托盘提供固定管理入口。",
    {
      api: [],
      caps: [],
      language: "json",
      code: `{\n  "singleInstance": true,\n  "trayEnabled": true,\n  "closeBehavior": "hide",\n  "rememberWindows": true\n}`,
      params: [
        ["singleInstance", "boolean", "true", "按数据目录区分实例，重复启动转交参数。"],
        ["trayEnabled", "boolean", "false", "独立开启托盘；hide 模式仍强制提供托盘。"],
        ["closeBehavior", "exit | hide", "exit", "关闭主窗口时退出或隐藏。"],
        ["rememberWindows", "boolean", "true", "记忆窗口位置、尺寸及最大化状态。"],
      ],
      returns: "配置在下次启动生效。托盘显示任务状态、开发文档、宿主管理与桌面能力入口。",
      notes: ["同一数据目录使用系统独占锁；需要多进程时指定不同 HYBRID_DATA_DIR。"],
    },
  ),
  doc(
    "host",
    "devtools",
    "开发文档与调试入口",
    "从托盘打开本页，或在已有页面的 F12 控制台输入固定命令。",
    {
      api: ["Hybrid.openDocs(options)", "Host.OpenDeveloperDocs()"],
      caps: ["host"],
      params: [
        [
          "debug",
          "boolean",
          "false",
          "设为 true 并重启；还需要开发构建或含 devtools 标签的正式包。",
        ],
        ["trayEnabled", "boolean", "false", "开启托盘后可点击开发文档菜单。"],
      ],
      code: `// F12 控制台直接输入（原生页面加载完成后由宿主提供）：\nhybridDocs();\n\n// 业务前端也可通过导出的 SDK 打开。\nconst { Hybrid } = await import('/native/client.js');\nawait Hybrid.openDocs();`,
      returns: "复用一个固定文档窗口；SDK 返回窗口信息。",
      notes: [
        "hybridDocs() 发送固定原生意图，不需要先导入 SDK；无 host 权限时宿主不会执行打开操作。",
        "文档窗口的示例可使用配置已授权的内置页面能力；文件、Shell 等仍需显式配置，不会自动新增权限。",
        "debug:false 时不通过本入口强行开启 F12。工作台也保留开发文档链接。",
        "默认正式包移除了开发者工具。开发时用 wails3 dev；需要正式调试包可执行 wails3 task build DEVTOOLS=true（Windows），再配置 debug:true。",
      ],
    },
  ),
  doc("host", "logging", "日志接口", "使用内置日志服务输出运行信息，或查询和调整日志级别。", {
    api: ["Log.Log(level, message, ...args)", "Log.LogLevel()", "Log.SetLogLevel(level)"],
    caps: ["log"],
    params: [
      ["level", "日志级别", "必填", "使用生成绑定中导出的 LogLevel 枚举。"],
      ["message / args", "string / any[]", "必填 / 可选", "消息和结构化字段，不放入令牌或密码。"],
    ],
    code: `import { Log } from '/native/client.js';\n\nconst currentLevel = await Log.LogLevel();\nconsole.log(currentLevel);`,
    notes: ["普通日志与维护诊断是两个入口；诊断包只保留结构化错误码，不自动打包完整日志。"],
  }),
  doc(
    "maintenance",
    "diagnostics",
    "诊断摘要与导出",
    "查看版本、内存、协程、窗口任务数量和最近的结构化错误码。",
    {
      api: ["Maintenance.GetDiagnostics()", "Maintenance.ExportDiagnostics(token)"],
      caps: ["diagnostics"],
      params: [["token", "保存文件凭证", "导出时必填", "导出额外要求 files 权限。"]],
      code: `import { Maintenance, Desktop, Hybrid } from '/native/client.js';\n\nconst report = await Maintenance.GetDiagnostics();\nconst destination = await Hybrid.call(\n  () => Desktop.SelectFile('save', 'hybrid-diagnostics.json'),\n  { timeoutMs: 300000 },\n);\ntry { await Maintenance.ExportDiagnostics(destination.token); }\nfinally { await Desktop.ReleaseFile(destination.token); }\nconsole.log(report.version, report.taskCount);`,
      notes: [
        "不收集业务 KV、网页 URL、截图、命令输出、秘密值或完整异常文本。",
        "最近 128 条事件落盘并可重载；不是操作系统原生崩溃转储。",
      ],
    },
  ),
  doc(
    "maintenance",
    "backups",
    "数据备份与恢复",
    "为业务 KV、偏好、窗口状态和数据版本创建完整性可校验的快照。",
    {
      api: [
        "Maintenance.CreateBackup()",
        "Maintenance.ListBackups()",
        "Maintenance.StageRestore(id)",
      ],
      caps: ["backups"],
      params: [
        ["id", "32 位十六进制 ID", "恢复时必填", "来自校验通过的备份清单，不接收任意路径。"],
      ],
      code: `import { Maintenance } from '/native/client.js';\n\nconst snapshot = await Maintenance.CreateBackup();\nconst backups = await Maintenance.ListBackups();\nconsole.log(snapshot.id, backups.length);\n// 用户明确选择并确认后，再排队恢复：\n// await Maintenance.StageRestore(snapshot.id);`,
      returns: "备份 ID、时间、版本、数据 Schema、大小及逐文件哈希。",
      notes: [
        "恢复在下次启动、KV 读取之前进行，先为当前状态创建备份。",
        "不包含权限配置和系统凭据。业务 KV 可能有敏感数据，备份文件没有额外加密。",
        "每文件最多 16 MiB、总解压内容 48 MiB；校验路径白名单、JSON、版本和 SHA256。",
      ],
    },
  ),
  doc(
    "maintenance",
    "updates",
    "签名更新与回滚",
    "检查 Ed25519 签名清单，后台下载校验包，在无业务任务时安装重启。",
    {
      api: [
        "Maintenance.GetUpdateState()",
        "Maintenance.CheckUpdates()",
        "Maintenance.DownloadUpdate()",
        "Maintenance.CancelUpdate()",
        "Maintenance.InstallUpdate()",
        "Maintenance.RollbackUpdate()",
      ],
      caps: ["updates"],
      params: [
        ["updates.enabled", "boolean", "false", "先提供清单地址、公钥和频道再开启。"],
        [
          "manifestUrl / publicKey",
          "HTTPS / Base64",
          "启用时必填",
          "固定信任根，前端调用不能临时更换。",
        ],
        ["channel", "stable | beta", "stable", "当前发布版本接受三段正式数字版本。"],
      ],
      code: `import { Maintenance } from '/native/client.js';\n\nconst state = await Maintenance.CheckUpdates();\nif (state.stage === 'available') {\n  await Maintenance.DownloadUpdate();\n}\nconsole.log(await Maintenance.GetUpdateState());\n// 下载为 ready，且用户确认后再安装重启：\n// await Maintenance.InstallUpdate();`,
      notes: [
        "自动安装当前支持可写目录中的 Windows 便携 EXE；系统安装器和其他平台没有接入自动替换。",
        "启动失败会回退程序及更新前数据。手动 RollbackUpdate 额外要求 backups，并会恢复旧数据快照。",
        "checkOnStartup / autoDownload 只控制检查和下载，不自动安装。断电或工作进程被强制终止仍可能需要人工恢复。",
      ],
    },
  ),
  doc(
    "maintenance",
    "migrations",
    "数据迁移与离线发布",
    "以 Schema 管理数据兼容，使用离线私钥为发布的 EXE 与清单签名。",
    {
      api: [],
      caps: [],
      language: "shell",
      code: `wails3 task build VERSION=0.2.0\nnode scripts/package-release.mjs --exe bin/hybrid-infra.exe --version 0.2.0 --url https://example.com/hybrid-infra-0.2.0-windows-amd64.exe --key-file C:/private/release.pem --out C:/release/hybrid-0.2.0`,
      params: [
        ["当前 Schema", "number", "1", "旧目录按 Schema 0 处理，迁移前保留白名单数据快照。"],
        ["--key-file", "Ed25519 PKCS8 PEM", "必填", "离线私钥不提交仓库或公开发布目录。"],
        ["--version", "major.minor.patch", "必填", "需与 EXE 编译版本匹配。"],
      ],
      returns: "发布目录包含 EXE、签名 manifest.json 与 public-key.txt。",
      notes: [
        "未来数据版本会阻止旧程序静默降级写入。旧仓库根目录 store.json 不自动覆盖用户目录。",
        "Ed25519 更新签名不替代 Windows Authenticode、macOS 公证或平台安装器签名。",
      ],
    },
  ),
];

/** 检索功能名称、API、权限及方法介绍；标题和接口命中优先，避免说明中的偶然提及排在真正功能前。 */
export function searchFeatures(query) {
  const words = query.normalize("NFKC").toLowerCase().trim().split(/\s+/).filter(Boolean);
  return features
    .map((item, index) => {
      const fields = [
        item.title,
        item.api.join(" "),
        item.caps.join(" "),
        item.summary,
        [
          ...item.api.map((signature) => methodDescriptions[signature.split("(")[0]]),
          ...item.notes,
          ...item.params.flat(),
        ].join(" "),
      ].map((value) => value.normalize("NFKC").toLowerCase());
      const text = fields.join(" ");
      const score = words.reduce(
        (sum, word) =>
          sum +
          Math.max(
            ...fields.map((field, position) =>
              field.includes(word) ? [12, 8, 6, 4, 1][position] : 0,
            ),
          ),
        0,
      );
      return { item, index, score, matches: words.every((word) => text.includes(word)) };
    })
    .filter((result) => result.matches)
    .sort((a, b) => b.score - a.score || a.index - b.index)
    .map((result) => result.item);
}
