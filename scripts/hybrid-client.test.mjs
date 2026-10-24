import assert from 'node:assert/strict';
import { readFile, readdir } from 'node:fs/promises';
import path from 'node:path';
import test from 'node:test';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
// Model browser/native APIs; all HTTP requests remain local to this test stub.
globalThis.document = { readyState: 'loading', addEventListener() {}, documentElement: { addEventListener() {} } };
globalThis.MouseEvent = class extends Event { buttons = 0; };
globalThis.window = {
 location: { origin: 'https://frontend.example.com', hostname: 'frontend.example.com' },
 _wails: { environment: { OS: 'windows' }, flags: {} },
 addEventListener() {}, setInterval() { return 0; }, clearInterval() {},
 chrome: { webview: { postMessage() {} } },
};
const requests = [];
globalThis.fetch = async (url, options) => {
 if (options.method === 'HEAD') return { ok: false };
 requests.push({ url: String(url), options });
 return new Response(JSON.stringify('1'), { headers: { 'Content-Type': 'application/json' } });
};
const { configureHybrid, Runtime, UI } = await import('../hybrid/client.js');

// 用无 window/document 的独立 Node 进程验证导出 SDK 可直接在 SSR 中导入，不能仅靠测试进程中的模拟浏览器掩盖问题。
test('exported SDK imports safely during SSR and detects server environment', () => {
 const source = "const { Hybrid } = await import('./hybrid/client.js'); console.log(JSON.stringify({ hint: Hybrid.isDesktopShell(), environment: await Hybrid.detectEnvironment() }));";
 const output = execFileSync(process.execPath, ['--input-type=module', '-e', source], { cwd: root, encoding: 'utf8', timeout: 5000 });
 const result = JSON.parse(output.trim());
 assert.equal(result.hint, false);
 assert.equal(result.environment.kind, 'server');
 assert.equal(result.environment.connected, false);
});

// 验证新增桌面绑定通过同一可配置传输传参，二进制 Base64 不会被生成绑定误转换为数字数组。
test('desktop facade forwards file bytes and clipboard operations', async () => {
 const { Desktop } = await import('../hybrid/client.js');
 const calls = [];
 configureHybrid({ transport: { async call(...args) { calls.push(args); return '5Lit5paH'; } } });
 try {
  await Desktop.WriteFile('opaque-file-token', '5Lit5paH');
  assert.deepEqual(calls.at(-1)[3].args, ['opaque-file-token', '5Lit5paH']);
  assert.equal(await Desktop.ReadFile('opaque-file-token'), '5Lit5paH');
  await Desktop.ClipboardText('write', '桌面剪贴板');
  assert.deepEqual(calls.at(-1)[3].args, ['write', '桌面剪贴板']);
 } finally { configureHybrid({ transport: null }); }
});

test('remote frontend calls the shell without depending on frontend origin', async () => {
 await UI.Alert('引号"\n', 'body', 2, 'bottomRight');
 const request = requests.at(-1);
 assert.equal(request.url, 'http://wails.localhost/wails/runtime');
 const body = JSON.parse(request.options.body);
 assert.equal(body.object, Runtime.objectNames.Call);
 assert.deepEqual(body.args.args, ['引号"\n', 'body', 2, 'bottomRight']);
 assert.equal(typeof body.args.methodID, 'number');
});

test('runtime origin can be configured after import', async () => {
 configureHybrid({ origin: 'https://shell.example.com:8443/' });
 assert.equal(await UI.Confirm({ title: '确认', timeoutSeconds: 10 }), '1');
 assert.equal(requests.at(-1).url, 'https://shell.example.com:8443/wails/runtime');
 for (const origin of ['javascript:alert(1)', 'wails:invalid', 'https://host/path', 'https://user:pass@host', 'https://host/?token=secret']) {
  assert.throws(() => configureHybrid({ origin }), TypeError);
 }
});

test('custom transport remains available to external frontends', async () => {
 const calls = [];
 configureHybrid({ transport: { async call(...args) { calls.push(args); return '0'; } } });
 assert.equal(await UI.Confirm({ body: 'cancel' }), '0');
 assert.equal(calls[0][0], Runtime.objectNames.Call);
 assert.deepEqual(calls[0][3].args, [{ body: 'cancel' }]);
 configureHybrid({ transport: null });
});

test('exported bindings resolve after copying the hybrid directory to any path', async () => {
 async function check(dir) {
  for (const entry of await readdir(dir, { withFileTypes: true })) {
   const file = path.join(dir, entry.name);
   if (entry.isDirectory()) { await check(file); continue; }
   if (!file.endsWith('.js')) continue;
   const source = await readFile(file, 'utf8');
   assert.ok(!source.includes('from "@wailsio/runtime"'));
   assert.ok(!source.includes('from "/hybrid/'));
   for (const match of source.matchAll(/from\s+["'](\.[^"']+)["']/g)) {
    await readFile(path.resolve(dir, match[1]));
   }
  }
 }
 await check(path.join(root, 'hybrid/bindings'));
 const manifest = JSON.parse(await readFile(path.join(root, 'hybrid/manifest.json'), 'utf8'));
 const pkg = JSON.parse(await readFile(path.join(root, 'frontend/package.json'), 'utf8'));
 assert.equal(manifest.wailsVersion, 'v' + pkg.dependencies['@wailsio/runtime']);
 assert.match(manifest.bindingsSHA256, /^[a-f0-9]{64}$/);
});
