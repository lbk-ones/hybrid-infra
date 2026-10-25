# 第六阶段：打印、PDF 与业务文件交换

入口为工作台和托盘的「打印与文件中心」，内置页面 `/shell/documents.html`。文档左侧「桌面能力」新增「打印与 PDF」「文件拖入与导入导出」，每个公开方法均有通俗说明、参数和可执行示例。

## 授权

在现有配置的 `desktopCapabilities` 中追加 `documents`、`printing`，原生选择和保存文件还需 `files`；如需在统一任务中心查看导出，再加 `tasks`。远程页面在 `permissions` 中单独授权。不会自动修改用户配置，重启后生效。全部权限清单现在为 30 项。

```json
{"desktopCapabilities":["documents","printing","files","tasks"]}
```

`documents` 控制文件解析和表格导入导出；`printing` 允许生成本地打印报表并打开系统默认浏览器。普通报表预览与标准 File 拖放监听在 JS 中执行，本身不获得额外宿主权限。

## 表格格式与导入

统一结构为 `{headers:string[],rows:string[][],warnings:string[]}`。表头唯一非空，最多 100 列、5000 行，序列化后的全表 JSON 最多 384 KiB；单元格最多 16 KiB。所有值是文本，不自动猜测数字、日期或业务字段类型。文件和导出产物最多 512 KiB；错误和超限明确返回，不静默截断。

```javascript
import {Documents,Desktop} from '/native/client.js';

const file=await Desktop.SelectFile('open','');
try {
  const table=await Documents.ImportFile(file.token,'csv');
  console.log(table.headers,table.rows,table.warnings);
} finally { await Desktop.ReleaseFile(file.token); }
```

| 格式 | 约定 |
| --- | --- |
| CSV | UTF-8，接受 BOM、引号内逗号与换行；首行是表头，列数必须一致。导出带 BOM 和 CRLF，公式样式文本添加单引号，保护也适用于表头。 |
| JSON | 统一表格对象，保留原始文本。不是任意业务对象数组，不自动映射字段。 |
| XLSX | 使用 Excelize，仅读取第一个工作表的显示/缓存值，不计算公式，不合并其他工作表。导出单元格全部写为字符串；不支持旧 XLS、加密文件和样式往返。 |
| ZIP | 本服务的表格包，只允许一个 `table.json`，保存统一表格对象。不是通用附件解压或应用数据备份恢复。 |

XLSX 和 ZIP 在解析前检查条目数量、重复名称、非法路径及实际解压容量，最多 256 个条目、合计 8 MiB；不将表格 ZIP 解压到磁盘。不接受公式作为可执行指令，XLSX 的公式缓存可能为空或过时；需要计算结果时应由业务系统生成。

`Documents.Parse(format,data)` 接受 Base64 文件内容；数据直接来自用户选择或拖入时，可使用 `Documents.readFile(file,format)`，省略 format 会按文件扩展名识别。实际解析仍由宿主校验 `documents`。

## 文件拖入与回调

```javascript
const off=Documents.onDrop('#drop-zone',async(files)=>{
  for(const file of files){
    const table=await Documents.readFile(file);
    console.log(file.name,table.rows.length);
  }
},{onError:error=>console.error(error.message)});
// 组件卸载时：off();
```

拖放使用标准 File API，一次最多 10 个文件。回调保留在 JS，不传给 Go；不自动读取、上传或入库，不返回用户磁盘绝对路径。组件移除时调用 `off()` 停止新回调；已经开始的业务操作需自行取消。文件中心多文件顺序校验，每个成功文件有独立结果入口，不自动合并表头。

固定 Wails 运行时的祖先拖放监听会设置禁止光标，SDK 在自己的文件区域阻止冒泡，避免进入原生文件路径转发通道。普通浏览器可监听拖放，但没有宿主时不能调用原生解析。macOS/Linux 文件拖放仍需实机验证，文件选择是可用的替代入口。

## 导出与任务中心

```javascript
const table={headers:['订单号','金额'],rows:[['001','128.00']]};
const target=await Desktop.SelectFile('save','orders.xlsx');
try {
  const result=await Documents.ExportFile(target.token,'xlsx',table);
  console.log(result.format,result.bytes);
} finally { await Desktop.ReleaseFile(target.token); }
```

服务在编码成功后重新检查保存凭证，再原子替换目标；任何字段校验失败不会覆盖原文件。只接受本窗口授权的 token。后台工作有退出保护；提交前会检查取消，但取消不能撤回已经原子保存的文件。

文件中心在有 `tasks` 时用 `Tasks.run` 包装导出，真实阶段为排队、生成、保存完成，不模拟连续进度。保存凭证在结束或失败后释放。组件可使用 `Hybrid.call(()=>Documents.ExportFile(...),{signal,timeoutMs:60000})` 传递取消。新接口不强制导入到某个数据库集合；业务应校验字段、主键及重复策略，再选择 `Database.Batch`，避免自动覆盖。

## 报表、打印与 PDF

```javascript
const report={
  title:'订单报表',subtitle:'本月业务明细',
  headers:['订单号','金额'],rows:[['001','128.00']],
  paper:'A4',orientation:'portrait',footer:'业务部门',
};
const clear=Documents.preview('#preview',report);
await Documents.printReport(report);
// 组件卸载时：clear();
```

`preview` 只向指定容器附加报表和样式，返回清理函数。`printReport` 调用宿主 `OpenPrintPreview(report)`，生成固定模板内存报表，通过仅监听 127.0.0.1 的随机预览地址打开系统默认浏览器。当前固定 Wails Windows 版本的页内打印请求不能正常显示预览，因此不使用它。业务文字通过文本节点渲染，不接受任意 HTML。支持 A4/Letter、纵向 portrait/横向 landscape、表头重复与行内避免分页；最终分页和缩放以系统预览为准。

在外部报表页点击「打印 / 另存为 PDF」，再在系统对话框中选择打印机、份数、纸张或「另存为 PDF」。系统默认浏览器需支持打印。**PDF 是交互式系统导出，当前没有静默 PDF 文件生成、打印机枚举、批量打印和打印成功回执。** `printReport` 返回 `{requested:true,mode:"external-preview"}`，`Documents.OpenPrintPreview(report)` 无返回值，只表示请求已发送，不表示用户已经保存或实际打印完成。

`Documents.Print()` 是平台原生打印入口，当前 Windows 版本明确返回 E_UNSUPPORTED，macOS/Linux 尚未实机验收；推荐使用 `printReport`。本地报表文字自动转义，不引用外部网络资源，最多保留最近八份内存报表。预览服务只绑定回环地址，不提供业务 RPC、文件目录或 CORS，校验 Host、随机地址和只读方法。退出时关闭服务并释放报表，浏览器已加载的页面不强制关闭；刷新预览需重新生成。`Documents.dispose()` 只清理本页预览 DOM，不强制关闭外部查看程序。文件中心预览只显示前 100 行，打印和导出使用全部校验数据。

## 验证

`go test ./...` 覆盖真实 CSV/JSON/XLSX/ZIP 往返、中文及换行、公式保护、损坏/超大压缩包、字段和容量限制、权限与文件归属、失败不覆盖文件。`node --test scripts/*.test.mjs` 覆盖 File 字节转换、文本预览、本地预览委托与清理、拖放回调释放及文档示例完整性。另执行 Go 静态检查、生产构建与项目格式检查。

本阶段不修改数据结构版本；Schema 仍为 2。需要保存的长期秘密继续使用系统凭据库，导出的业务表格由用户自行管理。
