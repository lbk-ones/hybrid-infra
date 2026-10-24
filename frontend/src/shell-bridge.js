import {Cache, Events, Hybrid, normalizeError, Shell} from "./host-client.js";

/** 从固定 ID 获取本页控件，不把外部输入作为选择器。 */
const $ = (id) => document.getElementById(id);
let caps = new Set();
let command;
let revision = 0;
let revisionKey;
let working = false;
let initializing = true;
let unsubscribes = [];

/** 根据实际权限与命令运行状态禁用控件，命令运行期间仍允许操作缓存和事件。 */
function updateControls() {
    for (const panel of document.querySelectorAll("[data-bridge-cap]")) {
        const allowed = caps.has(panel.dataset.bridgeCap);
        panel.querySelector(".cap-state").textContent = allowed ? "已授权" : `未授权 · ${panel.dataset.bridgeCap}`;
        for (const field of panel.querySelectorAll("input,button,textarea,select")) field.disabled = !allowed || initializing || (working && field.tagName === "BUTTON");
    }
    $("command-run").disabled = !caps.has("shell") || Boolean(command) || initializing;
    $("command-cancel").disabled = !command;
    $("command-script").disabled = !caps.has("shell.script");
    $("cache-global").disabled = !caps.has("cache.global");
    $("event-global").disabled = !caps.has("events.global");
}

/** 显示统一错误信息，不把命令参数或缓存内容自动写入系统日志。 */
function showError(error) {
    const value = normalizeError(error);
    $("bridge-notice").textContent = `${value.code} · ${value.message}`;
    $("bridge-notice").className = "notice error desktop-wide";
}

/** 串行执行短操作，明确恢复按钮，后台命令由独立 AbortController 管理。 */
async function run(operation) {
    if (working) return;
    working = true;
    updateControls();
    try {
        await operation();
        $("bridge-notice").textContent = "操作完成";
        $("bridge-notice").className = "desktop-wide";
    } catch (error) {
        showError(error);
    } finally {
        working = false;
        updateControls();
    }
}

/** 将按钮绑定到统一异步操作入口，避免未处理的 Promise 异常。 */
function bind(id, operation) {
    $(id).addEventListener("click", () => run(operation));
}

/** 读取缓存表单的空间参数，默认使用同来源空间，显式勾选才请求全应用共享。 */
function cacheOptions() {
    return [$("cache-namespace").value, $("cache-key").value, $("cache-global").checked];
}

/** 读取事件路由选项；目标和空间仍由宿主重新验证权限。 */
function eventOptions() {
    return {targetWindow: $("event-target").value.trim(), scope: $("event-global").checked ? "app" : "origin"};
}

/** 以纯文本追加有界回调记录，避免长期演示导致页面内容无限增长。 */
function eventLog(text) {
    $("event-output").value = `${new Date().toLocaleTimeString()} · ${text}\n${$("event-output").value}`.slice(0, 12000);
}

// 读写缓存使用完整命名空间；CAS 记录的修订号只适用于上一次读取的同一个键和共享空间。
bind("cache-set", async () => {
    const [namespace, key, global] = cacheOptions();
    const value = await Hybrid.call(() => Cache.Set(namespace, key, JSON.parse($("cache-value").value), Number($("cache-ttl").value) * 1000, global));
    revision = value.revision;
    revisionKey = JSON.stringify(cacheOptions());
    $("cache-state").textContent = `已写入 · 修订号 ${revision}`;
});
bind("cache-get", async () => {
    const [namespace, key, global] = cacheOptions();
    const value = await Hybrid.call(() => Cache.Get(namespace, key, global));
    revision = value.revision;
    revisionKey = JSON.stringify(cacheOptions());
    if (value.found) $("cache-value").value = JSON.stringify(value.value, null, 2);
    $("cache-state").textContent = value.found ? `命中 · 修订号 ${revision} · ${value.expiresAt ? new Date(value.expiresAt).toLocaleTimeString() + " 过期" : "进程内不过期"}` : "未命中或已过期";
});
bind("cache-cas", async () => {
    if (revisionKey !== JSON.stringify(cacheOptions())) throw new Error("E_INVALID_ARGUMENT: 请先读取当前空间和键的修订号");
    const [namespace, key, global] = cacheOptions();
    const value = await Hybrid.call(() => Cache.CompareAndSet(namespace, key, JSON.parse($("cache-value").value), Number($("cache-ttl").value) * 1000, global, revision));
    revision = value.revision;
    $("cache-state").textContent = `原子更新成功 · 修订号 ${revision}`;
});
bind("cache-delete", async () => {
    const [namespace, key, global] = cacheOptions();
    await Hybrid.call(() => Cache.Delete(namespace, key, global));
    revisionKey = undefined;
    $("cache-state").textContent = "此键已删除";
});

// 事件回调和响应器均是真正的前端函数，Go 只传送 JSON，回调返回值通过 request/handle 关联返回。
bind("event-on", async () => {
    unsubscribes.push(await Events.on($("event-topic").value, (data, meta) => eventLog(`事件回调 · ${meta.sender || "浏览器"} · ${JSON.stringify(data)}`), eventOptions()));
    eventLog("事件订阅已建立");
});
bind("event-emit", async () => {
    const count = await Events.emit($("event-topic").value, JSON.parse($("event-value").value), eventOptions());
    eventLog(`发送成功 · 匹配 ${count} 个监听`);
});
bind("event-handle", async () => {
    unsubscribes.push(await Events.handle($("event-topic").value, async (data, meta) => {
        eventLog(`请求回调 · ${meta.sender || "浏览器"}`);
        return {echo: data, handledAt: new Date().toISOString()};
    }, eventOptions()));
    eventLog("请求响应器已建立");
});
bind("event-request", async () => {
    const result = await Events.request($("event-topic").value, JSON.parse($("event-value").value), eventOptions());
    eventLog(`请求返回 · ${JSON.stringify(result)}`);
});
bind("event-off", async () => {
    const active = unsubscribes;
    unsubscribes = [];
    await Promise.all(active.map((off) => off()));
    eventLog("本页监听已取消");
});
Events.onError(showError);

// 长命令不占用短操作锁，取消按钮始终可用；输出回调只是追加文本，不执行输出中的脚本。
$("command-run").addEventListener("click", async () => {
    if (command) return;
    command = new AbortController();
    updateControls();
    $("command-output").value = "";
    $("command-state").textContent = "执行中…";
    try {
        const result = await Shell.run({
            mode: $("command-mode").value,
            program: $("command-program").value,
            args: JSON.parse($("command-args").value),
            script: $("command-script").value,
            timeoutMs: Number($("command-timeout").value)
        }, {
            signal: command.signal, onStdout: (text) => {
                $("command-output").value += text;
            }, onStderr: (text) => {
                $("command-output").value += text;
            }
        });
        $("command-state").textContent = `${result.state} · 退出码 ${result.exitCode}${result.truncated ? " · 输出已截断" : ""}`;
    } catch (error) {
        showError(error);
        $("command-state").textContent = "命令未完成，请查看提示";
    } finally {
        command = undefined;
        updateControls();
    }
});
$("command-cancel").addEventListener("click", () => command?.abort());

/** 先探测宿主身份，再读取已授权能力，普通浏览器显示连接状态，不使用模拟成功结果。 */
async function initialize() {
    const env = await Hybrid.detectEnvironment();
    $("bridge-environment").textContent = `${env.kind} · ${env.connected ? "宿主已验证" : "未连接宿主"} · 同步桥接检测 ${Hybrid.isDesktopShell()}`;
    const info = await Hybrid.connect();
    caps = new Set(info.capabilities);
    for (const cap of caps) {
        const badge = document.createElement("span");
        badge.textContent = cap;
        $("bridge-capabilities").append(badge);
    }
    $("bridge-notice").textContent = "已连接 · 新能力需按示例配置显式授权";
}

updateControls();
initialize().catch(showError).finally(() => {
    initializing = false;
    updateControls();
});
