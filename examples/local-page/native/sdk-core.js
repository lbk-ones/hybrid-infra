import { isDesktopShell, detectEnvironment as probeEnvironment } from "./environment.js";
/** 统一的宿主调用错误，保留可供业务判断的错误码和原始错误。 */
export class HybridError extends Error {
  /** 创建错误对象：code 用于程序判断，message 用于说明原因，cause 保留底层错误。 */
  constructor(code, message, cause) {
    super(message, { cause });
    this.name = "HybridError";
    this.code = code;
  }
}

/** 从宿主错误、HTTP 错误或取消异常中提取统一错误码，无法识别时归为通信错误。 */
export function normalizeError(error) {
  if (error instanceof HybridError) return error;
  let value = error;
  for (let depth = 0; value && depth < 5; depth++, value = value.cause) {
    if (typeof value.code === "string" && value.code.startsWith("E_")) {
      return new HybridError(value.code, value.message || value.code, error);
    }
    const text = typeof value === "string" ? value : value.message;
    if (typeof text === "string") {
      // Wails 可能把宿主 JSON 错误包在 HTTP 错误文本中，因此也检查文本中的错误码。
      const code = text.match(/\bE_[A-Z_]+\b/);
      if (code) {
        const message = text.startsWith(`${code[0]}:`)
          ? text.slice(code[0].length + 1).trim()
          : text;
        return new HybridError(code[0], message, error);
      }
    }
  }
  if (error?.name === "CancelError" || error?.name === "AbortError") {
    return new HybridError("E_CANCELLED", "操作已取消", error);
  }
  return new HybridError("E_TRANSPORT", error?.message || String(error), error);
}

/**
 * 执行一次宿主调用，并统一处理超时、AbortSignal 取消及错误格式。
 * operation 负责发起请求；timeoutMs 使用毫秒。取消时尝试停止底层请求，不会撤回已完成的操作。
 */
export function call(operation, { timeoutMs = 30000, signal } = {}) {
  if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) {
    return Promise.reject(new HybridError("E_INVALID_ARGUMENT", "timeoutMs 必须为正数"));
  }
  if (signal?.aborted) return Promise.reject(new HybridError("E_CANCELLED", "操作已取消"));
  return new Promise((resolve, reject) => {
    let pending;
    let timer;
    let settled = false;
    // 只交付一次结果，并移除本次调用的计时器和取消监听，避免重复回调。
    const finish = (fn, value) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      signal?.removeEventListener("abort", abort);
      fn(value);
    };
    // 先结束当前等待，再尝试取消原生请求；取消接口自身失败时不覆盖最初的取消原因。
    const cancel = (code, message) => {
      finish(reject, new HybridError(code, message));
      try {
        Promise.resolve(pending?.cancel?.()).catch(() => {});
      } catch {}
    };
    // 把外部 AbortSignal 转换为 SDK 统一的取消错误。
    const abort = () => cancel("E_CANCELLED", "操作已取消");
    signal?.addEventListener("abort", abort, { once: true });
    timer = setTimeout(() => cancel("E_TIMEOUT", "宿主调用超时"), timeoutMs);
    try {
      pending = operation();
      Promise.resolve(pending).then(
        (value) => finish(resolve, value),
        (error) => finish(reject, normalizeError(error)),
      );
    } catch (error) {
      finish(reject, normalizeError(error));
    }
  });
}

/** 为指定的宿主绑定创建 SDK 客户端，把握手缓存限制在当前客户端实例内。 */
export function createClient(Host) {
  let connection;
  /**
   * 连接宿主并校验协议版本和必需能力；成功后复用握手结果，失败后允许重试。
   * requiredCapabilities 仅用于检查已有授权，不会向宿主申请或扩大权限。
   */
  async function connect({ timeoutMs = 5000, requiredCapabilities = [] } = {}) {
    connection ??= call(() => Host.GetInfo(), { timeoutMs })
      .then((info) => {
        if (info?.protocolVersion !== 1)
          throw new HybridError("E_PROTOCOL_MISMATCH", "宿主与 SDK 协议版本不兼容");
        if (!Array.isArray(info.capabilities))
          throw new HybridError("E_PROTOCOL_MISMATCH", "宿主能力信息无效");
        return info;
      })
      .catch((error) => {
        connection = undefined;
        throw normalizeError(error);
      });
    const info = await connection;
    const missing = requiredCapabilities.filter((name) => !info.capabilities.includes(name));
    if (missing.length)
      throw new HybridError("E_CAPABILITY_UNAVAILABLE", `宿主未授权能力：${missing.join(", ")}`);
    return { ...info, capabilities: [...info.capabilities] };
  }
  /**
   * 先登记退出保护任务，再执行业务函数，并在 finally 中释放任务。
   * options 控制登记请求；业务函数负责自己的取消。释放请求不会沿用已取消的 signal。
   */
  async function withTask(label, operation, options = {}) {
    const task = await call(() => Host.BeginTask(label), options);
    let operationError;
    try {
      return await operation();
    } catch (error) {
      operationError = error;
      throw error;
    } finally {
      // 即使调用方已经取消，也单独尝试释放登记的任务，避免退出保护一直保留。
      try {
        await call(() => Host.EndTask(task.id), { timeoutMs: 5000 });
      } catch (error) {
        if (!operationError) throw error;
      }
    }
  }
  /** 短超时握手判断是否由真正的原生窗口发起；连接失败返回环境状态，不把普通预览页面当成异常流程。 */
  async function detectEnvironment({ timeoutMs = 1500 } = {}) {
    return probeEnvironment(() => call(() => Host.GetEnvironment(), { timeoutMs }));
  }
  /** 打开或聚焦宿主的内置开发文档窗口；固定入口只需要 host 权限，不接受任意 URL。 */
  async function openDocs(options) {
    return call(() => Host.OpenDeveloperDocs(), options);
  }
  return { connect, call, withTask, isDesktopShell, detectEnvironment, openDocs };
}
