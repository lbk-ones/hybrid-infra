import { Desktop, Host, Hybrid, normalizeError } from "./host-client.js";
import { permissionCatalog } from "./docs-content.js";
import {
  textSettings,
  numberSettings,
  booleanSettings,
  mergeSettingsDraft,
  changedSettingsFields,
  applySettingsPatch,
} from "./settings-model.js";

/** 取得固定控件，不把配置内容拼接为 HTML 或选择器。 */
const $ = (id) => document.getElementById(id);
const form = $("settings-form");
let snapshot, draftBase;
let originRows = [];
let capabilities = new Set();
let busy = false;

/** 按固定字段名读取表单，避免 title 等名字与 DOM 属性冲突。 */
const field = (name) => form.elements.namedItem(name);

/** 显示操作提示，保留宿主错误码；所有内容均为纯文本。 */
function notice(text, error = false) {
  $("settings-notice").textContent = text;
  $("settings-notice").classList.toggle("error", error);
}

/** 构建有用途说明的权限复选框，不将来源地址或配置值解释为页面内容。 */
function permissionChoices(container, selected) {
  container.replaceChildren();
  for (const permission of permissionCatalog) {
    const label = document.createElement("label");
    const input = document.createElement("input");
    input.type = "checkbox";
    input.value = permission.name;
    input.checked = (selected || []).includes(permission.name);
    const text = document.createElement("span");
    const code = document.createElement("code");
    code.textContent = permission.name;
    const hint = document.createElement("small");
    hint.textContent = permission.description;
    text.append(code, hint);
    label.append(input, text);
    container.append(label);
  }
}

/** 获取勾选权限，保留空数组表示显式拒绝全部授权的语义。 */
function selectedPermissions(container) {
  return [...container.querySelectorAll("input:checked")].map((input) => input.value);
}

/** 添加来源权限草稿，删除行只影响草稿，真正保存仍须点击提交并通过原生检查。 */
function addOrigin(origin = "", permissions = []) {
  const section = document.createElement("section");
  section.className = "settings-origin";
  const row = document.createElement("div");
  row.className = "settings-row";
  const label = document.createElement("label");
  label.className = "settings-origin-label";
  label.append(document.createTextNode("来源 origin"));
  const input = document.createElement("input");
  input.value = origin;
  input.placeholder = "https://app.example.com";
  label.append(input);
  const remove = document.createElement("button");
  remove.type = "button";
  remove.textContent = "移除此来源";
  row.append(label, remove);
  const choices = document.createElement("div");
  choices.className = "settings-permissions";
  permissionChoices(choices, permissions);
  section.append(row, choices);
  $("origin-permissions").append(section);
  const item = { section, input, choices };
  originRows.push(item);
  remove.addEventListener("click", () => {
    originRows = originRows.filter((entry) => entry !== item);
    section.remove();
    updatePreview();
  });
}

/** 完整配置载入表单，未显示的更新源等字段仍保留在草稿中，避免保存时丢失。 */
function writeForm(config) {
  draftBase = structuredClone(config);
  for (const name of [...textSettings, ...numberSettings]) field(name).value = config[name] ?? "";
  for (const name of booleanSettings) field(name).checked = config[name] === true;
  for (const name of ["allowedOrigins", "navigationOrigins", "shellPrograms"])
    field(name).value = (config[name] || []).join("\n");
  field("iconApplication").value = config.icons?.application || "";
  field("iconTray").value = config.icons?.tray || "";
  permissionChoices($("desktop-permissions"), config.desktopCapabilities || []);
  originRows = [];
  $("origin-permissions").replaceChildren();
  for (const [origin, permissions] of Object.entries(config.permissions || {}))
    addOrigin(origin, permissions);
  $("settings-json").value = JSON.stringify(config, null, 2);
  updatePreview();
}

/** 将表单合并到原始完整配置；格式不正确时中断，不向宿主提交半份配置。 */
function readForm() {
  const values = {};
  for (const name of [
    ...textSettings,
    ...numberSettings,
    "allowedOrigins",
    "navigationOrigins",
    "shellPrograms",
    "iconApplication",
    "iconTray",
  ])
    values[name] = field(name).value;
  for (const name of booleanSettings) values[name] = field(name).checked;
  values.desktopCapabilities = selectedPermissions($("desktop-permissions"));
  return mergeSettingsDraft(
    draftBase,
    values,
    originRows.map((row) => ({
      origin: row.input.value,
      capabilities: selectedPermissions(row.choices),
    })),
  );
}

/** 刷新预览、未保存字段和待重启字段；普通编辑不写磁盘，不热更新权限。 */
function updatePreview() {
  if (!snapshot) return;
  $("local-root-field").hidden = field("pageMode").value !== "local";
  $("choose-local-root").disabled = !capabilities.has("files") || busy;
  try {
    const config = readForm();
    $("settings-preview").textContent = JSON.stringify(config, null, 2);
    const changes = changedSettingsFields(snapshot.saved, config);
    $("settings-diff").textContent = changes.length
      ? `未保存 ${changes.length} 项：${changes.join("、")}`
      : "表单与磁盘配置一致，没有未保存修改。";
    $("save-settings").disabled = busy;
  } catch (error) {
    $("settings-diff").textContent = error.message;
    $("settings-preview").textContent = "请先修正表单输入。";
    $("save-settings").disabled = true;
  }
  $("settings-restart").textContent = snapshot.restartRequired
    ? `待重启生效：${snapshot.pendingFields.join("、")}`
    : "当前启动配置已生效";
}

/** 展示实际读写的配置路径、来源和独立数据目录，避免编辑另一个配置文件却误以为已生效。 */
function renderSnapshot(value) {
  snapshot = value;
  const sources = {
    environment: "HYBRID_SETTINGS 指定",
    user: "应用用户目录",
    executable: "EXE 同目录",
    development: "开发项目目录",
    defaults: "默认配置",
  };
  const location = $("settings-location");
  location.replaceChildren();
  for (const [label, text] of [
    ["应用身份", value.appId],
    ["配置来源", sources[value.configSource] || value.configSource],
    ["读写配置文件", value.configPath],
    ["运行数据目录", value.dataDir],
    ["专用协议", `${value.protocolScheme}://open?route=…`],
    ["当前页面", value.active.pageMode || "自动识别"],
  ]) {
    const dt = document.createElement("dt");
    dt.textContent = label;
    const dd = document.createElement("dd");
    dd.textContent = text;
    location.append(dt, dd);
  }
  writeForm(value.saved);
}

/** 串行操作并统一报错；异步保存时禁用输入，防止迟到结果覆盖新的草稿。 */
async function operation(action) {
  if (busy) return;
  busy = true;
  $("settings-fields").disabled = true;
  try {
    await action();
  } catch (error) {
    const value = normalizeError(error);
    notice(`${value.code} · ${value.message}`, true);
  } finally {
    busy = false;
    $("settings-fields").disabled = !capabilities.has("settings");
    updatePreview();
  }
}

// 输入只更新草稿，只有用户明确提交时才通过原有 settings 服务保存完整配置。
form.addEventListener("input", updatePreview);
form.addEventListener("change", updatePreview);
form.addEventListener("submit", (event) => {
  event.preventDefault();
  void operation(async () => {
    renderSnapshot(await Desktop.SaveSettings(readForm()));
    notice(
      snapshot.restartRequired
        ? "配置已保存，重启后生效；当前权限和窗口保持运行中的配置。"
        : "配置已保存，与当前运行一致。",
    );
  });
});
$("add-permission").addEventListener("click", () => {
  addOrigin();
  updatePreview();
});
$("reload-settings").addEventListener("click", () => {
  if (!window.confirm("重新读取会丢弃尚未保存的表单修改，是否继续？")) return;
  void operation(async () => {
    renderSnapshot(await Desktop.GetSettings());
    notice("已重新读取当前应用磁盘配置。");
  });
});
$("choose-local-root").addEventListener("click", () => {
  void operation(async () => {
    const grant = await Hybrid.call(() => Desktop.SelectFile("directory", ""), {
      timeoutMs: 300000,
    });
    try {
      field("localRoot").value = grant.path;
    } finally {
      await Desktop.ReleaseFile(grant.token);
    }
    notice("目录已填入草稿，尚未保存；请确认其中存在入口 HTML。");
  });
});
$("apply-json").addEventListener("click", () => {
  try {
    const next = applySettingsPatch(readForm(), $("settings-json").value, snapshot.appId);
    writeForm(next);
    notice("JSON 已应用到表单草稿，尚未保存。");
  } catch (error) {
    notice(error.message, true);
  }
});

/** 连接宿主并检查 settings 授权；无授权时展示说明，不提供模拟配置或自动扩大权限。 */
async function initialize() {
  const info = await Hybrid.connect();
  capabilities = new Set(info.capabilities);
  await Host.Ready();
  if (!capabilities.has("settings"))
    throw new Error(
      "请在当前来源授权 settings，或在部署配置 desktopCapabilities 中添加 settings 后重启。",
    );
  renderSnapshot(await Desktop.GetSettings());
  notice("已读取当前应用配置。编辑只修改草稿，保存后写入上方配置文件。");
}
void operation(initialize);
