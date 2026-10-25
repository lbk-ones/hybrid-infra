export const textSettings = [
  "title",
  "url",
  "localRoot",
  "pageMode",
  "closeBehavior",
  "externalLinks",
  "windowState",
];
export const numberSettings = ["width", "height", "minWidth", "minHeight", "loadTimeoutSeconds"];
export const booleanSettings = [
  "splashEnabled",
  "singleInstance",
  "trayEnabled",
  "rememberWindows",
  "frameless",
  "debug",
];

/** 拆分每行一项的列表并去重，空行不写入权限来源或程序白名单。 */
export function settingsLines(value) {
  return [
    ...new Set(
      value
        .split(/\r?\n/)
        .map((line) => line.trim())
        .filter(Boolean),
    ),
  ];
}

/** 将可视化字段合并到完整配置，保留未显示的更新等设置；身份由发布配置决定，不能在运行中变更。 */
export function mergeSettingsDraft(base, values, permissionRows) {
  const result = structuredClone(base);
  for (const name of textSettings)
    result[name] = (values[name] ?? (name === "windowState" ? base[name] || "auto" : "")).trim();
  validateWindowState(result.windowState);
  for (const name of booleanSettings) result[name] = Boolean(values[name]);
  for (const name of numberSettings) {
    const raw = values[name];
    const number = Number(raw);
    if (!String(raw).trim() || !Number.isSafeInteger(number) || number < 0)
      throw new Error(`${name} 请填写非负整数`);
    result[name] = number;
  }
  if (result.pageMode !== "local") result.localRoot = "";
  for (const name of ["allowedOrigins", "navigationOrigins", "shellPrograms", "deviceEndpoints"]) {
    const items = settingsLines(values[name] ?? (base[name] || []).join("\n"));
    result[name] = !items.length && base[name] == null ? base[name] : items;
  }
  result.icons = { application: values.iconApplication.trim(), tray: values.iconTray.trim() };
  result.desktopCapabilities = [...new Set(values.desktopCapabilities)];
  if (!result.desktopCapabilities.length && base.desktopCapabilities == null)
    result.desktopCapabilities = base.desktopCapabilities;
  result.permissions = Object.create(null);
  for (const row of permissionRows) {
    let origin;
    try {
      const url = new URL(row.origin.trim());
      if (
        !["http:", "https:"].includes(url.protocol) ||
        url.username ||
        url.password ||
        url.pathname !== "/" ||
        url.search ||
        url.hash
      )
        throw new Error();
      origin = url.origin;
    } catch {
      throw new Error(`权限来源无效：${row.origin || "请填写来源"}，例如 https://app.example.com`);
    }
    if (Object.hasOwn(result.permissions, origin)) throw new Error(`权限来源重复：${origin}`);
    result.permissions[origin] = [...new Set(row.capabilities)];
  }
  if (!permissionRows.length && base.permissions == null) result.permissions = base.permissions;
  return result;
}

/** 应用高级 JSON 补丁前检查字段类型与身份，失败时不触碰原草稿或表单，避免部分更新。 */
export function applySettingsPatch(base, text, identity) {
  const patch = JSON.parse(text);
  if (!patch || typeof patch !== "object" || Array.isArray(patch))
    throw new Error("请填写 JSON 对象");
  const next = { ...structuredClone(base), ...patch };
  if (next.windowState == null && !Object.hasOwn(patch, "windowState")) next.windowState = "auto";
  if (next.appId !== identity) throw new Error("appId 是发布身份，不能在配置中心更换");
  for (const name of textSettings)
    if (typeof next[name] !== "string") throw new Error(`${name} 必须是字符串`);
  validateWindowState(next.windowState);
  for (const name of booleanSettings)
    if (typeof next[name] !== "boolean") throw new Error(`${name} 必须是 true 或 false`);
  for (const name of numberSettings)
    if (!Number.isSafeInteger(next[name]) || next[name] < 0)
      throw new Error(`${name} 必须是非负整数`);
  for (const name of [
    "allowedOrigins",
    "navigationOrigins",
    "shellPrograms",
    "desktopCapabilities",
  ])
    if (
      next[name] != null &&
      (!Array.isArray(next[name]) || next[name].some((item) => typeof item !== "string"))
    )
      throw new Error(`${name} 必须是字符串数组`);
  if (
    next.permissions != null &&
    (typeof next.permissions !== "object" ||
      Array.isArray(next.permissions) ||
      Object.values(next.permissions).some(
        (list) => !Array.isArray(list) || list.some((item) => typeof item !== "string"),
      ))
  )
    throw new Error("permissions 必须是来源到权限数组的映射");
  if (
    next.icons != null &&
    (typeof next.icons !== "object" ||
      Array.isArray(next.icons) ||
      [next.icons.application, next.icons.tray].some(
        (item) => item != null && typeof item !== "string",
      ))
  )
    throw new Error("icons 必须包含字符串图标地址");
  return next;
}

/** 检查启动模式，避免拼写错误被当成普通窗口保存；auto 会跟随上次记住的模式。 */
function validateWindowState(value) {
  if (!["auto", "normal", "maximized", "fullscreen"].includes(value))
    throw new Error("windowState 请选择自动、普通窗口、最大化或全屏");
}

/** 对比配置的顶层字段，标出尚未保存或等待重启的具体项目，不修改输入对象。 */
export function changedSettingsFields(before, after) {
  return [...new Set([...Object.keys(before), ...Object.keys(after)])].filter(
    (name) => JSON.stringify(before[name]) !== JSON.stringify(after[name]),
  );
}
