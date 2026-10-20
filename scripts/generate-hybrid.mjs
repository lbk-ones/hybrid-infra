import { execFileSync, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { cp, mkdir, mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const run = (args) => execFileSync('wails3', args, { cwd: root, stdio: 'inherit' });
const goVersion = execFileSync('go', ['list', '-m', '-f', '{{.Version}}', 'github.com/wailsapp/wails/v3'], { cwd: root, encoding: 'utf8' }).trim();
const cli = spawnSync('wails3', ['version'], { cwd: root, encoding: 'utf8' });
if (cli.error || cli.status !== 0) throw cli.error || new Error(cli.stderr);
const cliVersion = (cli.stdout + cli.stderr).trim();
const pkg = JSON.parse(await readFile(path.join(root, 'frontend/package.json'), 'utf8'));
if (cliVersion !== goVersion || 'v' + pkg.dependencies['@wailsio/runtime'] !== goVersion) {
 throw new Error(`Version mismatch: Go=${goVersion}, CLI=${cliVersion}, runtime=${pkg.dependencies['@wailsio/runtime']}. Pin all three to the same release.`);
}
const temp = await mkdtemp(path.join(tmpdir(), 'hybrid-infra-'));
try {
 const output = path.join(temp, 'hybrid');
 const bindings = path.join(output, 'bindings');
 await mkdir(output);
 run(['generate', 'bindings', '-d', bindings]);
 run(['generate', 'runtime', '-d', output]);
 const runtimeOrigin = '(window.__HYBRID_RUNTIME_ORIGIN__ || (window.location.hostname === "wails.localhost" ? window.location.origin : "http://wails.localhost"))';
 for (const filename of ['runtime.js', 'runtime-debug.js']) {
  const target = path.join(output, filename);
  const source = await readFile(target, 'utf8');
  if (!source.includes('window.location.origin')) throw new Error(`Upstream runtime changed: review ${filename} before exporting.`);
  await writeFile(target, source.replaceAll('window.location.origin', runtimeOrigin));
 }
 const hash = createHash('sha256');
 async function rewrite(dir) {
  for (const entry of (await readdir(dir, { withFileTypes: true })).sort((a,b) => a.name.localeCompare(b.name, 'en'))) {
   const target = path.join(dir, entry.name);
   if (entry.isDirectory()) { await rewrite(target); continue; }
   let source = await readFile(target, 'utf8');
   if (entry.name.endsWith('.js')) {
    let relative = path.relative(dir, path.join(output, 'runtime.js')).split(path.sep).join('/');
    if (!relative.startsWith('.')) relative = './' + relative;
    source = source.replaceAll('"@wailsio/runtime"', JSON.stringify(relative));
    await writeFile(target, source);
   }
   hash.update(path.relative(output, target).split(path.sep).join('/')).update('\0').update(source);
  }
 }
 await rewrite(bindings);
 await cp(path.join(root, 'scripts/hybrid-client.js'), path.join(output, 'client.js'));
 await writeFile(path.join(output, 'manifest.json'), JSON.stringify({ wailsVersion: goVersion, bindingsSHA256: hash.digest('hex') }, null, 2) + '\n');
 // No shell rm/sed/eval. Only replace the repository-owned generated directory.
 const destination = path.join(root, 'hybrid');
 await mkdir(destination, { recursive: true });
 await cp(output, destination, { recursive: true });
 // Remove stale generated bindings after successful generation.
 const oldBindings = path.join(destination, 'bindings');
 await rm(oldBindings, { recursive: true, force: true });
 await cp(bindings, oldBindings, { recursive: true });
 console.log(`Exported hybrid for ${goVersion}. Copy the complete directory into your frontend's public assets.`);
} finally {
 await rm(temp, { recursive: true, force: true });
}
