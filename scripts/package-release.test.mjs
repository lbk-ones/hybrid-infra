import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, writeFile, readFile, rm, access } from 'node:fs/promises';
import { generateKeyPairSync, createPublicKey, verify, createHash } from 'node:crypto';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { packageRelease } from './package-release.mjs';

const exe = path.resolve('bin/hybrid-infra.exe');
let built = process.platform === 'win32';
try { await access(exe); } catch { built = false; }

// 使用临时密钥打包本机构建，核验签名、哈希和实际版本；不上传、安装或修改启动设置。
test('release manifest binds the actual executable version, hash and Ed25519 signature', { skip: !built }, async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'hybrid-release-test-'));
  try {
    const info = JSON.parse(execFileSync(exe, ['--hybrid-release-info'], { encoding: 'utf8', windowsHide: true }));
    const { privateKey, publicKey } = generateKeyPairSync('ed25519');
    const keyFile = path.join(dir, 'test-key.pem');
    await writeFile(keyFile, privateKey.export({ format: 'pem', type: 'pkcs8' }));
    const options = { exe, version: info.version, arch: info.arch, url: 'https://example.invalid/app.exe', keyFile, out: path.join(dir, 'release') };
    const result = await packageRelease(options);
    const envelope = JSON.parse(await readFile(result.manifest, 'utf8'));
    const payload = Buffer.from(envelope.payload, 'base64');
    assert.ok(verify(null, payload, publicKey, Buffer.from(envelope.signature, 'base64')));
    const manifest = JSON.parse(payload);
    assert.equal(manifest.sha256, createHash('sha256').update(await readFile(exe)).digest('hex'));
    assert.equal(result.publicKey, createPublicKey(privateKey).export({ format: 'der', type: 'spki' }).subarray(-32).toString('base64'));
    await assert.rejects(packageRelease({ ...options, version: '999.0.0' }), /版本/);
    await assert.rejects(packageRelease({ ...options, url: 'http://example.invalid/update.exe' }), /HTTPS/);
  } finally {
    const relative = path.relative(tmpdir(), dir);
    if (!relative || relative.startsWith('..') || path.isAbsolute(relative)) throw new Error('临时目录边界异常');
    await rm(dir, { recursive: true, force: true });
  }
});
