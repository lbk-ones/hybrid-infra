import test from "node:test";
import assert from "node:assert/strict";
import { createExampleRunner } from "../frontend/src/docs-runner.js";

// 真实调用器按类型传参，不把文本当表达式，也不在创建运行器时触发任何 SDK 方法。
test("runner invokes selected SDK method with typed inputs and exposes result or error", async () => {
  const calls = [];
  const runner = createExampleRunner({
    UI: {
      Notify: (...args) => {
        calls.push(args);
        return Promise.resolve({ ok: true });
      },
    },
    Cache: {
      Set: (...args) => {
        calls.push(args);
        return Promise.resolve();
      },
    },
    Desktop: {
      GetSettings: () =>
        Promise.reject(
          Object.assign(new Error("未授权 settings"), { code: "E_PERMISSION_DENIED" }),
        ),
    },
  });
  assert.equal(calls.length, 0);
  const signature = "UI.Notify(text, options)";
  await runner.run(
    signature,
    ['"; globalThis.invalidDocsCall = true;', '{"durationSeconds":8,"draggable":false}'],
    true,
  );
  assert.deepEqual(calls[0], [
    '"; globalThis.invalidDocsCall = true;',
    { durationSeconds: 8, draggable: false },
  ]);
  assert.equal(globalThis.invalidDocsCall, undefined);
  assert.equal(runner.state(signature).status, "done");
  assert.match(runner.state(signature).result, /"ok": true/);
  await runner.run(
    "Cache.Set(namespace, key, value, ttlMs, global)",
    ["demo", "key", '{"n":1}', "123", "false"],
    true,
  );
  assert.deepEqual(calls[1], ["demo", "key", { n: 1 }, 123, false]);
  await runner.run("Desktop.GetSettings()", [], true);
  assert.match(runner.state("Desktop.GetSettings()").error, /E_PERMISSION_DENIED: 未授权 settings/);
  await runner.run(signature, ["x", "{broken"], true);
  assert.equal(calls.length, 2);
  assert.equal(runner.state(signature).status, "error");
  await runner.run(signature, undefined, false);
  assert.match(runner.state(signature).error, /桌面外壳/);
  await assert.rejects(runner.run("UI.Unknown()", [], true), /不支持/);
});

// 用模拟事件总线验证真实回调的参数、日志、返回值和取消函数；回调只在明确发送消息后运行。
test("event examples receive callbacks, return async request results and stop subscriptions", async () => {
  let listener;
  let handler;
  let stopped = 0;
  const sdk = {
    Events: {
      on: async (_topic, callback) => {
        listener = callback;
        return () => {
          stopped++;
          listener = undefined;
        };
      },
      emit: async (_topic, data) => {
        await listener?.(data);
        return listener ? 1 : 0;
      },
      handle: async (_topic, callback) => {
        handler = callback;
        return () => {
          stopped++;
          handler = undefined;
        };
      },
      request: async (_topic, data) => handler(data),
    },
  };
  const runner = createExampleRunner(sdk);
  const on = "Events.on(topic, callback, options)";
  await runner.run(on, ["demo", "(data) => { console.log('收到订单', data.id); }", "{}"], true);
  assert.equal(runner.state(on).status, "active");
  assert.equal(runner.state(on).logs.length, 0);
  await runner.trigger(on, ["demo", "", "{}"]);
  assert.match(runner.state(on).logs.join("\n"), /收到订单 42/);
  await assert.rejects(runner.run(on, undefined, true), /先停止/);
  const handle = "Events.handle(topic, callback, options)";
  await runner.run(handle, ["sum", "async ({a,b}) => ({total:a+b})", "{}"], true);
  await runner.trigger(handle, ["sum", "", "{}"]);
  assert.match(runner.state(handle).logs.join("\n"), /"total": 5/);
  await runner.stopAll();
  assert.equal(stopped, 2);
  assert.equal(listener, undefined);
  assert.equal(handler, undefined);
  assert.equal(runner.state(on).status, "stopped");
});

// 取消时尚未返回的订阅也必须在迟到后释放，避免快速切换或关闭文档留下监听。
test("late subscription results are cleaned after stop and disposal", async () => {
  let resolve;
  let stopped = 0;
  const runner = createExampleRunner({
    Events: {
      on: () =>
        new Promise((done) => {
          resolve = done;
        }),
    },
  });
  const signature = "Events.on(topic, callback, options)";
  const pending = runner.run(signature, ["demo", "() => {}", "{}"], true);
  await runner.stop(signature);
  resolve(() => {
    stopped++;
  });
  await pending;
  assert.equal(stopped, 1);
  assert.equal(runner.state(signature).busy, false);
  assert.equal(runner.state(signature).status, "stopped");
  runner.dispose();
});

// 清理旧监听尚未完成时禁止重复运行，避免旧清理的迟到结果覆盖新一轮状态。
test("a new run waits for asynchronous cleanup to finish", async () => {
  let finishCleanup;
  const signature = "Events.on(topic, callback, options)";
  const runner = createExampleRunner({
    Events: {
      on: async () => () =>
        new Promise((resolve) => {
          finishCleanup = resolve;
        }),
    },
  });
  await runner.run(signature, ["demo", "() => {}", "{}"], true);
  const stopping = runner.stop(signature);
  await assert.rejects(runner.run(signature, undefined, true), /先停止/);
  finishCleanup();
  await stopping;
  assert.equal(runner.state(signature).stopping, false);
  assert.equal(runner.state(signature).status, "stopped");
});

// 关闭事件会话后还需移除本地错误监听，避免界面显示已清理但回调仍保留。
test("closing events also removes the local error observer", async () => {
  let removed = 0;
  const runner = createExampleRunner({
    Events: {
      onError: () => () => {
        removed++;
      },
      close: async () => {},
    },
  });
  await runner.run("Events.onError(callback)", ["() => {}"], true);
  await runner.run("Events.close()", [], true);
  await runner.stopAll();
  assert.equal(removed, 1);
  assert.equal(runner.state("Events.onError(callback)").status, "stopped");
});

// 验证文件凭证和任务可在本页连续使用，手动释放后不会重复自动释放；配置合并真实 saved 快照。
test("resource cleanup, settings merging and variadic calls use real SDK arguments", async () => {
  let releases = 0;
  let config;
  let logged;
  const runner = createExampleRunner({
    Desktop: {
      SelectFile: async () => ({ token: "file-token" }),
      ReleaseFile: async () => {
        releases++;
      },
      GetSettings: async () => ({ saved: { title: "原配置", splashEnabled: true } }),
      SaveSettings: async (value) => {
        config = value;
      },
    },
    Log: {
      Log: (...args) => {
        logged = args;
      },
    },
  });
  await runner.run("Desktop.SelectFile(mode, name)", ["open", ""], true);
  await runner.run("Desktop.ReleaseFile(token)", ["file-token"], true);
  await runner.stopAll();
  assert.equal(releases, 1);
  await runner.run("Desktop.SaveSettings(config)", ['{"splashEnabled":false}'], true);
  assert.deepEqual(config, { title: "原配置", splashEnabled: false });
  await runner.run("Log.Log(level, message, ...args)", ["4", "消息", '["count",3]'], true);
  assert.deepEqual(logged, [4, "消息", "count", 3]);
});

// 命令输出回调交给真实 SDK，停止按钮通过 AbortSignal 请求取消，不把取消伪装成成功返回。
test("shell run streams console output and receives stop signal", async () => {
  const signature = "Shell.run(options, callbacks)";
  const runner = createExampleRunner({
    Shell: {
      run: async (_options, callbacks) => {
        callbacks.onStdout("运行中");
        await new Promise((_resolve, reject) =>
          callbacks.signal.addEventListener("abort", () => reject(new Error("已取消")), {
            once: true,
          }),
        );
      },
    },
  });
  const pending = runner.run(
    signature,
    ['{"program":"go"}', "{onStdout: (text) => console.log(text)}"],
    true,
  );
  assert.match(runner.state(signature).logs.join("\n"), /运行中/);
  await runner.stop(signature);
  await pending;
  assert.equal(runner.state(signature).status, "stopped");
  assert.match(runner.state(signature).error, /已取消/);
});
