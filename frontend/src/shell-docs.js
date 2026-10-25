import {
  categories,
  features,
  methodDescriptions,
  permissionCatalog,
  permissionSnippets,
  searchFeatures,
} from "./docs-content.js";
import { buildMethodExample, getMethodExample } from "./docs-examples.js";
import * as DocsSDK from "./host-client.js";
import { createExampleRunner } from "./docs-runner.js";

const { Host, Hybrid } = DocsSDK;

const destructiveExamples = {
  "Store.Clear": "这会清空整个应用的 Store 业务数据，是否继续？",
  "Maintenance.StageRestore": "这会安排下次启动时用指定备份恢复业务数据，是否继续？",
  "Maintenance.InstallUpdate": "这会安装更新并重启应用，是否继续？",
  "Maintenance.RollbackUpdate": "这会回到旧版本并恢复更新前的数据快照，是否继续？",
  "Host.Quit": "这会退出整个应用，是否继续？",
};

/** 获取固定页面控件，不根据外部输入拼接 CSS 选择器。 */
const $ = (id) => document.getElementById(id);
const content = $("docs-content");
const main = $("docs-main");
const search = $("docs-query");
const featureMap = new Map(features.map((item) => [item.id, item]));
const categoryMap = new Map(categories.map((item) => [item.id, item]));
let selected = "overview";
let toastTimer;
// 参数草稿只保存在当前文档页内存中，切换功能时保留，不写入 Store 或本地磁盘。
const exampleDrafts = new Map();
const runtimeViews = new Set();
let nativeReady = false;
const runner = createExampleRunner(DocsSDK, refreshRunViews);
const mobile = matchMedia("(max-width: 800px)");

/** 更新当前可见的运行面板及全页活动计数，不重建参数输入框，以免打断编辑。 */
function refreshRunViews() {
  for (const refresh of runtimeViews) refresh();
  const active = [...runner.states.values()].filter((entry) => entry.busy || entry.cleanup).length;
  $("docs-stop-all").hidden = active === 0;
  $("docs-stop-all").textContent = `停止全部示例 (${active})`;
}

/** 创建文本节点控件，文档内容、搜索词和代码一律通过 textContent 输出。 */
function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

/** 显示短提示并替换旧计时器，避免连续复制时旧计时器提前关闭提示。 */
function notify(text) {
  clearTimeout(toastTimer);
  $("docs-toast").textContent = text;
  $("docs-toast").hidden = false;
  toastTimer = setTimeout(() => {
    $("docs-toast").hidden = true;
  }, 2600);
}

/** 复制明确点击的文档内容；浏览器拒绝剪贴板时选中示例，交给用户手动复制。 */
async function copyText(text, source) {
  try {
    await navigator.clipboard.writeText(text);
    notify("已复制");
  } catch {
    if (source) {
      const range = document.createRange();
      range.selectNodeContents(source);
      const selection = getSelection();
      selection.removeAllRanges();
      selection.addRange(range);
    }
    notify(source ? "已选中示例，请按 Ctrl+C 复制" : "当前环境无法复制，请手动复制地址栏链接");
  }
}

/** 构建只读代码块；复制时读取最新文本，确保参数修改后复制的是新示例，不执行代码。 */
function codeBlock(text, language = "javascript") {
  const block = element("div", "docs-code");
  const heading = element("div", "docs-code-header");
  const code = element("code", "", text);
  const copy = element("button", "", "复制示例");
  copy.type = "button";
  copy.addEventListener("click", () => {
    void copyText(code.textContent, code);
  });
  heading.append(element("span", "", language.toUpperCase()), copy);
  const pre = element("pre");
  pre.append(code);
  block.append(heading, pre);
  return block;
}

/** 展示可编辑、可运行的方法示例；填写时生成代码，只有点击运行才调用真实 SDK，并显示回调、结果与错误。 */
function methodExample(signature, open) {
  const name = signature.split("(")[0];
  const spec = getMethodExample(signature);
  const values = exampleDrafts.get(name) || spec.params.map((field) => field.value);
  exampleDrafts.set(name, values);
  const panel = element("details", "docs-method-example");
  panel.open = open;
  panel.append(
    element("summary", "", spec.params.length ? "调用示例 · 可填写参数" : "调用示例 · 无需参数"),
  );
  const body = element("div", "docs-example-body");
  body.append(
    element(
      "p",
      "docs-example-hint",
      spec.params.length
        ? "填写参数后点击「运行示例」，会真实调用当前窗口的 SDK。JSON 使用双引号；函数模板也会在运行时执行，输出显示在下方。"
        : "这个方法不需要参数，点击「运行示例」即可真实调用，返回结果显示在下方。",
    ),
  );
  if (spec.note) body.append(element("p", "docs-example-hint", spec.note));
  const fields = element("div", "docs-example-fields");
  const controls = [];
  const block = codeBlock("");
  const code = block.querySelector("code");
  const copy = block.querySelector("button");
  const error = element("p", "docs-example-error");
  error.id = `example-error-${name.replaceAll(".", "-")}`;
  error.setAttribute("role", "status");
  error.setAttribute("aria-live", "polite");
  let valid = true;
  const actions = element("div", "docs-run-actions");
  const run = element("button", "docs-run-button", "运行示例");
  const stop = element("button", "docs-example-reset", "停止 / 清理");
  const status = element("span", "docs-run-status", "尚未运行");
  for (const button of [run, stop]) button.type = "button";
  actions.append(run, stop, status);
  const output = element("div", "docs-run-output");
  output.hidden = true;
  const result = element("pre", "docs-run-result");
  const failure = element("pre", "docs-example-error");
  const logs = element("pre", "docs-run-logs");
  output.append(
    element("h4", "", "返回结果"),
    result,
    failure,
    element("h4", "", "回调 / 输出"),
    logs,
  );

  /** 仅刷新本方法运行状态；事件监听和后台输出即使在切换目录后仍记录到同一份状态。 */
  function refresh() {
    const entry = runner.state(signature);
    run.disabled = !valid || entry.busy || Boolean(entry.cleanup) || entry.stopping;
    run.textContent = entry.busy ? "运行中…" : "运行示例";
    stop.disabled = entry.stopping || (!entry.busy && !entry.cleanup);
    status.textContent = {
      idle: "尚未运行",
      running: "正在调用",
      active: "活动中 · 可停止 / 清理",
      done: "调用成功",
      error: "调用失败",
      stopped: "已停止 / 清理",
    }[entry.status];
    output.hidden = entry.status === "idle";
    result.textContent = entry.result || (entry.busy ? "等待返回…" : "—");
    failure.textContent = entry.error;
    logs.textContent = entry.logs.join("\n\n") || "暂无回调或日志输出";
  }
  runtimeViews.add(refresh);
  run.addEventListener("click", () => {
    if (destructiveExamples[name] && !window.confirm(destructiveExamples[name])) return;
    void runner
      .run(signature, [...values], nativeReady)
      .catch((failure) => notify(failure.message));
  });
  stop.addEventListener("click", () => {
    void runner.stop(signature).catch((failure) => notify(failure.message));
  });
  if (["Events.on", "Events.once", "Events.handle"].includes(name)) {
    const trigger = element(
      "button",
      "docs-example-reset",
      name === "Events.handle" ? "发起测试请求" : "触发测试事件",
    );
    trigger.type = "button";
    trigger.addEventListener("click", async () => {
      trigger.disabled = true;
      try {
        await runner.trigger(signature, [...values]);
      } catch (failure) {
        notify(failure.message);
      } finally {
        trigger.disabled = false;
      }
    });
    actions.append(trigger);
  }

  /** 更新代码和复制状态；无效 JSON 不保留旧示例，避免用户误复制上一次参数。 */
  function update() {
    try {
      code.textContent = buildMethodExample(signature, values);
      error.textContent = "";
      copy.disabled = false;
      valid = true;
    } catch (failure) {
      code.textContent = "// 请先修正参数，再复制调用示例。";
      error.textContent = failure.message;
      copy.disabled = true;
      valid = false;
    }
    refresh();
  }

  for (const [index, field] of spec.params.entries()) {
    const wrapper = element("div", "docs-example-field");
    const multiline = ["object", "array", "json", "source"].includes(field.kind);
    if (multiline) wrapper.classList.add("docs-example-field-wide");
    const id = `example-${name.replaceAll(".", "-")}-${index}`;
    const label = element("label", "", field.name);
    label.htmlFor = id;
    const kind = {
      text: "字符串",
      number: "数字",
      boolean: "开关",
      object: "JSON 对象",
      array: "JSON 数组",
      json: "JSON 值",
      source: "函数 / JS 表达式",
    }[field.kind];
    label.append(element("span", "", kind));
    let input;
    if (field.kind === "boolean" || field.choices) {
      input = element("select");
      for (const value of field.choices || ["true", "false"]) {
        const option = element("option", "", value);
        option.value = value;
        input.append(option);
      }
    } else if (multiline) {
      input = element("textarea");
      input.rows = Math.max(3, Math.min(9, field.value.split("\n").length));
      input.spellcheck = false;
    } else {
      input = element("input");
      input.type = field.kind === "number" ? "number" : "text";
      if (field.kind === "number") input.step = "any";
    }
    input.id = id;
    input.value = values[index];
    input.autocomplete = "off";
    const hint = element("small", "", field.hint);
    hint.id = `${id}-hint`;
    input.setAttribute("aria-describedby", `${hint.id} ${error.id}`);
    input.addEventListener("input", () => {
      values[index] = input.value;
      update();
    });
    controls.push(input);
    wrapper.append(label, input, hint);
    fields.append(wrapper);
  }
  if (spec.params.length) {
    const reset = element("button", "docs-example-reset", "恢复示例值");
    reset.type = "button";
    reset.addEventListener("click", () => {
      for (const [index, field] of spec.params.entries()) {
        values[index] = field.value;
        controls[index].value = field.value;
      }
      update();
    });
    body.append(fields, reset);
  }
  body.append(error, actions, output, block);
  panel.append(body);
  update();
  return panel;
}

/** 根据选中项展开对应大类；搜索时展开所有命中的类别，保留大类与功能点两级结构。 */
function renderNavigation(query = "") {
  const nav = $("docs-nav");
  nav.replaceChildren();
  const overview = element("a", "docs-overview-link", "概览 / 功能地图");
  overview.href = "#overview";
  if (selected === "overview" && !query) overview.setAttribute("aria-current", "page");
  nav.append(overview);
  const matches = query ? searchFeatures(query) : features;
  for (const category of categories) {
    const items = matches.filter((item) => item.category === category.id);
    if (!items.length) continue;
    const group = element("details", "docs-nav-group");
    group.open =
      Boolean(query) ||
      featureMap.get(selected)?.category === category.id ||
      (selected === "overview" && category.id === "start");
    const heading = element("summary");
    heading.append(
      element("span", "docs-nav-number", category.icon),
      element("span", "", category.title),
      element("small", "", String(items.length)),
    );
    const links = element("div", "docs-nav-links");
    for (const item of items) {
      const link = element("a", "", item.title);
      link.href = `#${item.id}`;
      if (item.id === selected && !query) link.setAttribute("aria-current", "page");
      links.append(link);
    }
    group.append(heading, links);
    nav.append(group);
  }
  if (!matches.length) nav.append(element("p", "docs-empty", "没有匹配的功能点"));
}

/** 从相同内容源生成概览卡片与统计，数量随功能文档增减而更新。 */
function renderOverview() {
  content.append($("docs-overview").content.cloneNode(true));
  const methods = new Set(features.flatMap((item) => item.api.map((api) => api.split("(")[0])));
  for (const [value, label] of [
    [categories.length, "功能大类"],
    [features.length, "功能点"],
    [methods.size, "调用入口"],
  ]) {
    const stat = element("span");
    stat.append(element("strong", "", String(value)), document.createTextNode(label));
    $("docs-stats").append(stat);
  }
  for (const category of categories) {
    const items = features.filter((item) => item.category === category.id);
    const card = element("a", "docs-category-card");
    card.href = `#${items[0].id}`;
    const top = element("div", "docs-card-top");
    top.append(
      element("span", "docs-card-number", category.icon),
      element("span", "docs-card-arrow", "↗"),
    );
    const tags = element("div", "docs-card-bottom");
    for (const item of items.slice(0, 3)) tags.append(element("span", "", item.title));
    card.append(
      top,
      element("h3", "", category.title),
      element("p", "", `${items.length} 个功能点 · ${category.caption}`),
      tags,
    );
    $("docs-category-grid").append(card);
  }
  $("docs-first-code").append(
    codeBlock(
      "import { UI, Hybrid } from '/native/client.js';\n\nawait Hybrid.connect();\nawait UI.Notify('你好，桌面。');",
    ),
  );
  $("docs-permission-count").textContent = `${permissionCatalog.length} 项`;
  for (const permission of permissionCatalog) {
    const item = element("li");
    const header = element("div", "docs-permission-heading");
    header.append(
      element("code", "", permission.name),
      element("span", "", permission.defaultGranted ? "默认基础" : "显式开启"),
    );
    item.append(header, element("p", "", permission.description));
    $("docs-permission-list").append(item);
  }
  /** 按复制类型替换预览，保证连续切换按钮时选中和复制的始终是当前 JSON。 */
  function showPermissionSnippet(text, label) {
    const preview = codeBlock(text, "json");
    preview.querySelector("button").textContent = label;
    $("docs-permission-code").replaceChildren(preview);
    return preview;
  }
  showPermissionSnippet(permissionSnippets.all, "复制全部权限");
  $("docs-copy-all-permissions").addEventListener("click", () => {
    // 无剪贴板权限时展开并选中实际要复制的 JSON，仍可直接手动复制。
    const preview = showPermissionSnippet(permissionSnippets.all, "复制全部权限");
    preview.closest("details").open = true;
    void copyText(permissionSnippets.all, preview.querySelector("code"));
  });
  $("docs-copy-local-permissions").addEventListener("click", () => {
    const preview = showPermissionSnippet(permissionSnippets.local, "复制内置页面配置");
    preview.closest("details").open = true;
    void copyText(permissionSnippets.local, preview.querySelector("code"));
  });
  const copy = content.querySelector("[data-copy]");
  copy.addEventListener("click", () => {
    void copyText(copy.dataset.copy, copy.previousElementSibling);
  });
}

/** 创建有标题的文档章节，便于屏幕阅读器和内容扫描。 */
function section(title) {
  const node = element("section", "docs-feature-section");
  node.append(element("h2", "", title));
  return node;
}

/** 渲染功能详情：能力、签名、参数、示例、返回值和边界，全部来自可测试的文档数据。 */
function renderFeature(item) {
  const article = element("article", "docs-feature");
  const header = element("header");
  const category = categoryMap.get(item.category);
  const heading = element("h1", "", item.title);
  heading.tabIndex = -1;
  header.append(
    element("div", "docs-eyebrow", `${category.caption} / ${category.title}`),
    heading,
    element("p", "docs-intro", item.summary),
  );
  const badges = element("div", "docs-badges");
  for (const cap of item.caps.length ? item.caps : ["配置 / 客户端"])
    badges.append(element("span", "docs-badge", cap));
  header.append(badges);
  article.append(header);
  if (item.api.length) {
    const api = section("调用入口");
    const list = element("ul", "docs-api-list");
    for (const [index, signature] of item.api.entries()) {
      const entry = element("li");
      entry.append(
        element("code", "docs-api-signature", signature),
        element("p", "docs-api-description", methodDescriptions[signature.split("(")[0]]),
        methodExample(signature, index === 0),
      );
      list.append(entry);
    }
    api.append(list);
    article.append(api);
  }
  if (item.params.length) {
    const parameters = section("参数与默认值");
    const wrap = element("div", "docs-table-wrap");
    const table = element("table");
    const head = element("thead");
    const headRow = element("tr");
    for (const title of ["参数", "类型", "默认 / 取值", "说明"]) {
      const cell = element("th", "", title);
      cell.scope = "col";
      headRow.append(cell);
    }
    head.append(headRow);
    const body = element("tbody");
    for (const row of item.params) {
      const tr = element("tr");
      for (const cell of row) tr.append(element("td", "", cell));
      body.append(tr);
    }
    table.append(head, body);
    wrap.append(table);
    parameters.append(wrap);
    article.append(parameters);
  }
  const example = section("调用示例");
  example.append(codeBlock(item.code, item.language));
  article.append(example);
  const result = section("返回与生效方式");
  result.append(element("p", "docs-returns", item.returns));
  article.append(result);
  if (item.notes.length) {
    const notes = section("使用说明");
    const list = element("ul", "docs-notes");
    for (const note of item.notes) list.append(element("li", "", note));
    notes.append(list);
    article.append(notes);
  }
  const index = features.indexOf(item);
  const pagination = element("nav", "docs-feature-nav");
  pagination.setAttribute("aria-label", "相邻文档");
  for (const [neighbor, caption] of [
    [features[index - 1], "上一篇"],
    [features[index + 1], "下一篇"],
  ]) {
    const link = element("a");
    link.href = neighbor ? `#${neighbor.id}` : "#overview";
    link.append(
      element("small", "", neighbor ? caption : "返回概览"),
      document.createTextNode(neighbor?.title || "功能地图"),
    );
    pagination.append(link);
  }
  article.append(pagination);
  content.append(article);
}

/** 展示搜索结果，检索词仅按文字显示；不把搜索内容当正则、HTML 或可执行表达式。 */
function renderResults(query) {
  const results = searchFeatures(query);
  const view = element("section", "docs-results");
  const title = element("h1", "", "搜索文档");
  title.tabIndex = -1;
  view.append(title, element("p", "", `“${query}” · 找到 ${results.length} 个功能点`));
  for (const item of results) {
    const link = element("a", "docs-result");
    link.href = `#${item.id}`;
    link.append(
      element("small", "", categoryMap.get(item.category).title),
      element("h2", "", item.title),
      element("p", "", item.summary),
    );
    view.append(link);
  }
  if (!results.length)
    view.append(element("p", "docs-empty", "试试「通知」「events」「Screenshot」或权限名称。"));
  content.append(view);
}

/** 切换窄屏目录，并禁止隐藏侧栏获得键盘焦点。 */
function setMenu(open) {
  document.body.classList.toggle("docs-nav-open", open);
  $("docs-menu").setAttribute("aria-expanded", String(open));
  $("docs-backdrop").hidden = !open;
  $("docs-sidebar").inert = mobile.matches && !open;
}

/** 按哈希定位文档，未知 ID 回退概览；搜索和目录选择都不重载整个页面。 */
function render() {
  const candidate = location.hash.slice(1);
  selected = featureMap.has(candidate) ? candidate : "overview";
  const query = search.value.trim();
  runtimeViews.clear();
  content.replaceChildren();
  renderNavigation(query);
  const item = featureMap.get(selected);
  $("docs-breadcrumb").textContent = query
    ? "开发文档 / 搜索"
    : item
      ? `开发文档 / ${categoryMap.get(item.category).title} / ${item.title}`
      : "开发文档 / 概览";
  document.title = `${query ? "搜索" : item?.title || "开发文档"} · Hybrid`;
  if (query) renderResults(query);
  else if (item) renderFeature(item);
  else renderOverview();
  main.scrollTop = 0;
}

/** 仅尝试一次宿主握手；浏览器预览和无 host 授权时仍可完整阅读静态文档。 */
async function connect() {
  try {
    const info = await Hybrid.connect({ timeoutMs: 1500 });
    nativeReady = (await Hybrid.detectEnvironment()).isDesktopShell;
    $("docs-detach").disabled = false;
    $("docs-environment").textContent =
      `v${info.version} · ${info.capabilities.length} 项已授权能力`;
    $("docs-environment").title =
      `当前窗口授权：${info.capabilities.join(", ")}；未授权的方法运行时会显示权限错误。`;
    await Hybrid.call(() => Host.Ready(), { timeoutMs: 2000 });
  } catch {
    $("docs-environment").textContent = "浏览器预览 · 桌面示例需外壳环境";
  }
}

search.addEventListener("input", render);
window.addEventListener("hashchange", () => {
  search.value = "";
  render();
  setMenu(false);
  content.querySelector("h1")?.focus({ preventScroll: true });
});
// 同一哈希的搜索结果点击也应退出搜索，避免浏览器不派发 hashchange 时页面停留在结果列表。
document.addEventListener("click", (event) => {
  const link = event.target.closest("a[href^='#']");
  if (link && link.hash === location.hash) {
    search.value = "";
    render();
    setMenu(false);
  }
});
$("docs-link").addEventListener("click", () => {
  void copyText(location.href);
});
$("docs-stop-all").addEventListener("click", () => {
  void runner.stopAll();
});
// 只调用固定文档入口，不把当前搜索词、地址或窗口选项交给原生层。
$("docs-detach").addEventListener("click", async () => {
  try {
    await Hybrid.openDocs();
    notify("已打开或聚焦独立文档窗口");
  } catch {
    notify("暂时无法打开独立窗口，请确认原生连接和 host 权限");
  }
});
$("docs-menu").addEventListener("click", () =>
  setMenu(!document.body.classList.contains("docs-nav-open")),
);
$("docs-backdrop").addEventListener("click", () => setMenu(false));
mobile.addEventListener("change", () => setMenu(false));
// 快捷键只接管搜索，不拦截用户在输入框中的普通文字编辑。
document.addEventListener("keydown", (event) => {
  const editing = event.target.matches("input,textarea,[contenteditable='true']");
  if (
    ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k") ||
    (event.key === "/" && !editing)
  ) {
    event.preventDefault();
    setMenu(true);
    search.focus();
  } else if (event.key === "Escape") {
    search.value = "";
    render();
    setMenu(false);
    search.blur();
  }
});
// 离开文档页时丢弃参数草稿，避免编辑过的业务信息继续驻留在本页缓存中。
window.addEventListener(
  "pagehide",
  () => {
    clearTimeout(toastTimer);
    exampleDrafts.clear();
    runtimeViews.clear();
    runner.dispose();
  },
  { once: true },
);
setMenu(false);
render();
void connect();
