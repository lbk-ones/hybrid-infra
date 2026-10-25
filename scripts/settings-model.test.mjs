import test from "node:test";
import assert from "node:assert/strict";
import {
  mergeSettingsDraft,
  changedSettingsFields,
  settingsLines,
  applySettingsPatch,
} from "../frontend/src/settings-model.js";

// 构造完整可视化输入，不调用宿主或改动用户配置。
function fixture() {
  return {
    title: "业务应用",
    pageMode: "local",
    url: "/index.html",
    localRoot: ".",
    closeBehavior: "exit",
    externalLinks: "browser",
    width: "1000",
    height: "800",
    minWidth: "800",
    minHeight: "600",
    loadTimeoutSeconds: "30",
    splashEnabled: true,
    singleInstance: true,
    trayEnabled: true,
    rememberWindows: true,
    frameless: false,
    debug: false,
    allowedOrigins: "https://example.com\nhttps://example.com",
    navigationOrigins: "",
    shellPrograms: "go\ngit",
    iconApplication: "",
    iconTray: "",
    desktopCapabilities: ["settings", "files"],
  };
}

// 验证启动模式可视化编辑、高级 JSON 校验，以及旧配置缺少字段时继续使用自动模式。
test("settings window startup modes and legacy defaults", () => {
  for (const windowState of ["auto", "normal", "maximized", "fullscreen"]) {
    const config = mergeSettingsDraft({ appId: "test" }, { ...fixture(), windowState }, []);
    assert.equal(config.windowState, windowState);
    assert.equal(
      applySettingsPatch(config, JSON.stringify({ windowState }), "test").windowState,
      windowState,
    );
  }
  const legacy = mergeSettingsDraft({ appId: "test" }, fixture(), []);
  assert.equal(legacy.windowState, "auto");
  delete legacy.windowState;
  assert.equal(applySettingsPatch(legacy, "{}", "test").windowState, "auto");
  assert.throws(() => applySettingsPatch(legacy, '{"windowState":"wrong"}', "test"), /windowState/);
  assert.throws(
    () => mergeSettingsDraft({}, { ...fixture(), windowState: "wrong" }, []),
    /windowState/,
  );
});

// 验证设备网络白名单可编辑、去重和明确清空；旧表单未包含该字段时不丢失高级配置。
test("settings preserve and edit device endpoint allowlist", () => {
  const base = { appId: "device-app", deviceEndpoints: ["192.168.1.10:9100"] };
  assert.deepEqual(mergeSettingsDraft(base, fixture(), []).deviceEndpoints, base.deviceEndpoints);
  assert.deepEqual(
    mergeSettingsDraft(
      base,
      { ...fixture(), deviceEndpoints: "127.0.0.1:9100\n127.0.0.1:9100\n192.168.1.20:4001" },
      [],
    ).deviceEndpoints,
    ["127.0.0.1:9100", "192.168.1.20:4001"],
  );
  assert.deepEqual(
    mergeSettingsDraft(base, { ...fixture(), deviceEndpoints: "" }, []).deviceEndpoints,
    [],
  );
});

// 验证可视化保存保留身份和隐藏配置，权限覆盖中的空列表不会被误认为默认授权。
test("settings form preserves identity and advanced fields and explicit denials", () => {
  const base = { appId: "erp-a", updates: { enabled: true, channel: "stable" } };
  const result = mergeSettingsDraft(base, fixture(), [
    { origin: "https://app.example.com/", capabilities: [] },
  ]);
  assert.equal(result.appId, "erp-a");
  assert.deepEqual(result.updates, base.updates);
  assert.deepEqual(result.permissions["https://app.example.com"], []);
  assert.deepEqual(result.allowedOrigins, ["https://example.com"]);
  assert.equal(result.width, 1000);
  assert.equal(base.title, undefined);
  assert.deepEqual(settingsLines("go\r\ngit\n go \n"), ["go", "git"]);
  assert.deepEqual(changedSettingsFields({ title: "a", width: 1 }, { title: "b", width: 1 }), [
    "title",
  ]);
});

// 无效来源、重复来源和无效数字阻止提交；切换出 local 模式会清理不适用的目录字段。
test("settings form rejects malformed inputs and normalizes page mode fields", () => {
  assert.throws(
    () =>
      mergeSettingsDraft({}, fixture(), [{ origin: "https://example.com/path", capabilities: [] }]),
    /权限来源无效/,
  );
  assert.throws(
    () =>
      mergeSettingsDraft({}, fixture(), [
        { origin: "https://example.com", capabilities: [] },
        { origin: "https://example.com/", capabilities: [] },
      ]),
    /重复/,
  );
  assert.throws(() => mergeSettingsDraft({}, { ...fixture(), width: "" }, []), /width/);
  assert.throws(() => mergeSettingsDraft({}, { ...fixture(), height: "1.5" }, []), /height/);
  assert.equal(
    mergeSettingsDraft({}, { ...fixture(), pageMode: "http", url: "https://app.example.com" }, [])
      .localRoot,
    "",
  );
});

// 空列表与 Go 返回的 null 保持原样，初次打开页面不应因为序列化差异误报未保存。
test("unchanged null lists stay clean and advanced JSON is validated before applying", () => {
  const values = {
    ...fixture(),
    allowedOrigins: "",
    navigationOrigins: "",
    shellPrograms: "",
    desktopCapabilities: [],
  };
  const base = mergeSettingsDraft({ appId: "erp-a" }, values, []);
  for (const name of [
    "allowedOrigins",
    "navigationOrigins",
    "shellPrograms",
    "desktopCapabilities",
    "permissions",
  ])
    base[name] = null;
  assert.deepEqual(changedSettingsFields(base, mergeSettingsDraft(base, values, [])), []);
  const next = applySettingsPatch(base, '{"title":"新标题"}', "erp-a");
  assert.equal(next.title, "新标题");
  assert.equal(base.title, "业务应用");
  assert.throws(() => applySettingsPatch(base, '{"appId":"erp-b"}', "erp-a"), /发布身份/);
  assert.throws(() => applySettingsPatch(base, '{"debug":"false"}', "erp-a"), /true 或 false/);
  assert.throws(
    () => applySettingsPatch(base, '{"permissions":{"https://example.com":"files"}}', "erp-a"),
    /permissions/,
  );
});
