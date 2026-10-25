import test from "node:test";
import assert from "node:assert/strict";
import { createTasks } from "../frontend/src/tasks-client.js";

/** 模拟宿主状态机，记录真实参数并让回调仍在 JS 中执行，用于验证客户端取消与收尾。 */
function serviceFixture() {
  const tasks = new Map();
  let serial = 0;
  const service = {
    /** 创建排队记录，不接收函数参数。 */
    async Create(title) {
      const task = { id: String(++serial), title, state: "queued" };
      tasks.set(task.id, task);
      return { ...task };
    },
    /** 首次领取立即进入运行状态，模拟宿主调度完成。 */
    async Poll(id) {
      const task = tasks.get(id);
      if (task.state === "queued") task.state = "running";
      return { ...task };
    },
    /** 记录进度及取消前后的实际状态。 */
    async Report(id, progress, message) {
      Object.assign(tasks.get(id), { progress, message });
      return { ...tasks.get(id) };
    },
    /** 取消保留执行中状态，直到函数自行返回。 */
    async Cancel(id) {
      const task = tasks.get(id);
      task.state = task.state === "queued" ? "cancelled" : "cancelling";
    },
    /** 结束时取消优先，模拟原生统一完成语义。 */
    async Finish(id, state, message) {
      const task = tasks.get(id);
      task.state = task.state === "cancelling" ? "cancelled" : state;
      task.message = message;
      return { ...task };
    },
    /** 原生重试在此模拟中只记录调用，业务重试应走本页保留函数。 */
    async Retry(id) {
      return { id, native: true };
    },
  };
  return { tasks, service };
}

// 验证回调、进度和返回值，失败后的同页重试，以及成功后释放回调引用。
test("business task reports real progress and retries live callback", async () => {
  const { tasks, service } = serviceFixture();
  const client = createTasks(service);
  let count = 0;
  await assert.rejects(
    client.run("demo", async ({ report }) => {
      await report(40, "doing");
      if (++count === 1) throw new Error("retry me");
      return 42;
    }),
    /retry me/,
  );
  assert.equal(tasks.get("1").state, "failed");
  assert.equal(tasks.get("1").progress, 40);
  assert.equal(client.canRetry("1"), true);
  const value = await client.retry("1");
  assert.equal(value.value, 42);
  assert.equal(value.task.state, "completed");
  assert.equal(client.canRetry(value.task.id), false);
  client.dispose();
});

// 验证外部取消只中止协作信号，不会在函数仍执行时提前上报完成。
test("abort waits for cooperative function to settle", async () => {
  const { tasks, service } = serviceFixture();
  const client = createTasks(service);
  const controller = new AbortController();
  let release;
  const started = new Promise((resolve) => {
    release = resolve;
  });
  let finish;
  const promise = client.run(
    "cancel",
    async ({ signal }) => {
      release();
      await new Promise((resolve) => {
        finish = resolve;
      });
      assert.equal(signal.aborted, true);
      return "late";
    },
    { signal: controller.signal },
  );
  await started;
  controller.abort();
  await Promise.resolve();
  assert.equal(tasks.get("1").state, "cancelling");
  finish();
  await assert.rejects(promise, { code: "E_CANCELLED" });
  assert.equal(tasks.get("1").state, "cancelled");
  client.dispose();
});

// 验证页面在异步创建返回前卸载，也会取消迟到任务且不会执行函数或保留重试引用。
test("dispose cancels a late Create response", async () => {
  const { tasks, service } = serviceFixture();
  const create = service.Create;
  let resolve;
  service.Create = async (title) => {
    await new Promise((done) => {
      resolve = done;
    });
    return create(title);
  };
  const client = createTasks(service);
  let called = false;
  const pending = client.run("late", () => {
    called = true;
  });
  client.dispose();
  resolve();
  await assert.rejects(pending, { code: "E_CANCELLED" });
  assert.equal(called, false);
  assert.equal(tasks.get("1").state, "cancelled");
  assert.equal(client.canRetry("1"), false);
});

// 验证进度订阅抛错不会遗留运行任务，预先取消也不会在宿主创建新记录。
test("progress callback error and pre-aborted signal clean up", async () => {
  const { tasks, service } = serviceFixture();
  const client = createTasks(service);
  await assert.rejects(
    client.run("bad", () => 1, {
      onProgress: () => {
        throw new Error("callback error");
      },
    }),
    /callback error/,
  );
  assert.equal(tasks.get("1").state, "failed");
  const controller = new AbortController();
  controller.abort();
  await assert.rejects(
    client.run("never", () => 1, { signal: controller.signal }),
    { code: "E_CANCELLED" },
  );
  assert.equal(tasks.size, 1);
  client.dispose();
});
