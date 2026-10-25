import test from "node:test";
import assert from "node:assert/strict";
import { createDevices } from "../frontend/src/devices-client.js";

/** 将测试字节转成宿主实际返回的 Base64 数据包，不打开真实设备。 */
function packet(data, sequence = 1) {
  return { sequence, data: Buffer.from(data).toString("base64") };
}
/** 等待 SDK 的异步回调完成一轮，不使用没有上限的轮询。 */
function settled() {
  return new Promise(setImmediate);
}

// 验证停止后立即重订阅时等待旧 Read 实际退出，不与宿主读取锁竞争。
test("resubscribing hands off the active native read", async () => {
  let rejectRead,
    calls = 0;
  const values = [],
    errors = [];
  const client = createDevices({
    Read: () => {
      if (++calls === 1)
        return new Promise((_resolve, reject) => {
          rejectRead = reject;
        });
      return Promise.resolve({ packets: [packet("new\n")], state: "closed" });
    },
    CancelRead: async () => {
      rejectRead(Object.assign(new Error("cancelled"), { code: "E_CANCELLED" }));
    },
  });
  const off = client.onData("id", () => assert.fail("removed handler called"), {
    mode: "lines",
    onError: (error) => errors.push(error),
  });
  off();
  client.onData("id", (line) => values.push(line), {
    mode: "lines",
    onError: (error) => errors.push(error),
  });
  await settled();
  assert.deepEqual(values, ["new"]);
  assert.equal(calls, 2);
  assert.deepEqual(errors, []);
  client.dispose();
});

// 验证跨块 UTF-8 和按行分帧，多个订阅共享一条读取流，断开前最后未换行内容也会交付。
test("device callbacks share reads and decode split UTF-8 and final frame", async () => {
  const bytes = new TextEncoder().encode("条码\r\nST,GS,+12.340kg");
  let reads = 0;
  const responses = [
    { packets: [packet(bytes.slice(0, 1))], state: "connected" },
    { packets: [packet(bytes.slice(1), 2)], state: "disconnected" },
  ];
  const client = createDevices({
    Read: async () => {
      reads++;
      return responses.shift();
    },
  });
  const first = [],
    second = [];
  client.onData("one", (line) => first.push(line), { mode: "lines" });
  client.onData("one", (line) => second.push(line), { mode: "lines" });
  await settled();
  assert.deepEqual(first, ["条码", "ST,GS,+12.340kg"]);
  assert.deepEqual(second, first);
  assert.equal(reads, 2);
  client.dispose();
});

// 验证单个回调失败被隔离，过长文本帧明确报错，不传入失去边界的大字符串。
test("callback error does not break peers and long frames fail", async () => {
  const errors = [],
    values = [];
  const client = createDevices({
    Read: async () => ({ packets: [packet("ok\n")], state: "closed" }),
  });
  client.onData(
    "one",
    () => {
      throw new Error("subscriber failed");
    },
    { mode: "lines", onError: (error) => errors.push(error.message) },
  );
  client.onData("one", (line) => values.push(line), { mode: "lines" });
  await settled();
  assert.deepEqual(values, ["ok"]);
  assert.deepEqual(errors, ["subscriber failed"]);
  client.dispose();
  let index = 0;
  const long = createDevices({
    Read: async () => ({
      packets: [packet("x".repeat(4096) + (++index === 5 ? "\n" : ""))],
      state: "connected",
    }),
  });
  long.onData("long", () => assert.fail("oversized frame delivered"), {
    mode: "lines",
    onError: (error) => errors.push(error.message),
  });
  await settled();
  assert.ok(errors.some((message) => message.includes("16 KiB")));
  long.dispose();
});

// 验证编码不会猜测二进制协议，迟到的打开结果在页面卸载后被立即关闭。
test("device write encoding and late connection cleanup", async () => {
  const closed = [];
  let release;
  const client = createDevices({
    OpenSerial: async () => {
      await new Promise((resolve) => {
        release = resolve;
      });
      return { id: "late" };
    },
    Close: async (...args) => closed.push(args),
  });
  assert.equal(Buffer.from(client.encode("条码"), "base64").toString(), "条码");
  assert.deepEqual(
    [...Buffer.from(client.encode("1B 40 00 FF", "hex"), "base64")],
    [27, 64, 0, 255],
  );
  assert.throws(() => client.encode("A", "hex"), /完整字节/);
  const pending = client.OpenSerial({ port: "COM1" });
  client.dispose();
  release();
  await assert.rejects(pending, /页面已关闭/);
  assert.deepEqual(closed, [["late", true]]);
});

// 验证通用文本秤帧的数值、稳定标志和单位，不把未知协议或无状态数字假装成稳定重量。
test("scale parser distinguishes stable unstable overload and unknown protocol", () => {
  const client = createDevices({});
  assert.deepEqual(client.parseWeight("ST,GS,+0012.340kg"), {
    value: 12.34,
    unit: "kg",
    stable: true,
    overload: false,
    raw: "ST,GS,+0012.340kg",
  });
  assert.equal(client.parseWeight("US,NT,-0.200 g").stable, false);
  assert.equal(client.parseWeight("12.34").stable, null);
  assert.equal(client.parseWeight("12.34").unit, "");
  assert.equal(client.parseWeight("OL").overload, true);
  assert.throws(() => client.parseWeight("vendor:data:12.34"), /无法识别/);
  client.dispose();
});

/** 生成只包含普通按键属性的测试事件，不向操作系统注入键盘输入。 */
function key(target, name) {
  const event = new Event("keydown", { cancelable: true });
  Object.assign(event, {
    key: name,
    isComposing: false,
    repeat: false,
    ctrlKey: false,
    metaKey: false,
    altKey: false,
  });
  target.dispatchEvent(event);
  return event;
}
// 验证键盘扫码要求连续输入和终止键，解除监听后不再回调，不依赖真实扫码枪。
test("keyboard scanner framing and listener cleanup", async () => {
  const target = new EventTarget(),
    values = [];
  const client = createDevices({});
  const off = client.onScan(target, (value) => values.push(value));
  for (const character of "ABC123") key(target, character);
  const enter = key(target, "Enter");
  await settled();
  assert.deepEqual(values, ["ABC123"]);
  assert.equal(enter.defaultPrevented, true);
  off();
  for (const character of "OTHER") key(target, character);
  key(target, "Enter");
  await settled();
  assert.equal(values.length, 1);
  client.dispose();
});
