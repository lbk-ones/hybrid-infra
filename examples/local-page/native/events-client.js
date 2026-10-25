import { HybridError, normalizeError } from "./sdk-core.js";

/** 将事件数据复制成纯 JSON，明确拒绝函数、BigInt、Symbol 和循环引用，回调函数只能作为订阅参数保留在前端。 */
function jsonValue(value) {
  try {
    return JSON.parse(
      JSON.stringify(value ?? null, (_key, item) => {
        if (["function", "symbol", "bigint"].includes(typeof item))
          throw new TypeError("事件数据不能包含函数、Symbol 或 BigInt");
        return item;
      }),
    );
  } catch {
    throw new HybridError(
      "E_INVALID_ARGUMENT",
      "消息数据必须可转换为 JSON，不能包含函数或循环引用",
    );
  }
}

/** 把来源空间选项转换成宿主授权参数；跨来源空间必须显式选择 app，不能以任意来源字符串冒充其他页面。 */
function scopeOptions(options = {}) {
  if (options.scope !== undefined && !["origin", "app"].includes(options.scope))
    throw new HybridError("E_INVALID_ARGUMENT", "scope 必须是 origin 或 app");
  return { global: options.scope === "app", target: options.targetWindow || "" };
}

/** 创建页面级事件客户端，单一长轮询复用所有回调；每次订阅返回可独立取消的函数，刷新后需重新订阅。 */
export function createEvents(service, invoke) {
  let opening;
  let session;
  let generation = 0;
  let pumping = false;
  let controller;
  let subscribing = 0;
  const listeners = new Map();
  const errorHandlers = new Set();

  /** 把回调异常和后台通信错误交给业务错误处理器；没有处理器时只记录错误码，避免自动打印业务数据。 */
  function report(error) {
    const value = normalizeError(error);
    if (!errorHandlers.size) {
      console.error("Hybrid.Events", value.code);
      return;
    }
    for (const handler of errorHandlers) {
      try {
        Promise.resolve(handler(value)).catch(() => {});
      } catch {}
    }
  }

  /** 合并并发打开会话的请求；close 期间迟到的会话立即释放，不让旧异步结果复活已经关闭的订阅。 */
  async function ensureSession() {
    if (!opening) {
      const epoch = generation;
      opening = invoke(() => service.Open())
        .then(async (id) => {
          if (epoch !== generation) {
            await invoke(() => service.Close(id));
            throw new HybridError("E_CANCELLED", "事件会话已关闭");
          }
          session = id;
          return id;
        })
        .catch((error) => {
          if (epoch === generation) opening = undefined;
          throw error;
        });
    }
    return opening;
  }

  /** 移除本地函数后再撤销宿主监听，防止已经取回的消息在取消订阅后继续触发回调。 */
  async function unsubscribe(id, current) {
    listeners.delete(id);
    if (!listeners.size) controller?.abort();
    if (session === current) {
      try {
        await invoke(() => service.Unsubscribe(current, id));
      } catch (error) {
        // 多个组件同时取消时，最后一个监听可能已经关闭会话；此时把迟到的取消视为幂等成功。
        if (!(session !== current && normalizeError(error).code === "E_SESSION_EXPIRED"))
          throw error;
      }
      if (session === current && !listeners.size && !subscribing) await close();
    }
  }

  /** 分发单条回调；普通事件不等待处理结果，请求处理器会把 Promise 的返回值或失败状态发送回宿主。 */
  function dispatch(message, current) {
    const listener = listeners.get(message.listenerId);
    if (!listener) return;
    if (listener.once) unsubscribe(message.listenerId, current).catch(report);
    const meta = {
      topic: message.topic,
      sender: message.sender,
      requestId: message.requestId,
      kind: message.kind,
    };
    const result = Promise.resolve().then(() => listener.callback(message.data, meta));
    if (message.kind === "request") {
      result
        .then(jsonValue)
        .then(
          (value) =>
            invoke(() =>
              service.Respond(current, message.listenerId, message.requestId, value, false),
            ),
          (error) => {
            report(error);
            return invoke(() =>
              service.Respond(current, message.listenerId, message.requestId, null, true),
            );
          },
        )
        .catch(report);
    } else result.catch(report);
  }

  /** 使用一条可取消的长轮询持续接收批量消息；临时网络失败退避重试，会话失效时明确报告并要求业务重新订阅。 */
  async function pump(current) {
    if (pumping) return;
    pumping = true;
    let delay = 250;
    try {
      while (session === current && listeners.size) {
        controller = new AbortController();
        try {
          const messages = await invoke(() => service.Poll(current), {
            timeoutMs: 20000,
            signal: controller.signal,
          });
          delay = 250;
          if (session === current) for (const message of messages) dispatch(message, current);
        } catch (error) {
          if (session !== current || !listeners.size) break;
          const value = normalizeError(error);
          if (
            ["E_SESSION_EXPIRED", "E_PERMISSION_DENIED", "E_SHUTTING_DOWN"].includes(value.code)
          ) {
            listeners.clear();
            session = undefined;
            opening = undefined;
            generation++;
            report(value);
            break;
          }
          report(value);
          await new Promise((resolve) => setTimeout(resolve, delay));
          delay = Math.min(delay * 2, 4000);
        }
      }
    } finally {
      pumping = false;
      if (session && listeners.size) void pump(session);
    }
  }

  /** 订阅前检查函数类型和空间选项，宿主登记完成后再启动接收，避免先监听后打开通道导致消息空窗。 */
  async function subscribe(topic, callback, options, kind, once) {
    if (typeof callback !== "function")
      throw new HybridError("E_INVALID_ARGUMENT", "监听回调必须是函数");
    const { global } = scopeOptions(options);
    subscribing++;
    try {
      const current = await ensureSession();
      const id = await invoke(() => service.Subscribe(current, topic, kind, global));
      if (session !== current) throw new HybridError("E_CANCELLED", "事件会话已关闭");
      listeners.set(id, { callback, once });
      void pump(current);
      return () => unsubscribe(id, current);
    } finally {
      subscribing--;
    }
  }

  /** 注册普通事件回调，返回 Promise<取消函数>；同主题可有多个独立监听。 */
  async function on(topic, callback, options = {}) {
    return subscribe(topic, callback, options, "event", false);
  }
  /** 注册最多执行一次的普通事件回调，在执行前移除本地监听以避免同批次重复触发。 */
  async function once(topic, callback, options = {}) {
    return subscribe(topic, callback, options, "event", true);
  }
  /** 注册请求处理器，允许返回普通 JSON 或 Promise<JSON>；多个匹配处理器时请求方必须指定唯一目标。 */
  async function handle(topic, callback, options = {}) {
    return subscribe(topic, callback, options, "handler", false);
  }
  /** 发送异步通知并返回匹配监听数；不会等待回调执行，也不会收集回调返回值。 */
  async function emit(topic, data, options = {}) {
    const { global, target } = scopeOptions(options);
    const value = jsonValue(data);
    return invoke(() => service.Emit(topic, value, target, global), { signal: options.signal });
  }
  /** 发送请求并等待唯一处理器返回 JSON，支持超时和 AbortSignal，回调函数不跨进程序列化。 */
  async function request(topic, data, options = {}) {
    const { global, target } = scopeOptions(options);
    const timeoutMs = options.timeoutMs ?? 10000;
    if (!Number.isInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > 30000)
      throw new HybridError("E_INVALID_ARGUMENT", "请求超时必须是 1–30000 毫秒");
    const value = jsonValue(data);
    return invoke(() => service.Request(topic, value, target, global, timeoutMs), {
      timeoutMs: timeoutMs + 1000,
      signal: options.signal,
    });
  }
  /** 关闭当前会话并清空本页监听；后续再次调用 on/handle 会创建新会话。 */
  async function close() {
    const current = session;
    generation++;
    session = undefined;
    opening = undefined;
    listeners.clear();
    controller?.abort();
    if (current) await invoke(() => service.Close(current));
  }
  /** 登记后台错误回调并返回本地取消函数，不向宿主传递该函数。 */
  function onError(callback) {
    if (typeof callback !== "function")
      throw new HybridError("E_INVALID_ARGUMENT", "错误回调必须是函数");
    errorHandlers.add(callback);
    return () => errorHandlers.delete(callback);
  }
  return { on, once, handle, emit, request, close, onError };
}
