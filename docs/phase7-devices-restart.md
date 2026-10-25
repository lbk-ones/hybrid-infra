# 第七阶段：设备接入与应用重启

设备入口：工作台及托盘的「设备接入中心」，地址 `/shell/devices.html`。独立前端复制整个生成的 `hybrid/`，导入 `client.js` 中的 `Devices`。当前没有具体型号或实物设备，提供通用通信和文本协议工具，不宣称自动识别所有扫码枪、打印机、电子秤。

## 权限与配置

| 能力 | 用途 |
| --- | --- |
| devices.serial | 枚举、打开串口及收发原始字节 |
| devices.network | 连接固定白名单中的 TCP 设备及收发字节 |
| devices.print | 枚举系统已安装打印机、提交 RAW 打印指令 |
| lifecycle | 复用退出保护并调用 Host.Restart；基础能力默认提供，来源显式列表可撤销 |

新增能力均需在 `desktopCapabilities` 或对应来源的 `permissions` 中开启；一键复制全部权限现在为 33 项。

```json
{
  "desktopCapabilities":["settings","devices.serial","devices.network","devices.print"],
  "deviceEndpoints":["192.168.1.50:9100"]
}
```

网络白名单最多 64 项，只接受字面 IP:端口，不接受域名、URL、通配符、组播和未指定地址。地址必须与连接时传入值完全一致。配置中心已增加白名单输入，保存并重启后生效。不自动扫描网络、连接设备或发送检测指令。

## 串口通信

```javascript
import {Devices} from '/native/client.js';

const ports=await Devices.ListPorts();
const device=await Devices.OpenSerial({
  port:ports[0],baudRate:9600,dataBits:8,
  parity:'none',stopBits:'1',dtr:false,rts:false,
});
const off=Devices.onData(device.id,line=>console.log(line),{
  mode:'lines',encoding:'utf-8',delimiter:'\n',
  onError:error=>console.error(error.code,error.message),
});
// 仅在确认真实指令后发送：
// await Devices.writeHex(device.id,'...');
// 组件卸载：off(); await Devices.Close(device.id,true);
```

默认 9600、8N1，波特率范围 50–4000000，数据位 5–8，校验 none/odd/even/mark/space，停止位 1/1.5/2。仅允许打开当前操作系统枚举的串口，不接受任意磁盘路径。同一串口在本应用内独占，驱动仍可能报告被外部程序占用。DTR/RTS 默认关闭，但部分系统驱动在打开瞬间仍可能产生控制线脉冲，应按设备说明书操作。

连接返回 `{id,kind,name,state,error}`，ID 绑定实际来源和原生窗口。最多八项活动连接、每窗口四项、全应用 64 条记录；连接不持久化，重启后必须重新连接。关闭窗口立即回收；无读取或发送一分钟回收。`ListConnections()` 只查看当前窗口，不会续租闲置连接。

`Write(id,data)` 的 data 为 Base64，返回写入字节数；每次 1 字节至 64 KiB，写入等待最多五秒，失败、超时或取消可能已经发送部分内容，不提供自动重试。`Devices.encode(text,'text')` 为 UTF-8，`encode(hex,'hex')` 为精确十六进制；`writeText`、`writeHex` 是便捷封装。需要 GBK、厂商二进制协议或校验和时，由业务编码后调用 Write，不能假设 UTF-8 对所有设备都正确。

## 回调、接收与清理

`onData(id,callback,options)` 的函数始终留在 JS。宿主只有受限的原始字节读取接口，SDK 按连接共享一个长轮询，再分发给多个订阅者：

- bytes（默认）：回调 `Uint8Array`，适合二进制协议。
- text：流式解码文本，字符跨块时保持解码状态。
- lines：跨块拼接后按 delimiter 回调，默认 LF，并去掉 CRLF 中的 CR。

编码默认 UTF-8，可按设备指定 GBK/GB18030 等 TextDecoder 支持的编码。行帧最多 16 KiB 字符；非法编码、过长帧或业务回调错误通过 onError 报告，单个回调失败不影响其他订阅。连接终止会交付剩余未换行文本，再报告断线。业务回调较慢会造成宿主队列积压，不能无限缓存。

`Read(id,timeoutMs)` 是高级底层接口，每次等待 1–10000 毫秒（0 为默认 5000），返回 `{packets,state,error}`，每包有 sequence 和 Base64 data。每次最多 16 包，全队列最多 64 包/256 KiB，积压明确返回 E_BACKPRESSURE 并关闭连接，不静默丢包。断线后已经接收的多页数据仍可取完。手动 Read 不能与同连接的 onData 竞争接收。

解除监听函数只停止回调，不等于关闭硬件；用 `Close(id,true)` 关闭并移除记录，false 保留终态和剩余接收数据。整页卸载会尽力关闭本页创建的连接；异常离开由窗口和闲置租约兜底。`Devices.dispose()` 是整页清理，调用后需刷新页面重新创建 SDK。

`CancelRead(id)` 明确停止宿主当前接收等待并释放读取权，不关闭设备或清空队列。SDK 切换监听时自动使用它，避免只取消前端 Promise 而旧的 Go 读取仍占用连接。

## 扫码枪

USB 键盘模式扫码枪直接向聚焦区域输入字符，不需要特殊 USB 驱动接口：

```javascript
const off=Devices.onScan('#scan-input',code=>console.log(code),{
  gapMs:50,minLength:3,maxLength:256,terminator:'Enter',prefix:'',
});
// 组件卸载时：off();
```

仅在指定 DOM 区域监听，不使用全局键盘钩子。默认识别快速连续按键加 Enter，忽略输入法组合、重复键及控制快捷键；只有识别后的终止键才阻止默认提交。快速打字也可能匹配，应使用专用输入框和设备前缀降低误判，不把结果当作设备身份或用户认证。串口模式扫码枪使用上面的 onData；不支持厂商私有 USB HID 报告。

## 电子秤

```javascript
const value=Devices.parseWeight('ST,GS,+0012.340kg');
// {value:12.34,unit:'kg',stable:true,overload:false,raw:'...'}
```

支持常见 ST（稳定）、US（不稳定）、OL（超载），可选 GS/NT，单位 kg/g/lb/t。没有稳定标志时 stable 为 null，没有单位时保留空字符串，或通过 `{defaultUnit:'kg'}` 明确指定。不会自动换算、去皮或平滑数据，也不会自动发送秤的查询、清零等指令。私有二进制帧、校验和以及其他品牌格式需要业务提供协议解析器。

## 标签打印机

网络标签机：先配置 deviceEndpoints，再 `OpenTCP('192.168.1.50:9100')`，使用 Write/onData，结束后 Close。串口标签机走 OpenSerial。TCP 只提供原始通信，没有 TLS、认证或设备发现，应按业务网络要求部署。

已安装系统打印机：

```javascript
const names=await Devices.ListPrinters();
// 确认设备支持 ZPL，并准备真实出纸后再执行：
const data=Devices.encode('^XA^FO30,30^A0N,30,30^FDDEMO-001^FS^XZ','text');
const jobID=await Devices.PrintRaw(names[0],'业务标签',data);
```

系统 RAW 仅 Windows 实现，使用已安装的打印机名称及 RAW 数据类型，不安装驱动，不接受任意未安装的打印机路径。每次最多 64 KiB，最多两项同时提交；返回作业 ID 只表示后台接受，不表示设备已出纸。设备必须实际支持所发的 ZPL、TSPL、ESC/POS 等语言；纸张尺寸、字符集和中文字体需按厂商配置。取消等待不保证撤销系统后台作业，避免自动重打导致重复出纸。

## 独立应用重启

```javascript
import {Host} from '/native/client.js';
await Host.Restart();
```

Host.Restart 是独立封装的宿主能力，不依赖配置中心，也不运行 Shell 命令。需要 lifecycle 和实际原生窗口。没有强制重启参数，有未完成任务时返回 E_BUSY：硬件连接打开、写入和 RAW 打印均使用退出保护；空闲连接本身不会一直阻止重启。

配置中心增加「重启应用」和「保存并重启」。只重启时如有未保存草稿，会提示先处理；保存并重启先校验、写入配置再发起重启。如果保存成功但因任务未完成而重启失败，配置仍已保存，稍后可再次重启。

重启使用当前程序的专用助手参数，固定 appId、数据目录、启动配置和工作目录，不接受网页指定其他程序或参数，不重放启动协议链接或更新健康令牌。助手先验证父进程、确认就绪，旧进程才正常保存数据和退出；助手等进程真正结束再启动，避免新进程被旧单实例锁拦住。准备失败保留原应用运行；重新启动失败记录 `restarts/result.json`，不伪装成功。不会自动更新程序或回退业务数据。

Windows 助手需能脱离父作业对象，不满足时会明确失败而不关闭旧应用。`wails3 dev` 的监听器可能另行管理进程生命周期，完整重启应以打包后的程序验收。macOS/Linux 的原生窗口与实际串口仍待实机验证。

## 验证

自动测试使用模拟串口、真实的本机 TCP 回显服务、分包字节和独立可执行文件，覆盖权限隔离、独占和清理、发送取消、任务保护、接收积压、断线多页数据、UTF-8 分包、扫码回调、秤解析、配置白名单，以及父进程退出后的真实助手重启。测试不会向真实设备或打印机发送指令。没有实物设备联调结果，不把模拟测试当作硬件验收。

Windows 另外以隔离目录的无业务窗口 Wails 程序验证了 Controller.Restart 的完整启动链路：父子 PID 不同，重启后的标题由 Stage7 Restart Before 变为 Stage7 Restart After，appId 和数据目录保持一致，旧实例的数据锁没有阻止新实例启动。未重启或替换用户正在运行的 bin 程序。
