import { Desktop, Host, Hybrid, normalizeError } from "./host-client.js";

/** 按固定页面 ID 获取控件，不拼接外部选择器。 */
const $ = (id) => document.getElementById(id);
const notice = $("notice");
let capabilities = new Set();
let selected;
let busy = false;
let polling = false;
let downloadTimer;
let downloadSnapshot;

/** 将宿主返回的 Base64 字符串转换为字节；文件内容只按纯文本或 PNG 使用，不插入 HTML。 */
function fromBase64(value) {
  return Uint8Array.from(atob(value), (character) => character.charCodeAt(0));
}

/** 分块将二进制编码为 Base64，避免一次展开整个缓冲区导致调用栈溢出。 */
function toBase64(bytes) {
  let result = "";
  for (let index = 0; index < bytes.length; index += 8192) {
    result += String.fromCharCode(...bytes.subarray(index, index + 8192));
  }
  return btoa(result);
}

/** 将有单位的下载大小用于进度展示，未知总量时保持明确的未知状态。 */
function size(bytes) {
  if (bytes < 0) return "未知大小";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MiB`;
}

/** 根据真实授权禁用整块区域；忙碌期间只禁用操作按钮，避免重复点击创建后台任务。 */
function updateControls() {
  for (const section of document.querySelectorAll("[data-cap]")) {
    const allowed = capabilities.has(section.dataset.cap);
    section.classList.toggle("unavailable", !allowed);
    section.querySelector(".cap-state").textContent = allowed
      ? "已授权"
      : `未授权 · ${section.dataset.cap}`;
    for (const field of section.querySelectorAll("button,input,textarea,select")) {
      field.disabled = !allowed || (busy && field.tagName === "BUTTON");
    }
  }
}

/** 以统一提示执行用户操作；错误保留稳定错误码，成功提示不展示秘密、完整下载地址或文件内容。 */
async function run(operation, success = "操作完成") {
  if (busy) return;
  busy = true;
  updateControls();
  try {
    await operation();
    notice.className = "notice";
    notice.textContent = success;
  } catch (error) {
    const value = normalizeError(error);
    notice.className = "notice error";
    notice.textContent = `${value.code} · ${value.message}`;
  } finally {
    busy = false;
    updateControls();
  }
}

/** 绑定固定操作按钮，所有异步回调都交给 run 管理错误和重复提交。 */
function bind(id, operation, success) {
  $(id).addEventListener("click", () => run(operation, success));
}

/** 选择新的文件凭证后释放旧凭证；下载使用独立保存凭证，不受这里的文件切换影响。 */
async function choose(mode) {
  const next = await Hybrid.call(() => Desktop.SelectFile(mode, "export.txt"), {
    timeoutMs: 300000,
  });
  if (selected) await Hybrid.call(() => Desktop.ReleaseFile(selected.token));
  selected = next;
  $("file-info").textContent =
    `${next.directory ? "目录" : next.writable ? "可写文件" : "只读文件"} · ${next.path}`;
}

/** 取得当前文件凭证；未选择时直接显示可操作的提示，而不发出无意义 RPC。 */
function selectedToken() {
  if (!selected) throw new Error("E_INVALID_ARGUMENT: 请先选择文件或保存位置");
  return selected.token;
}

/** 读取并重绘本窗口的下载列表；轮询不覆盖用户操作提示，也不同时发起多次请求。 */
async function refreshDownloads() {
  if (polling || !capabilities.has("downloads")) return;
  polling = true;
  try {
    const downloads = await Hybrid.call(() => Desktop.ListDownloads());
    const serialized = JSON.stringify(downloads);
    if (serialized === downloadSnapshot) return;
    downloadSnapshot = serialized;
    const list = $("downloads");
    list.replaceChildren();
    if (!downloads.length) {
      const empty = document.createElement("p");
      empty.className = "panel-description";
      empty.textContent = "暂无下载任务";
      list.append(empty);
    }
    for (const item of downloads) {
      const row = document.createElement("div");
      row.className = "download-row";
      const info = document.createElement("div");
      const title = document.createElement("strong");
      title.textContent = item.name;
      const status = document.createElement("small");
      const label = { running: "下载中", completed: "已完成", cancelled: "已取消", failed: "失败" }[
        item.state
      ];
      status.textContent = `${label} · ${size(item.received)} / ${size(item.total)}${item.error ? ` · ${item.error.code}` : ""}`;
      const progress = document.createElement("progress");
      progress.max = Math.max(item.total, 1);
      if (item.total >= 0) progress.value = item.received;
      progress.setAttribute("aria-label", `${item.name} 下载进度`);
      info.append(title, status, progress);
      const actions = document.createElement("div");
      actions.className = "actions";
      const operations =
        item.state === "running"
          ? [["取消", () => Desktop.CancelDownload(item.id)]]
          : item.state === "completed"
            ? [
                ["打开文件", () => Desktop.OpenDownload(item.id, false)],
                ["显示文件", () => Desktop.OpenDownload(item.id, true)],
                ["清理记录", () => Desktop.RemoveDownload(item.id)],
              ]
            : [
                ["重试", () => Desktop.RetryDownload(item.id)],
                ["清理记录", () => Desktop.RemoveDownload(item.id)],
              ];
      // 每个按钮只携带宿主返回的任务编号，不把地址或文件名拼接成脚本。
      for (const [text, operation] of operations) {
        const button = document.createElement("button");
        button.textContent = text;
        button.addEventListener("click", () =>
          run(async () => {
            await Hybrid.call(operation);
            await refreshDownloads();
          }),
        );
        actions.append(button);
      }
      row.append(info, actions);
      list.append(row);
    }
    updateControls();
  } finally {
    polling = false;
  }
}

/** 应用偏好到演示页面并提示启动配置是否待重启；业务前端应自行订阅或读取偏好后渲染语言和主题。 */
function renderSettings(snapshot) {
  const p = snapshot.preferences;
  $("theme").value = p.theme;
  $("locale").value = p.locale;
  $("notifications-enabled").checked = p.notifications;
  document.querySelector(".desktop-main").dataset.theme = p.theme;
  $("startup-settings").value = JSON.stringify(snapshot.saved, null, 2);
  $("settings-state").textContent = snapshot.restartRequired
    ? "启动配置已保存，重启应用后生效。"
    : "启动配置与当前运行一致；界面偏好保存后立即生效。";
}

/** 显示剪贴板 PNG 预览；只设置经过 PNG 校验的图片源，不执行文件内容。 */
function previewImage(base64) {
  const image = $("image-preview");
  image.src = `data:image/png;base64,${base64}`;
  image.hidden = false;
}

// 文件操作均使用原生选择返回的 token，后续页面操作不接受自由输入磁盘路径。
bind("file-open", () => choose("open"), "已选择文件");
bind("file-directory", () => choose("directory"), "已选择目录");
bind("file-save", () => choose("save"), "已授权保存位置");
bind(
  "file-reveal",
  () => Hybrid.call(() => Desktop.RevealFile(selectedToken())),
  "已请求系统定位文件",
);
bind("file-release", async () => {
  await Hybrid.call(() => Desktop.ReleaseFile(selectedToken()));
  selected = undefined;
  $("file-info").textContent = "文件凭证已释放";
});
bind(
  "file-read",
  async () => {
    if (selected?.directory) {
      const entries = await Hybrid.call(() => Desktop.ListDirectory(selectedToken()));
      $("file-content").value = entries
        .map(
          (item) =>
            `${item.directory ? "[目录]" : "[文件]"} ${item.name}${item.directory ? "" : ` · ${size(item.size)}`}`,
        )
        .join("\n");
      return;
    }
    const data = await Hybrid.call(() => Desktop.ReadFile(selectedToken()));
    $("file-content").value = new TextDecoder("utf-8", { fatal: true }).decode(fromBase64(data));
  },
  "已读取 UTF-8 文本",
);
bind(
  "file-write",
  () =>
    Hybrid.call(() =>
      Desktop.WriteFile(
        selectedToken(),
        toBase64(new TextEncoder().encode($("file-content").value)),
      ),
    ),
  "已保存文本",
);

// 下载先明确选择覆盖目标，再启动后台传输；独立凭证保留到任务记录清理，避免文件工作区操作撤销下载目标。
bind(
  "download-start",
  async () => {
    if (!capabilities.has("files"))
      throw new Error("E_PERMISSION_DENIED: 下载目标选择还需要 files 权限");
    const raw = $("download-url").value.trim();
    const url = new URL(raw);
    if (!["http:", "https:"].includes(url.protocol) || url.username || url.password)
      throw new Error("E_INVALID_ARGUMENT: 请输入 HTTP(S) 下载地址");
    const target = await Hybrid.call(() => Desktop.SelectFile("save", "download.bin"), {
      timeoutMs: 300000,
    });
    try {
      await Hybrid.call(() => Desktop.StartDownload(raw, target.token));
    } catch (error) {
      await Hybrid.call(() => Desktop.ReleaseFile(target.token));
      throw error;
    }
    await refreshDownloads();
  },
  "下载已开始，可在下方查看进度",
);
bind("download-refresh", refreshDownloads, "下载列表已刷新");

bind(
  "clipboard-read",
  async () => {
    $("clipboard-text").value = await Hybrid.call(() => Desktop.ClipboardText("read", ""));
  },
  "已读取剪贴板文本",
);
bind(
  "clipboard-write",
  () => Hybrid.call(() => Desktop.ClipboardText("write", $("clipboard-text").value)),
  "已复制文本",
);
bind(
  "clipboard-clear",
  async () => {
    await Hybrid.call(() => Desktop.ClipboardText("clear", ""));
    $("clipboard-text").value = "";
    $("image-preview").hidden = true;
  },
  "剪贴板已清空",
);
bind(
  "image-write",
  async () => {
    const file = $("clipboard-image").files[0];
    if (!file || file.size > 512 * 1024)
      throw new Error("E_TOO_LARGE: 请选择不超过 512 KiB 的 PNG");
    const data = toBase64(new Uint8Array(await file.arrayBuffer()));
    await Hybrid.call(() => Desktop.ClipboardImage(data, true));
    previewImage(data);
  },
  "图像已复制",
);
bind(
  "image-read",
  async () => {
    previewImage(await Hybrid.call(() => Desktop.ClipboardImage("", false)));
  },
  "已读取剪贴板图像",
);
bind(
  "notify",
  () =>
    Hybrid.call(() =>
      Desktop.Notify(
        $("notification-title").value,
        $("notification-body").value,
        $("notification-route").value,
      ),
    ),
  "已提交系统通知，显示取决于系统设置",
);
bind(
  "shortcut-register",
  () =>
    Hybrid.call(() =>
      Desktop.SetShortcut(
        $("shortcut-key").value,
        $("shortcut-route").value,
        $("shortcut-global").checked,
        true,
      ),
    ),
  "快捷键已注册，本次运行期间有效",
);
bind(
  "shortcut-remove",
  () =>
    Hybrid.call(() =>
      Desktop.SetShortcut($("shortcut-key").value, "", $("shortcut-global").checked, false),
    ),
  "快捷键已注销",
);
bind(
  "protocol-register",
  () => Hybrid.call(() => Desktop.ConfigureProtocol(true)),
  "当前用户的应用协议已注册",
);
bind(
  "protocol-remove",
  () => Hybrid.call(() => Desktop.ConfigureProtocol(false)),
  "当前安装的应用协议已移除",
);

// 偏好即时保存，完整启动配置明确展示重启状态；读取失败时不自动覆盖磁盘配置。
bind(
  "settings-save",
  async () => {
    await Hybrid.call(() =>
      Desktop.SavePreferences({
        theme: $("theme").value,
        locale: $("locale").value,
        notifications: $("notifications-enabled").checked,
      }),
    );
    renderSettings(await Hybrid.call(() => Desktop.GetSettings()));
  },
  "界面偏好已保存",
);
bind(
  "settings-reset",
  async () => {
    await Hybrid.call(() => Desktop.ResetPreferences());
    renderSettings(await Hybrid.call(() => Desktop.GetSettings()));
  },
  "界面偏好已恢复默认",
);
bind(
  "startup-save",
  async () => {
    renderSettings(
      await Hybrid.call(() => Desktop.SaveSettings(JSON.parse($("startup-settings").value))),
    );
  },
  "启动配置已校验并保存，请查看生效状态",
);

// 图标使用已保存的固定地址；用户编辑 JSON 后须先保存，再显式重新加载，其他启动配置不热更新。
bind(
  "icons-reload",
  async () => {
    renderSettings(await Hybrid.call(() => Desktop.ReloadIcons(), { timeoutMs: 30000 }));
  },
  "图标已重新加载；其他启动设置仍按页面提示生效",
);

// 秘密只进入密码框和凭据 RPC，不写入提示、console、下载记录或普通设置；写入和删除后清空密码框。
bind(
  "credential-save",
  async () => {
    await Hybrid.call(() =>
      Desktop.Credential($("credential-key").value, $("credential-value").value, "set"),
    );
    $("credential-value").value = "";
    $("credential-state").textContent = "系统凭据已保存";
  },
  "系统凭据已保存",
);
bind(
  "credential-read",
  async () => {
    $("credential-value").value = await Hybrid.call(() =>
      Desktop.Credential($("credential-key").value, "", "get"),
    );
    $("credential-state").textContent = "已读取到密码框，内容保持隐藏";
  },
  "已读取系统凭据",
);
bind(
  "credential-delete",
  async () => {
    await Hybrid.call(() => Desktop.Credential($("credential-key").value, "", "delete"));
    $("credential-value").value = "";
    $("credential-state").textContent = "系统凭据已删除";
  },
  "系统凭据已删除",
);

/** 建立真实宿主连接、显示能力授权并读取初始状态；不在普通浏览器中展示模拟成功数据。 */
async function initialize() {
  const info = await Hybrid.connect();
  capabilities = new Set(info.capabilities);
  $("capabilities").replaceChildren();
  for (const cap of capabilities) {
    const badge = document.createElement("span");
    badge.textContent = cap;
    $("capabilities").append(badge);
  }
  await Hybrid.call(() => Host.Ready());
  if (capabilities.has("settings")) renderSettings(await Hybrid.call(() => Desktop.GetSettings()));
  await refreshDownloads();
  // 轮询只更新下载快照；页面离开立即停止，隐藏时跳过，避免后台窗口持续刷表。
  downloadTimer = window.setInterval(() => {
    if (!document.hidden)
      refreshDownloads().catch((error) => {
        const value = normalizeError(error);
        notice.textContent = `${value.code} · 无法刷新下载列表`;
      });
  }, 1000);
}

updateControls();
window.addEventListener("pagehide", () => {
  clearInterval(downloadTimer);
  $("credential-value").value = "";
});
run(initialize, "已连接桌面宿主；未授权能力请按示例配置开启");
