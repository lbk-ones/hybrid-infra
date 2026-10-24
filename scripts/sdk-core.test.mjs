import assert from "node:assert/strict";
import test from "node:test";
import {
  call,
  createClient,
  HybridError,
  normalizeError,
} from "../frontend/src/sdk-core.js";

test("handshake checks protocol and capabilities, retries after failure", async () => {
  let attempts = 0;
  const client = createClient({
    async GetInfo() {
      if (++attempts === 1) throw new Error("offline");
      return { protocolVersion: 1, capabilities: ["host", "windows"] };
    },
  });
  await assert.rejects(client.connect(), { code: "E_TRANSPORT" });
  const info = await client.connect({ requiredCapabilities: ["windows"] });
  info.capabilities.push("store");
  assert.deepEqual((await client.connect()).capabilities, ["host", "windows"]);
  await assert.rejects(client.connect({ requiredCapabilities: ["store"] }), {
    code: "E_CAPABILITY_UNAVAILABLE",
  });
  assert.equal(attempts, 2);
  await assert.rejects(
    createClient({
      async GetInfo() {
        return { protocolVersion: 2 };
      },
    }).connect(),
    { code: "E_PROTOCOL_MISMATCH" },
  );
});

test("timeout cancels the underlying native call and a late result is handled", async () => {
  let cancelled = 0;
  let resolve;
  const pending = new Promise((done) => {
    resolve = done;
  });
  pending.cancel = () => {
    cancelled++;
  };
  await assert.rejects(
    call(() => pending, { timeoutMs: 10 }),
    { code: "E_TIMEOUT" },
  );
  assert.equal(cancelled, 1);
  resolve("late");
});

test("AbortSignal cancels an active call and does not start an already aborted call", async () => {
  const controller = new AbortController();
  let cancelled = 0;
  const pending = new Promise(() => {});
  pending.cancel = () => {
    cancelled++;
  };
  const result = call(() => pending, { signal: controller.signal });
  controller.abort();
  await assert.rejects(result, { code: "E_CANCELLED" });
  assert.equal(cancelled, 1);
  let started = false;
  await assert.rejects(
    call(
      () => {
        started = true;
      },
      { signal: controller.signal },
    ),
    { code: "E_CANCELLED" },
  );
  assert.equal(started, false);
});

test("task registration is released on success, failure, and caller cancellation", async () => {
  const released = [];
  const client = createClient({
    async BeginTask() {
      return { id: "task-1" };
    },
    async EndTask(id) {
      released.push(id);
    },
  });
  assert.equal(await client.withTask("work", async () => 42), 42);
  const original = new Error("business failure");
  await assert.rejects(
    client.withTask("work", async () => {
      throw original;
    }),
    (error) => error === original,
  );
  const controller = new AbortController();
  await assert.rejects(
    client.withTask(
      "work",
      async () => {
        controller.abort();
        throw original;
      },
      { signal: controller.signal },
    ),
    (error) => error === original,
  );
  assert.deepEqual(released, ["task-1", "task-1", "task-1"]);
});

test("structured host errors and HTTP permission errors keep their error codes", () => {
  assert.equal(
    normalizeError({ cause: { code: "E_BUSY", message: "unfinished" } }).code,
    "E_BUSY",
  );
  assert.equal(
    normalizeError(new Error('HTTP 403: {"code":"E_PERMISSION_DENIED"}')).code,
    "E_PERMISSION_DENIED",
  );
  assert.equal(normalizeError({ name: "CancelError" }).code, "E_CANCELLED");
  const error = new HybridError("E_BUSY", "busy");
  assert.equal(normalizeError(error), error);
});
