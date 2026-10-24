# 通知、拖拽和截图

工作台新增“通知与弹窗”和“区域截图”入口，分别为 `/shell/ui.html` 和 `/shell/capture.html`。独立前端复制完整 `hybrid/` 目录后导入 `client.js`。

## UI 通知

```javascript
import { UI } from '/native/client.js';

await UI.Notify('操作完成'); // 右上角、四秒、允许拖拽。
const id = await UI.Notify({
  text: '业务数据已同步',
  place: 'topRight',
  durationSeconds: 6,
  draggable: false,
});
await UI.CloseNotification(id); // 可选，创建窗口可提前关闭自己的通知。
```

也支持 `UI.Notify(text, options)`。此方法是应用自己渲染的原生浮窗，不是 `Desktop.Notify` 的操作系统通知，使用已有 `ui` 权限。

位置支持 topLeft / topCenter / topRight / center / bottomLeft / bottomCenter / bottomRight。默认右上角。上方通知按调用顺序向下排，下方通知向上排；竖向满后另起一列，关闭后收拢空位。拖离堆叠位置的通知不会被新通知拉回原处，但仍按原时间关闭。

时长为秒，支持小数；省略或 0 为四秒，有效范围 0.1–3600 秒。顶部线性进度条表示剩余时间，真实关闭由 Go 定时器负责，从页面内容就绪开始计时；加载失败的窗口也有十秒兜底关闭。通知文本为纯文本，最多 4096 字节，同时最多 24 条且受工作区容量限制。关闭创建窗口会清理其通知。

## 其他 UI 的拖拽属性

```javascript
await UI.Alert({ title: '提示', body: '已保存', destroy: 3, draggable: false });
// 旧四参数 Alert 保留，也可加第五个选项：
await UI.Alert('提示', '已保存', 3, 'topRight', { draggable: false });
await UI.Confirm({ title: '继续？', body: '请确认', draggable: false });
await UI.Show({ body: trustedHTML, width: 360, height: 120, draggable: false });
```

Notify / Alert / Confirm / Show 的 draggable 都默认 true，只有显式 false 禁用。消息标题及空白区域、自定义浮窗的非交互区域可拖动；按钮、链接、输入框等保留点击和输入，消息正文保留选择文字。此属性控制这些无边框 UI 的网页拖拽区域，不改变系统标题栏窗口的原生行为。Show 仍只接受可信 HTML，不进行 HTML 清洗。

直接使用生成 Go 绑定时，旧 Alert 方法仍是四参数，对象写法用 `AlertWithOptions`；完整独立 SDK 和内置 host-client 的 UI 门面已统一上述两种写法。

## 坐标截图与手动框选

```javascript
import { Desktop, Hybrid } from '/native/client.js';

const displays = await Desktop.CaptureDisplays();
// 坐标为虚拟桌面的物理像素，位于主屏左侧的屏幕可以有负坐标。
const shot = await Desktop.Screenshot({ x: 100, y: 100, width: 800, height: 600 });
image.src = `data:${shot.mime};base64,${shot.base64}`;

// 手动模式应增加 RPC 等待时间；省略 displayId 默认选择调用窗口所在屏幕。
const selected = await Hybrid.call(
  () => Desktop.Screenshot({ mode: 'select', displayId: displays[0].id, output: 'base64' }),
  { timeoutMs: 125000, signal: controller.signal },
);
// 只写剪贴板，不把 Base64 返回前端：
await Desktop.Screenshot({ x: 100, y: 100, width: 800, height: 600, output: 'clipboard' });
// 两种输出都需要时用 output:'both'。
```

新增 `screenshot` 能力，必须在内置 desktopCapabilities 或远程 permissions 中显式开启。写剪贴板还需已有 `clipboard` 权限，权限在采集屏幕之前检查。示例配置已加入 screenshot，实际用户配置不会被自动修改。

`CaptureDisplays` 返回显示器 id、name、primary、physical、logical。physical 用于截图坐标，logical 仅用于解释显示缩放；不需要前端自行猜测 devicePixelRatio。坐标模式可跨显示器，屏幕间空隙填黑，越过虚拟桌面则拒绝。

手动模式先冻结指定显示器，然后显示不可拖拽的全屏遮罩，拖动并松开鼠标完成；支持任意方向拖动，Escape 或右键取消。选区根据遮罩真实 viewport 与显示器尺寸转换成物理像素，再从冻结图像裁剪，所以结果不含遮罩和工具条。手动框选限定在所选显示器内，需要其他屏幕可通过 displayId 切换。

返回 `region:{x,y,width,height}`、`mime:'image/png'`、可选 base64、clipboard。Base64 是原始 PNG 的 Base64 字符串，不含 data URI 前缀。PNG 不自动写入磁盘，也不进入日志或业务事件广播。剪贴板输出使用 PNG 和兼容 DIB，较大的截图可以写入剪贴板，原普通 ClipboardImage RPC 的 512 KiB / 400 万像素限制仍保持。

单次最多 1600 万像素，单边 16384，PNG 最多 20 MiB；同时只允许一个采集或框选。框选最多等待两分钟，Escape、调用取消、来源窗口关闭、遮罩关闭或退出都会结束等待并释放画面。截图必须从桌面窗口发起。录屏、OCR、自动落盘和跨屏手动拖选不在本次范围。

## 验证与平台

Go 测试覆盖通知默认值、工作区堆叠/换列、倒计时与归属、拖离和清理、脚本文本边界；截图覆盖负坐标、跨屏拼接、高 DPI、输出选择、随机凭证、原生来源和取消。测试使用合成像素，不读用户桌面、不改用户剪贴板。Node 测试验证 UI 新旧参数兼容。

Windows 原生已核对通知可见及顺序堆叠坐标。截图主验收平台为 Windows；其他系统依赖屏幕采集库及系统屏幕录制权限，剪贴板图片目前仅 Windows 适配。CGO 关闭的 Darwin 可能返回 E_UNSUPPORTED。
