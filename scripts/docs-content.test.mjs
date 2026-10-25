import test from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import {
  categories,
  features,
  methodDescriptions,
  permissionCatalog,
  permissionSnippets,
  searchFeatures,
} from "../frontend/src/docs-content.js";
import * as SDK from "../hybrid/client.js";
import { buildMethodExample, getMethodExample } from "../frontend/src/docs-examples.js";

// 对照 Go 权限真值和默认授权，确保一键复制的数组完整、无重复，内置配置不会重复添加基础权限。
test("permission catalog and copy snippets cover all native capabilities", () => {
  const policy = readFileSync(new URL("../go/host/policy.go", import.meta.url), "utf8");
  const native = JSON.parse(`[${policy.match(/var Capabilities = \[\]string\{([^}]+)\}/)[1]}]`);
  const defaults = JSON.parse(
    `[${policy.match(/var defaultCapabilities = \[\]string\{([^}]+)\}/)[1]}]`,
  );
  const names = permissionCatalog.map((permission) => permission.name);
  assert.deepEqual(names, native);
  assert.equal(new Set(names).size, names.length);
  assert.deepEqual(
    permissionCatalog
      .filter((permission) => permission.defaultGranted)
      .map((permission) => permission.name),
    defaults,
  );
  for (const permission of permissionCatalog)
    assert.match(permission.description, /[\u4e00-\u9fff]/);
  assert.deepEqual(JSON.parse(permissionSnippets.all), native);
  const local = JSON.parse(permissionSnippets.local).desktopCapabilities;
  assert.deepEqual(
    local,
    native.filter((name) => !defaults.includes(name)),
  );
});

// 检查文档分类、深链接、参数表及示例完整，防止新条目出现重复路由或空白详情。
test("developer docs have unique categories and complete feature entries", () => {
  assert.equal(categories.length, 6);
  assert.ok(features.length >= 30);
  assert.equal(new Set(features.map((item) => item.id)).size, features.length);
  for (const item of features) {
    assert.ok(categories.some((category) => category.id === item.category));
    assert.match(item.id, /^[a-z0-9-]+$/);
    assert.ok(item.title && item.summary && item.code && item.returns);
    assert.ok(Array.isArray(item.caps));
    for (const row of item.params) assert.equal(row.length, 4);
  }
});

// 将签名中的方法名与真正导出的 SDK 对照，避免文档列出无法调用的接口。
test("documented API entry points exist in the generated SDK", () => {
  for (const item of features) {
    for (const signature of item.api) {
      const name = signature.split("(")[0];
      assert.equal(typeof methodDescriptions[name], "string", `${name}: 缺少方法介绍`);
      assert.match(methodDescriptions[name], /[\u4e00-\u9fff]/, `${name}: 需要中文介绍`);
      const parts = name.split(".");
      let target = SDK;
      for (const part of parts) target = target?.[part];
      assert.equal(typeof target, "function", `${item.id}: ${name}`);
    }
  }
});

// 只做语法检查，不运行示例，避免测试触发命令、截图、系统设置或其他桌面副作用。
test("all JavaScript and JSON examples are syntactically valid", () => {
  for (const item of features) {
    if (item.language === "json") {
      assert.doesNotThrow(() => JSON.parse(item.code), item.id);
    } else if (item.language === "javascript") {
      const check = spawnSync(process.execPath, ["--input-type=module", "--check"], {
        input: item.code,
        encoding: "utf8",
      });
      assert.equal(check.status, 0, `${item.id}: ${check.stderr}`);
    }
  }
});

// 检索保持大小写不敏感，多个关键词同时匹配，不将用户输入解释为表达式或 HTML。
test("docs search matches names, APIs and capabilities safely", () => {
  assert.equal(searchFeatures("  ").length, features.length);
  assert.ok(searchFeatures("screenshot").some((item) => item.id === "screenshot"));
  assert.equal(searchFeatures("截图")[0].id, "screenshot");
  assert.ok(searchFeatures("EVENTS handle").some((item) => item.id === "event-requests"));
  assert.ok(searchFeatures("通知").some((item) => item.id === "notify"));
  assert.ok(searchFeatures("傻等").some((item) => item.id === "calls"));
  assert.deepEqual(searchFeatures("<img onerror=unknown>"), []);
});

// 检查每一个入口都有可生成的示例，并按签名顺序提供真实参数；只检查语法，不执行宿主调用。
test("each API has editable arguments and a syntactically valid standalone example", () => {
  const signatures = new Set(features.flatMap((item) => item.api));
  for (const signature of signatures) {
    const spec = getMethodExample(signature);
    const expected = signature
      .slice(signature.indexOf("(") + 1, -1)
      .split(",")
      .map((name) => name.trim())
      .filter(Boolean);
    assert.deepEqual(
      spec.params.map((param) => param.name),
      expected,
      signature,
    );
    for (const field of spec.params)
      assert.ok(field.hint && typeof field.value === "string", signature);
    const check = spawnSync(process.execPath, ["--input-type=module", "--check"], {
      input: buildMethodExample(signature),
      encoding: "utf8",
    });
    assert.equal(check.status, 0, `${signature}: ${check.stderr}`);
  }
});

// 验证修改参数后的代码保留准确类型，拒绝无效 JSON；字符串与函数模板均不会在生成时执行。
test("example generator validates inputs and only produces code text", () => {
  const text = '保存成功\n"引号" </code><script>globalThis.docsExecuted = true</script>';
  const notify = buildMethodExample("UI.Notify(text, options)", [
    text,
    '{"durationSeconds":8,"draggable":false}',
  ]);
  assert.ok(notify.includes(JSON.stringify(text)));
  assert.ok(notify.includes('"durationSeconds": 8'));
  assert.ok(notify.includes('"draggable": false'));
  assert.throws(
    () => buildMethodExample("UI.Notify(text, options)", ["你好", "{ broken"]),
    /有效的 JSON/,
  );
  assert.throws(() => buildMethodExample("UI.Notify(text, options)", ["你好", "[]"]), /JSON 对象/);
  assert.throws(() => buildMethodExample("Host.Retry(home)", ["yes"]), /true 或 false/);
  assert.throws(() => buildMethodExample("Log.SetLogLevel(level)", [""]), /有效数字/);
  assert.throws(() => buildMethodExample("Log.SetLogLevel(level)", ["Infinity"]), /有效数字/);
  assert.throws(
    () => buildMethodExample("Log.Log(level, message, ...args)", ["0", "日志", "{}"]),
    /JSON 数组/,
  );
  const log = buildMethodExample("Log.Log(level, message, ...args)", [
    "4",
    "业务消息",
    '["count", 3]',
  ]);
  assert.ok(log.includes("...["));
  assert.ok(log.includes("  4,"));
  const callback = "() => { globalThis.docsExecuted = true; }";
  assert.ok(
    buildMethodExample("Events.on(topic, callback, options)", ["test", callback, "{}"]).includes(
      callback,
    ),
  );
  assert.equal(globalThis.docsExecuted, undefined);
  assert.match(
    buildMethodExample("Desktop.SaveSettings(config)", ['{"splashEnabled":true}']),
    /const \{ saved \} = await Desktop.GetSettings\(\)/,
  );
  assert.match(buildMethodExample("Desktop.Credential(key, value, operation)"), /不打印到日志/);
  assert.doesNotMatch(
    buildMethodExample("Desktop.Credential(key, value, operation)"),
    /console\.log/,
  );
});
