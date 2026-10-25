import { createRequire } from "node:module";
import { copyFile, readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const require = createRequire(path.join(root, "frontend/package.json"));
const sharp = require("sharp");
const source = path.join(root, "frontend/public/app-icon.svg");

/** 从同一份矢量母版生成打包 PNG 和运行时图标，保证托盘、任务栏及前端标识一致。 */
async function generateIcons() {
  const svg = await readFile(source);
  for (const [name, size] of [
    ["app-icon.png", 256],
    ["tray-icon.png", 64],
  ]) {
    await sharp(svg)
      .resize(size, size)
      .png()
      .toFile(path.join(root, "frontend/public", name));
  }
  await sharp(svg).resize(1024, 1024).png().toFile(path.join(root, "build/appicon.png"));
  await copyFile(source, path.join(root, "frontend/public/favicon.svg"));
  console.log("Updated app, tray, favicon and build icon assets from app-icon.svg.");
}

await generateIcons();
