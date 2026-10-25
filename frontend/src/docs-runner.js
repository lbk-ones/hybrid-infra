import { features } from "./docs-content.js";
import { getMethodExample, readMethodArguments } from "./docs-examples.js";

const allowed = new Set(features.flatMap((item) => item.api));
const browserMethods = new Set([
  "Hybrid.isDesktopShell",
  "Hybrid.detectEnvironment",
  "normalizeError",
]);

/** 将结果与错误转成可读文本，兼容循环引用、函数、BigInt 和 undefined，并限制输出大小防止日志占满页面。 */
export function formatExampleResult(value) {
  if (typeof value === "string") return value.slice(0, 16000);
  if (value === undefined) return "undefined（调用已完成，无返回值）";
  if (typeof value === "function") return "取消函数已保存，可点击「停止 / 清理」调用";
  const seen = new WeakSet();
  return (
    JSON.stringify(
      value,
      (_key, item) => {
        if (typeof item === "bigint") return String(item);
        if (typeof item === "function") return "[函数]";
        if (item instanceof Error)
          return { name: item.name, code: item.code, message: item.message };
        if (item && typeof item === "object") {
          if (seen.has(item)) return "[重复引用]";
          seen.add(item);
        }
        return item;
      },
      2,
    ) ?? String(value)
  ).slice(0, 16000);
}

/** 创建文档专用真实调用器；仅开放已列出的 SDK 入口，事件监听跨目录保留，停止或离页时释放本页示例资源。 */
export function createExampleRunner(sdk, onChange = () => {}) {
  const states = new Map();
  let disposed = false;

  /** 获取一个方法的运行记录，和 DOM 分离，切换目录后仍能查看回调与返回结果。 */
  function state(signature) {
    if (!states.has(signature))
      states.set(signature, { status: "idle", logs: [], result: "", error: "", busy: false });
    return states.get(signature);
  }

  /** 通知界面刷新当前运行状态；已卸载的页面不再触碰控件。 */
  function changed() {
    if (!disposed) onChange();
  }

  /** 向对应方法追加回调或命令输出，按行数和字符数截断，只保留本页内存中的最近记录。 */
  function log(signature, label, values) {
    if (disposed) return;
    const entry = state(signature);
    entry.logs.push(`${label} ${values.map(formatExampleResult).join(" ")}`.slice(0, 4000));
    entry.logs = entry.logs.slice(-40);
    changed();
  }

  /** 停止当前调用等待并释放其监听、文件授权、受保护任务或后台命令；结束结果迟到时同样清理。 */
  async function stop(signature) {
    const entry = state(signature);
    if (entry.stopping) return;
    entry.stopping = true;
    entry.cancelled = true;
    entry.controller?.abort();
    try {
      entry.pending?.cancel?.();
      const cleanup = entry.cleanup;
      entry.cleanup = undefined;
      if (cleanup) await cleanup();
      if (!entry.busy) entry.status = "stopped";
    } catch (error) {
      entry.error = `清理失败：${error.message || error}`;
      entry.status = "error";
    } finally {
      entry.stopping = false;
      changed();
    }
  }

  /** 按用户输入执行真实 SDK 方法；回调模板在点击时编译，console 输出转到本示例，不会执行生成的整段代码。 */
  async function run(signature, values, native = false) {
    if (!allowed.has(signature)) throw new Error("不支持的示例方法");
    const name = signature.split("(")[0];
    const entry = state(signature);
    if (entry.busy || entry.cleanup || entry.stopping) throw new Error("请先停止 / 清理上一次示例");
    entry.error = "";
    entry.result = "";
    entry.logs = [];
    entry.cancelled = false;
    entry.controller = new AbortController();
    entry.busy = true;
    entry.status = "running";
    changed();
    try {
      if (disposed) throw new Error("示例页面已关闭");
      if (!native && !browserMethods.has(name))
        throw new Error("请在桌面外壳中打开文档运行此示例，浏览器预览没有原生能力");
      const console = Object.fromEntries(
        ["log", "info", "warn", "error", "debug"].map((level) => [
          level,
          (...args) => log(signature, level, args),
        ]),
      );
      // 函数模板是用户主动编辑并运行的 JS，和业务代码拥有同样的页面权限，不声称是隔离沙箱。
      const compile = (source) =>
        new Function(...Object.keys(sdk), "console", `"use strict"; return (${source}\n);`)(
          ...Object.values(sdk),
          console,
        );
      const args = readMethodArguments(signature, values, compile);
      if (name === "Desktop.SaveSettings") {
        const snapshot = await sdk.Desktop.GetSettings();
        args[0] = { ...snapshot.saved, ...args[0] };
      }
      if (name === "Shell.run") args[1] = { ...args[1], signal: entry.controller.signal };
      if (["Events.request", "Hybrid.call", "Hybrid.withTask"].includes(name))
        args[args.length - 1] = { ...args.at(-1), signal: entry.controller.signal };
      // 回调的参数和返回值都可见，即使用户没写 console.log，也能观察消息与异步返回。
      if (
        name.startsWith("Events.") &&
        ["on", "once", "handle", "onError"].includes(name.split(".")[1])
      ) {
        const index = name === "Events.onError" ? 0 : 1;
        const callback = args[index];
        if (typeof callback !== "function") throw new Error("callback 必须是函数");
        args[index] = async (...data) => {
          log(signature, "收到回调", data);
          try {
            const value = await callback(...data);
            if (name === "Events.handle") log(signature, "回调返回", [value]);
            return value;
          } catch (error) {
            log(signature, "回调失败", [error]);
            throw error;
          }
        };
      }
      const parts = name.split(".");
      const owner = parts.length === 1 ? sdk : sdk[parts[0]];
      if (entry.cancelled || disposed) throw new Error("示例已停止");
      entry.pending = owner[parts.at(-1)](...args);
      const result = await entry.pending;
      entry.result =
        name === "Desktop.Credential"
          ? "凭据调用成功；秘密值不在结果面板中显示"
          : formatExampleResult(result);
      if (typeof result === "function") entry.cleanup = result;
      if (["Desktop.SelectFile", "Desktop.OpenChild"].includes(name) && result?.token)
        entry.cleanup = () => sdk.Desktop.ReleaseFile(result.token);
      if (name === "Host.BeginTask" && result?.id)
        entry.cleanup = () => sdk.Host.EndTask(result.id);
      if (name === "Shell.start" && result?.id) entry.cleanup = () => sdk.Shell.cancel(result.id);
      if (name === "Desktop.SetShortcut" && args[3])
        entry.cleanup = () => sdk.Desktop.SetShortcut(args[0], "", args[2], false);
      // 手动释放资源后同步清掉对应的自动清理登记，避免切换目录后仍显示已结束的任务。
      if (["Desktop.ReleaseFile", "Host.EndTask", "Shell.cancel", "Events.close"].includes(name)) {
        for (const [key, other] of states) {
          const related =
            name === "Events.close"
              ? key.startsWith("Events.") && Boolean(other.cleanup)
              : name === "Desktop.ReleaseFile"
                ? key.startsWith("Desktop.SelectFile(") || key.startsWith("Desktop.OpenChild(")
                : name === "Host.EndTask"
                  ? key.startsWith("Host.BeginTask(")
                  : key.startsWith("Shell.start(");
          if (related && (name === "Events.close" || other.resourceId === args[0])) {
            // SDK close 关闭消息会话，但 onError 是本地独立监听，需显式调用其取消函数。
            if (name === "Events.close" && key.startsWith("Events.onError(")) await other.cleanup();
            other.cleanup = undefined;
            other.status = "stopped";
          }
        }
      }
      entry.resourceId = result?.token || result?.id;
      if (entry.cancelled || disposed) {
        await entry.cleanup?.();
        entry.cleanup = undefined;
        entry.status = "stopped";
      } else entry.status = entry.cleanup ? "active" : "done";
      return result;
    } catch (error) {
      entry.status = entry.cancelled ? "stopped" : "error";
      entry.error = `${error.code || error.name || "E_EXAMPLE"}: ${error.message || error}`;
    } finally {
      entry.busy = false;
      entry.pending = undefined;
      changed();
    }
  }

  /** 触发监听示例所用的真实事件或请求，方便在同一页直接验证回调，不需要另外打开控制台。 */
  async function trigger(signature, values) {
    const name = signature.split("(")[0];
    const params = getMethodExample(signature).params;
    const specTopic = values?.[0] ?? params[0].value;
    const options = JSON.parse(values?.[2] ?? params[2].value);
    const result =
      name === "Events.handle"
        ? await sdk.Events.request(specTopic, { a: 2, b: 3 }, { ...options, timeoutMs: 5000 })
        : await sdk.Events.emit(specTopic, { id: 42, text: "来自文档的测试消息" }, options);
    log(signature, name === "Events.handle" ? "请求返回" : "发送完成", [result]);
  }

  /** 停止本页全部示例，逐项清理，某一项失败不影响其他资源释放。 */
  async function stopAll() {
    return Promise.allSettled(
      [...states.keys()]
        .filter((key) => {
          const entry = states.get(key);
          return entry.busy || entry.cleanup;
        })
        .map(stop),
    );
  }

  /** 离页时尽力停止正在运行和已注册的示例；原生窗口关闭后的宿主归属清理继续兜底。 */
  function dispose() {
    disposed = true;
    void stopAll();
  }

  return { state, run, stop, trigger, stopAll, dispose, states };
}
