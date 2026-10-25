import { readFile, writeFile, mkdir, copyFile } from 'node:fs/promises';
import { createPrivateKey, createPublicKey, createHash, sign, verify } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

/** 校验发布元数据并离线签名；私钥只从指定文件读取，不写入清单或日志。 */
export async function packageRelease({ exe, version, url, keyFile, out, channel = 'stable', arch = 'amd64' }) {
  if (!exe || !keyFile || !out || !/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version) || !['stable', 'beta'].includes(channel) || !['amd64', 'arm64'].includes(arch)) throw new Error('需要 exe、key-file、out、三段正式版本号及有效频道、架构');
  const address = new URL(url);
  if (address.username || address.password || address.hash || !(address.protocol === 'https:' || address.protocol === 'http:' && ['localhost', '127.0.0.1', '[::1]'].includes(address.hostname))) throw new Error('下载地址必须是 HTTPS；本机测试允许回环 HTTP');
  const bytes = await readFile(exe);
  if (bytes.length < 64 || bytes.length > 256 * 1048576 || bytes.toString('ascii', 0, 2) !== 'MZ') throw new Error('仅支持 ≤256 MiB 的 Windows EXE');
  const offset = bytes.readUInt32LE(0x3c);
  if (offset + 6 > bytes.length || bytes.toString('ascii', offset, offset + 4) !== 'PE\0\0' || bytes.readUInt16LE(offset + 4) !== (arch === 'amd64' ? 0x8664 : 0xaa64)) throw new Error('EXE 架构不匹配');
  // 从 Go 构建元数据核实实际版本，避免清单版本与运行时健康标记不一致而自动回滚。
  const buildInfo = JSON.parse(execFileSync(path.resolve(exe), ['--hybrid-release-info'], { encoding: 'utf8', timeout: 5000, windowsHide: true }));
  if (buildInfo.version !== version || buildInfo.platform !== 'windows' || buildInfo.arch !== arch || buildInfo.dataSchema !== 1) throw new Error('EXE 版本、平台或数据版本不匹配；先执行 wails3 task build VERSION=' + version);
  const key = createPrivateKey(await readFile(keyFile));
  if (key.asymmetricKeyType !== 'ed25519') throw new Error('私钥必须是 Ed25519 PKCS8 PEM');
  const payload = Buffer.from(JSON.stringify({ schema: 1, version, channel, platform: 'windows', arch, url, sha256: createHash('sha256').update(bytes).digest('hex'), size: bytes.length, minDataSchema: 0, maxDataSchema: 1 }));
  const signature = sign(null, payload, key);
  if (!verify(null, payload, createPublicKey(key), signature)) throw new Error('发布签名自检失败');
  const envelope = { payload: payload.toString('base64'), signature: signature.toString('base64') };
  const publicKey = createPublicKey(key).export({ format: 'der', type: 'spki' }).subarray(-32).toString('base64');
  await mkdir(out, { recursive: true });
  await copyFile(exe, path.join(out, `hybrid-infra-${version}-windows-${arch}.exe`));
  await writeFile(path.join(out, 'manifest.json'), JSON.stringify(envelope, null, 2) + '\n');
  await writeFile(path.join(out, 'public-key.txt'), publicKey + '\n');
  return { version, publicKey, manifest: path.resolve(out, 'manifest.json') };
}

/** 将固定的命名参数转为发布选项，拒绝未知项和缺值，避免误发布到默认位置。 */
function parseArgs(args) {
  const options = {}, names = { '--exe': 'exe', '--version': 'version', '--url': 'url', '--key-file': 'keyFile', '--out': 'out', '--channel': 'channel', '--arch': 'arch' };
  for (let index = 0; index < args.length; index += 2) {
    if (!names[args[index]] || !args[index + 1] || args[index + 1].startsWith('--')) throw new Error('未知参数或缺少参数值：' + args[index]);
    options[names[args[index]]] = args[index + 1];
  }
  return options;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { console.log(JSON.stringify(await packageRelease(parseArgs(process.argv.slice(2))), null, 2)); }
  catch (error) { console.error(error.message); process.exitCode = 1; }
}
