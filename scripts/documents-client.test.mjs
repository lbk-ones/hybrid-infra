import test from "node:test";
import assert from "node:assert/strict";
import { createDocuments } from "../frontend/src/documents-client.js";

/** 建立轻量 DOM 测试夹具，禁止 innerHTML 写入，并在测试后恢复全局变量。 */
function documentFixture(t) {
  const previous = {
    document: globalThis.document,
    window: globalThis.window,
    requestAnimationFrame: globalThis.requestAnimationFrame,
  };
  /** 模拟文本节点和父子关系，测试真正使用的 append/remove 与事件清理。 */
  class Node extends EventTarget {
    /** 创建一个空的文本容器，不解析任何 HTML。 */
    constructor(tag) {
      super();
      this.tag = tag;
      this.children = [];
      this.textContent = "";
    }
    /** 将子项附到当前容器，保留父节点供清理验证。 */
    append(...nodes) {
      for (const node of nodes) {
        node.parent = this;
        this.children.push(node);
      }
    }
    /** 从实际父节点移除自身，重复清理安全。 */
    remove() {
      if (this.parent) this.parent.children = this.parent.children.filter((node) => node !== this);
      this.parent = undefined;
    }
    /** 测试中任何使用 HTML 注入的实现都会立即失败。 */
    set innerHTML(_value) {
      throw new Error("HTML injection forbidden");
    }
  }
  const target = new Node("main"),
    body = new Node("body"),
    head = new Node("head");
  globalThis.document = {
    body,
    head,
    fonts: { ready: Promise.resolve() },
    createElement: (tag) => new Node(tag),
    querySelector: () => target,
  };
  globalThis.window = new EventTarget();
  globalThis.requestAnimationFrame = (callback) => queueMicrotask(callback);
  t.after(() => {
    for (const [key, value] of Object.entries(previous)) {
      if (value === undefined) delete globalThis[key];
      else globalThis[key] = value;
    }
  });
  return { target, body, head };
}

// 验证标准 File 按 UTF-8/Base64 调用宿主，不序列化本地路径，超过大小或不支持格式时拒绝。
test("documents readFile validates user File and sends real bytes", async () => {
  const calls = [];
  const client = createDocuments({
    Parse: async (...args) => {
      calls.push(args);
      return { rows: [] };
    },
  });
  const input = "id,名称\n001,示例\n";
  await client.readFile(new File([input], "table.csv"));
  assert.equal(calls[0][0], "csv");
  assert.equal(Buffer.from(calls[0][1], "base64").toString(), input);
  await assert.rejects(client.readFile(new File([input], "old.xls")), /仅支持/);
  await assert.rejects(
    client.readFile(new File([new Uint8Array(512 * 1024 + 1)], "big.csv")),
    /512/,
  );
  assert.equal(calls.length, 1);
});

// 验证报表只写文本，预览不修改打印纸张；解除预览会移除样式和内容。
test("report preview is text only and rejects mismatched rows", (t) => {
  const { target } = documentFixture(t),
    client = createDocuments({});
  const malicious = "<img src=x onerror=alert(1)>";
  const clear = client.preview(target, {
    title: malicious,
    headers: ["备注"],
    rows: [[malicious]],
  });
  assert.equal(target.children[1].children[0].textContent, malicious);
  assert.doesNotMatch(target.children[0].textContent, /@page/);
  assert.throws(() => client.preview(target, { headers: ["a"], rows: [["1", "2"]] }), /等长/);
  clear();
  assert.equal(target.children.length, 0);
});

// 验证本地预览委托宿主处理，返回只表示请求，错误原样传播，不把内容传到外网。
test("print report opens a validated local preview through host", async () => {
  const calls = [];
  const client = createDocuments({
    OpenPrintPreview: async (report) => {
      calls.push(report);
    },
  });
  const report = { headers: ["a"], rows: [["1"]], paper: "Letter", orientation: "landscape" };
  assert.deepEqual(await client.printReport(report), { requested: true, mode: "external-preview" });
  assert.deepEqual(calls, [report]);
  const denied = createDocuments({
    OpenPrintPreview: async () => {
      throw new Error("denied");
    },
  });
  await assert.rejects(denied.printReport(report), /denied/);
});

// 验证拖放只回调用户文件，默认不读取上传，并将错误通知给本页；移除监听后不再回调。
test("file drop callback handles errors and releases listeners", async (t) => {
  const { target } = documentFixture(t);
  const client = createDocuments({});
  let received,
    errors = [];
  const off = client.onDrop(
    target,
    async (files) => {
      received = files;
      throw new Error("business error");
    },
    { onError: (error) => errors.push(error.message) },
  );
  const file = new File(["a\n1"], "a.csv");
  const drop = new Event("drop", { cancelable: true });
  Object.defineProperty(drop, "dataTransfer", { value: { files: [file], types: ["Files"] } });
  target.dispatchEvent(drop);
  await new Promise(setImmediate);
  assert.equal(drop.defaultPrevented, true);
  assert.equal(received[0], file);
  assert.deepEqual(errors, ["business error"]);
  received = undefined;
  off();
  target.dispatchEvent(drop);
  await new Promise(setImmediate);
  assert.equal(received, undefined);
});
