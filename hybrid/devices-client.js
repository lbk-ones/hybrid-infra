import { normalizeError } from "./sdk-core.js";
/** 创建设备 SDK；二进制在宿主传输，数据、扫码和电子秤解析回调只在当前页面执行。 */
export function createDevices(service) {
  const owned = new Set(),
    hubs = new Map(),
    scanners = new Set();
  let disposed = false;

  /** 将用户文本或十六进制转换为 Base64；UTF-8 不代表设备一定支持中文，需要按厂商协议选择编码。 */
  function encode(text, format = "text") {
    let bytes;
    if (format === "text") bytes = new TextEncoder().encode(String(text));
    else if (format === "hex") {
      const value = String(text).replace(/\s/g, "");
      if (!value || value.length % 2 || !/^[0-9a-f]+$/i.test(value))
        throw new TypeError("十六进制应为完整字节，例如 1B 40");
      bytes = Uint8Array.from(value.match(/../g), (part) => parseInt(part, 16));
    } else throw new TypeError("format 仅支持 text 或 hex");
    if (bytes.length > 65536) throw new RangeError("每次发送最多 64 KiB");
    let binary = "";
    for (let i = 0; i < bytes.length; i += 32768)
      binary += String.fromCharCode(...bytes.subarray(i, i + 32768));
    return btoa(binary);
  }
  /** 打开连接并记住归属，页面在异步打开期间卸载时立即关闭迟到连接。 */
  async function open(method, options) {
    if (disposed) throw new Error("设备客户端已清理，请刷新页面");
    const connection = await service[method](options);
    if (disposed) {
      await service.Close(connection.id, true).catch(() => {});
      throw new Error("页面已关闭，设备连接已释放");
    }
    owned.add(connection.id);
    return connection;
  }
  /** 打开串口，默认 9600、8N1，DTR/RTS 默认关闭；实际设备参数由业务显式传入。 */
  function OpenSerial(options) {
    return open("OpenSerial", options);
  }
  /** 打开启动配置白名单中的 TCP 地址，不自动发送标签、查询重量或扫描指令。 */
  function OpenTCP(endpoint) {
    return open("OpenTCP", endpoint);
  }
  /** 关闭连接并结束本页接收，默认移除记录；不会撤回已经发送到设备的字节。 */
  async function Close(id, remove = true) {
    const hub = hubs.get(id);
    if (hub) {
      hub.listeners.clear();
      hub.cancelled = true;
      hub.pending?.cancel?.();
    }
    await service.Close(id, remove);
    if (remove) owned.delete(id);
  }
  /** 按 UTF-8 发送文本；不能当作所有打印机或电子秤的通用设备命令。 */
  function writeText(id, text) {
    return service.Write(id, encode(text));
  }
  /** 发送精确十六进制字节，供二进制协议或业务自行编码后的设备指令使用。 */
  function writeHex(id, hex) {
    return service.Write(id, encode(hex, "hex"));
  }

  /** 创建一个订阅的解码状态；lines 模式跨数据包拼接，防止 UTF-8 字符或行边界恰好分包时乱码。 */
  function listener(callback, options) {
    const mode = options.mode || "bytes";
    if (!["bytes", "text", "lines"].includes(mode))
      throw new TypeError("mode 应为 bytes、text 或 lines");
    const decoder =
      mode === "bytes" ? null : new TextDecoder(options.encoding || "utf-8", { fatal: true });
    let buffer = "";
    const delimiter = options.delimiter ?? "\n";
    if (typeof delimiter !== "string" || !delimiter.length || delimiter.length > 16)
      throw new TypeError("分隔符需要 1–16 个字符");
    return {
      onError: options.onError || ((error) => console.error("设备接收失败", error)),
      /** 分发一次真实字节；行回调一次接收一行，终止符去除，默认 LF 同时去掉尾部 CR。 */
      async receive(bytes) {
        if (mode === "bytes") {
          await callback(bytes);
          return;
        }
        const text = decoder.decode(bytes, { stream: true });
        if (mode === "text") {
          if (text) await callback(text);
          return;
        }
        buffer += text;
        let index;
        while ((index = buffer.indexOf(delimiter)) >= 0) {
          if (index > 16384) throw new RangeError("设备帧超过 16 KiB，请检查分隔符或协议");
          let line = buffer.slice(0, index);
          buffer = buffer.slice(index + delimiter.length);
          if (delimiter === "\n" && line.endsWith("\r")) line = line.slice(0, -1);
          await callback(line);
        }
        if (buffer.length > 16384) throw new RangeError("设备帧超过 16 KiB，请检查分隔符或协议");
      },
      /** 连接结束时交付无终止符的最后一段，并检查被截断的字符编码；不静默吞掉最后的设备数据。 */
      async finish() {
        if (mode === "bytes") return;
        const tail = decoder.decode();
        if (mode === "text") {
          if (tail) await callback(tail);
        } else {
          buffer += tail;
          if (buffer) await callback(buffer);
          buffer = "";
        }
      },
    };
  }

  /** 保护错误回调，单个业务回调失败不会打断其他订阅者；错误不会写入宿主设备日志。 */
  function reportError(value, error) {
    try {
      value.onError(error);
    } catch {}
  }

  /** 每个连接只进行一个宿主长轮询，多个回调共享接收流，回调函数不通过 RPC 发送。 */
  async function pump(id, hub) {
    try {
      while (hub.listeners.size && !disposed) {
        let value;
        try {
          hub.pending = service.Read(id, 5000);
          value = await hub.pending;
        } catch (error) {
          if (hub.cancelled) {
            hub.cancelled = false;
            if (hub.listeners.size) continue;
            break;
          }
          for (const subscriber of hub.listeners) reportError(subscriber, normalizeError(error));
          break;
        } finally {
          hub.pending = null;
        }
        for (const packet of value.packets) {
          const binary = atob(packet.data);
          const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
          for (const subscriber of [...hub.listeners]) {
            try {
              await subscriber.receive(bytes.slice());
            } catch (error) {
              reportError(subscriber, error);
              hub.listeners.delete(subscriber);
            }
          }
        }
        if (value.state !== "connected") {
          for (const subscriber of hub.listeners) {
            try {
              await subscriber.finish();
            } catch (error) {
              reportError(subscriber, error);
            }
            if (value.error)
              reportError(
                subscriber,
                Object.assign(new Error(value.error.message), { code: value.error.code }),
              );
          }
          break;
        }
      }
    } finally {
      hub.listeners.clear();
      if (hubs.get(id) === hub) hubs.delete(id);
    }
  }

  /** 通过宿主显式停止旧接收，等旧 Read 真实返回再重启轮询，避免仅取消 Promise 时底层仍占用读取权。 */
  function interruptRead(id, hub) {
    hub.cancelled = true;
    if (service.CancelRead) void service.CancelRead(id).catch(() => hub.pending?.cancel?.());
    else hub.pending?.cancel?.();
  }

  /** 监听指定连接的原始字节、流式文本或按行数据，返回解除监听函数；停监听不等于关闭硬件。 */
  function onData(id, callback, options = {}) {
    if (disposed || typeof callback !== "function")
      throw new TypeError("设备客户端已清理或回调无效");
    let hub = hubs.get(id);
    const subscriber = listener(callback, options);
    if (!hub) {
      hub = { listeners: new Set(), pending: null, cancelled: false };
      hubs.set(id, hub);
      hub.listeners.add(subscriber);
      void pump(id, hub);
    } else hub.listeners.add(subscriber);
    return () => {
      hub.listeners.delete(subscriber);
      if (!hub.listeners.size) {
        interruptRead(id, hub);
      }
    };
  }

  /** 监听 USB 键盘模式扫码枪；只在指定区域捕获连续快速按键加终止键，不读取 USB HID 报告或全局键盘。 */
  function onScan(
    target,
    callback,
    {
      gapMs = 50,
      minLength = 3,
      maxLength = 256,
      terminator = "Enter",
      prefix = "",
      preventDefault = true,
    } = {},
  ) {
    const element = typeof target === "string" ? document.querySelector(target) : target;
    if (
      disposed ||
      !element?.addEventListener ||
      typeof callback !== "function" ||
      !Number.isFinite(gapMs) ||
      gapMs < 1 ||
      gapMs > 1000 ||
      !Number.isInteger(minLength) ||
      !Number.isInteger(maxLength) ||
      minLength < 1 ||
      maxLength < minLength ||
      maxLength > 4096
    )
      throw new TypeError("扫码目标、回调或长度间隔参数无效");
    let buffer = "",
      last = 0;
    /** 用按键间隔区分扫码和普通慢输入；只拦截确认识别后的终止键，快捷键和输入法组合不参与扫码。 */
    const keydown = (event) => {
      if (event.isComposing || event.repeat || event.ctrlKey || event.metaKey || event.altKey) {
        buffer = "";
        return;
      }
      const now = performance.now();
      if (now - last > gapMs) buffer = "";
      last = now;
      if (event.key === terminator) {
        const value = buffer;
        buffer = "";
        if (value.length >= minLength && value.startsWith(prefix)) {
          if (preventDefault) event.preventDefault();
          void Promise.resolve()
            .then(() => callback(value.slice(prefix.length)))
            .catch((error) => console.error("扫码回调失败", error));
        }
        return;
      }
      if (event.key?.length === 1) {
        buffer += event.key;
        if (buffer.length > maxLength) buffer = "";
      } else buffer = "";
    };
    element.addEventListener("keydown", keydown);
    const off = () => {
      element.removeEventListener("keydown", keydown);
      scanners.delete(off);
      buffer = "";
    };
    scanners.add(off);
    return off;
  }

  /** 解析常见文本秤帧，如 ST,GS,+0012.340kg；未知二进制或厂商协议须自行解析，不默认判定稳定。 */
  function parseWeight(text, { defaultUnit = "" } = {}) {
    const raw = String(text).trim();
    if (raw.length > 256) throw new RangeError("秤帧最多 256 个字符");
    if (!["", "kg", "g", "lb", "t"].includes(defaultUnit))
      throw new TypeError("默认单位应为 kg/g/lb/t 或空");
    if (raw === "OL") return { value: null, unit: defaultUnit, stable: false, overload: true, raw };
    const match =
      /^\s*(?:(ST|US|OL)[,\s]+)?(?:(?:GS|NT)[,\s]+)?([+-]?\d+(?:\.\d+)?)\s*(kg|g|lb|t)?\s*$/i.exec(
        raw,
      );
    if (!match) throw new TypeError("无法识别该文本秤帧，请按设备协议解析");
    const value = Number(match[2]);
    if (!Number.isFinite(value)) throw new RangeError("重量数值无效");
    const state = match[1]?.toUpperCase();
    return {
      value,
      unit: match[3]?.toLowerCase() || defaultUnit,
      stable: state ? state === "ST" : null,
      overload: state === "OL",
      raw,
    };
  }

  /** 页面卸载时尽力取消轮询并关闭本页创建的设备，窗口清理与一分钟租约继续兜底。 */
  function dispose() {
    disposed = true;
    for (const off of [...scanners]) off();
    for (const hub of hubs.values()) {
      hub.listeners.clear();
      hub.cancelled = true;
      hub.pending?.cancel?.();
    }
    for (const id of owned) void service.Close(id, true).catch(() => {});
    owned.clear();
  }
  return {
    ...service,
    OpenSerial,
    OpenTCP,
    Close,
    encode,
    writeText,
    writeHex,
    onData,
    onScan,
    parseWeight,
    dispose,
  };
}
