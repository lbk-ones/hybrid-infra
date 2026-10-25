package host

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

const maxTableBytes = 384 << 10
const maxDocumentFile = 512 << 10

// BusinessTable 是通用交换表格；所有单元格均为文本，避免自动执行公式或猜测业务字段类型。
type BusinessTable struct {
	Headers  []string   `json:"headers"`
	Rows     [][]string `json:"rows"`
	Warnings []string   `json:"warnings"`
}

// DocumentExport 返回实际写入的格式和字节数，不把临时路径或整个文件送回网页。
type DocumentExport struct {
	Format string `json:"format"`
	Bytes  int    `json:"bytes"`
}

// Documents 复用已有文件授权，提供有界的表格交换和当前窗口打印，不接受任意磁盘路径。
type Documents struct {
	desktop       *Desktop
	previewMu     sync.Mutex
	previews      []string
	reports       map[string][]byte
	previewServer *http.Server
	previewBase   string
	closed        bool
}

// NewDocuments 创建业务文档服务，文件凭证与现有 Desktop 共用同一个归属管理器。
func NewDocuments(desktop *Desktop) *Documents { return &Documents{desktop: desktop} }

// validateTable 检查列名、矩形行结构和容量；一次返回全表，超限明确拒绝，不悄悄截断业务数据。
func validateTable(table BusinessTable) error {
	if len(table.Headers) == 0 || len(table.Headers) > 100 || len(table.Rows) > 5000 {
		return Fail("E_INVALID_TABLE", "表格需要 1–100 列，最多 5000 行")
	}
	seen := map[string]bool{}
	for _, name := range table.Headers {
		if strings.TrimSpace(name) == "" || len(name) > 256 || !utf8.ValidString(name) || seen[name] {
			return Fail("E_INVALID_TABLE", "列名不能为空、重复或超过 256 字节")
		}
		seen[name] = true
	}
	for _, row := range table.Rows {
		if len(row) != len(table.Headers) {
			return Fail("E_INVALID_TABLE", "每行列数必须与表头一致")
		}
		for _, cell := range row {
			if len(cell) > 16384 || !utf8.ValidString(cell) {
				return Fail("E_INVALID_TABLE", "单元格需为有效 UTF-8 文本，最多 16 KiB")
			}
		}
	}
	encoded, err := json.Marshal(table)
	if err != nil || len(encoded) > maxTableBytes {
		return Fail("E_TOO_LARGE", "表格 JSON 不能超过 384 KiB")
	}
	return nil
}

// Parse 解析用户提供的文件内容，不读取任意路径，不执行公式，不自动写入业务数据库。
func (d *Documents) Parse(ctx context.Context, format string, data []byte) (table BusinessTable, err error) {
	if err = Require(ctx, "documents"); err != nil {
		return table, err
	}
	if len(data) == 0 || len(data) > maxDocumentFile {
		return table, Fail("E_TOO_LARGE", "导入文件需要 1 字节至 512 KiB")
	}
	// 第三方解析器遇到畸形 XML 时不能使宿主退出；预检同时限制解压数据与条目数量。
	defer func() {
		if recover() != nil {
			table = BusinessTable{}
			err = Fail("E_INVALID_FILE", "文档内容损坏，无法导入")
		}
	}()
	switch format {
	case "csv":
		table, err = parseCSV(data)
	case "json":
		if !utf8.Valid(data) || json.Unmarshal(bytes.TrimPrefix(data, []byte{239, 187, 191}), &table) != nil {
			err = Fail("E_INVALID_FILE", "JSON 应为包含 headers 和 rows 的表格对象")
		}
	case "xlsx":
		table, err = parseWorkbook(ctx, data)
	case "zip":
		table, err = parseTableZip(data)
	default:
		err = Fail("E_INVALID_ARGUMENT", "格式仅支持 csv、json、xlsx、zip")
	}
	if err != nil {
		return BusinessTable{}, err
	}
	table.Warnings = []string{}
	for _, row := range table.Rows {
		for _, cell := range row {
			if formulaText(cell) {
				table.Warnings = []string{"发现公式样式文本：仅作为文本导入；导出 CSV 时会加保护前缀。"}
				break
			}
		}
		if len(table.Warnings) > 0 {
			break
		}
	}
	if err = validateTable(table); err != nil {
		return BusinessTable{}, err
	}
	return table, nil
}

// parseCSV 读取 UTF-8 CSV，首行为表头，支持 BOM、引号内逗号和换行，拒绝列数不一致。
func parseCSV(data []byte) (BusinessTable, error) {
	table := BusinessTable{Rows: [][]string{}, Warnings: []string{}}
	if !utf8.Valid(data) {
		return table, Fail("E_INVALID_FILE", "CSV 必须使用 UTF-8 编码")
	}
	reader := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte{239, 187, 191})))
	header, err := reader.Read()
	if err != nil {
		return table, Fail("E_INVALID_FILE", "CSV 缺少有效表头")
	}
	table.Headers = header
	for {
		row, e := reader.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return table, Fail("E_INVALID_FILE", "CSV 格式错误或行列数不一致")
		}
		table.Rows = append(table.Rows, row)
		if len(table.Rows) > 5000 {
			return table, Fail("E_TOO_LARGE", "最多导入 5000 行")
		}
	}
	return table, nil
}

// checkedZip 在交给 XLSX 或业务 ZIP 解析前限制真实解压量、条目数、重复名称及非法路径，不向磁盘解压。
func checkedZip(data []byte) (*zip.Reader, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(reader.File) > 256 {
		return nil, Fail("E_INVALID_FILE", "ZIP 格式无效或条目过多")
	}
	seen := map[string]bool{}
	remaining := int64(8 << 20)
	for _, file := range reader.File {
		if seen[file.Name] || strings.Contains(file.Name, "\\") || strings.HasPrefix(file.Name, "/") || strings.Contains(file.Name, "../") || file.Mode()&os.ModeSymlink != 0 || file.UncompressedSize64 > uint64(remaining) {
			return nil, Fail("E_INVALID_FILE", "压缩包包含非法条目或解压内容超过 8 MiB")
		}
		seen[file.Name] = true
		stream, e := file.Open()
		if e != nil {
			return nil, Fail("E_INVALID_FILE", "压缩条目无法读取")
		}
		n, e := io.Copy(io.Discard, io.LimitReader(stream, remaining+1))
		_ = stream.Close()
		if e != nil || n > remaining {
			return nil, Fail("E_INVALID_FILE", "压缩条目损坏或超过解压限制")
		}
		remaining -= n
	}
	return reader, nil
}

// parseWorkbook 只读取第一个工作表的显示值，不计算公式，限制解压和迭代行数；其他工作表不会自动合并。
func parseWorkbook(ctx context.Context, data []byte) (BusinessTable, error) {
	table := BusinessTable{Rows: [][]string{}}
	if _, err := checkedZip(data); err != nil {
		return table, err
	}
	workbook, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{UnzipSizeLimit: 8 << 20, UnzipXMLSizeLimit: 8 << 20})
	if err != nil {
		return table, Fail("E_INVALID_FILE", "XLSX 无法打开，不支持加密或旧版 XLS")
	}
	defer workbook.Close()
	sheets := workbook.GetSheetList()
	if len(sheets) == 0 {
		return table, Fail("E_INVALID_FILE", "工作簿没有工作表")
	}
	rows, err := workbook.Rows(sheets[0])
	if err != nil {
		return table, Fail("E_INVALID_FILE", "工作表无法读取")
	}
	defer rows.Close()
	for rows.Next() {
		if ctx.Err() != nil {
			return table, ctx.Err()
		}
		row, e := rows.Columns()
		if e != nil {
			return table, Fail("E_INVALID_FILE", "工作表内容损坏")
		}
		if len(table.Headers) == 0 {
			table.Headers = row
			if len(row) == 0 || len(row) > 100 {
				return table, Fail("E_INVALID_TABLE", "首行必须有 1–100 个列名")
			}
			continue
		}
		if len(row) > len(table.Headers) {
			return table, Fail("E_INVALID_TABLE", "数据行超出表头列数")
		}
		for len(row) < len(table.Headers) {
			row = append(row, "")
		}
		table.Rows = append(table.Rows, row)
		if len(table.Rows) > 5000 {
			return table, Fail("E_TOO_LARGE", "工作表最多 5000 行")
		}
	}
	if rows.Error() != nil {
		return table, Fail("E_INVALID_FILE", "工作表读取未完成")
	}
	return table, nil
}

// parseTableZip 只接受本服务的 table.json 表格包，不解压任意附件，未知条目明确拒绝。
func parseTableZip(data []byte) (BusinessTable, error) {
	reader, err := checkedZip(data)
	if err != nil {
		return BusinessTable{}, err
	}
	if len(reader.File) != 1 || reader.File[0].Name != "table.json" {
		return BusinessTable{}, Fail("E_INVALID_FILE", "表格 ZIP 必须且只能包含 table.json")
	}
	stream, err := reader.File[0].Open()
	if err != nil {
		return BusinessTable{}, Fail("E_INVALID_FILE", "表格包无法读取")
	}
	defer stream.Close()
	data, err = io.ReadAll(io.LimitReader(stream, maxTableBytes+1))
	var table BusinessTable
	if err != nil || len(data) > maxTableBytes || !utf8.Valid(data) || json.Unmarshal(data, &table) != nil {
		return table, Fail("E_INVALID_FILE", "表格包内容无效")
	}
	return table, nil
}

// formulaText 判断可能被电子表格软件解释为公式的文本，CSV 导出默认加单引号保护，不执行或计算它。
func formulaText(value string) bool {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	return len(trimmed) > 0 && strings.ContainsRune("=+-@", rune(trimmed[0]))
}

// encodeTable 生成交换文件；CSV 使用 UTF-8 BOM 并保护公式，XLSX 始终写字符串单元格，JSON/ZIP 保留原始文本。
func encodeTable(ctx context.Context, format string, table BusinessTable) ([]byte, error) {
	table.Warnings = []string{}
	if err := validateTable(table); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	switch format {
	case "csv":
		output.Write([]byte{239, 187, 191})
		writer := csv.NewWriter(&output)
		writer.UseCRLF = true
		for _, row := range append([][]string{table.Headers}, table.Rows...) {
			safe := make([]string, len(row))
			for i, value := range row {
				safe[i] = value
				if formulaText(value) {
					safe[i] = "'" + value
				}
			}
			if err := writer.Write(safe); err != nil {
				return nil, err
			}
		}
		writer.Flush()
		if writer.Error() != nil {
			return nil, writer.Error()
		}
	case "json":
		data, err := json.Marshal(table)
		return data, err
	case "zip":
		archive := zip.NewWriter(&output)
		entry, err := archive.Create("table.json")
		if err != nil {
			return nil, err
		}
		if err = json.NewEncoder(entry).Encode(table); err != nil {
			return nil, err
		}
		if err = archive.Close(); err != nil {
			return nil, err
		}
	case "xlsx":
		workbook := excelize.NewFile()
		defer workbook.Close()
		for r, row := range append([][]string{table.Headers}, table.Rows...) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			for c, value := range row {
				cell, _ := excelize.CoordinatesToCellName(c+1, r+1)
				if err := workbook.SetCellStr("Sheet1", cell, value); err != nil {
					return nil, Fail("E_INVALID_TABLE", "单元格无法写入 XLSX")
				}
			}
		}
		if err := workbook.Write(&output); err != nil {
			return nil, Fail("E_EXPORT_FAILED", "工作簿生成失败")
		}
	default:
		return nil, Fail("E_INVALID_ARGUMENT", "格式仅支持 csv、json、xlsx、zip")
	}
	return output.Bytes(), nil
}

// ImportFile 用当前窗口只读凭证导入小表格，不接受路径，预览后是否入库由业务明确决定。
func (d *Documents) ImportFile(ctx context.Context, token, format string) (BusinessTable, error) {
	if err := Require(ctx, "documents"); err != nil {
		return BusinessTable{}, err
	}
	if err := Require(ctx, "files"); err != nil {
		return BusinessTable{}, err
	}
	data, err := d.desktop.ReadFile(ctx, token)
	if err != nil {
		return BusinessTable{}, err
	}
	return d.Parse(ctx, format, data)
}

// ExportFile 将表格原子写入保存凭证授权的目标，校验取消及凭证后才提交，不删除或改动业务数据库。
func (d *Documents) ExportFile(ctx context.Context, token, format string, table BusinessTable) (DocumentExport, error) {
	result := DocumentExport{Format: format}
	if err := Require(ctx, "documents"); err != nil {
		return result, err
	}
	if err := Require(ctx, "files"); err != nil {
		return result, err
	}
	if _, err := d.desktop.file(ctx, token, true); err != nil {
		return result, err
	}
	guard, err := d.desktop.Controller.BeginTask(ctx, "导出业务表格")
	if err != nil {
		return result, err
	}
	defer d.desktop.Controller.EndTask(ctx, guard.ID)
	data, err := encodeTable(ctx, format, table)
	if err != nil {
		return result, err
	}
	if len(data) > maxDocumentFile {
		return result, Fail("E_TOO_LARGE", "导出文件超过 512 KiB")
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	grant, err := d.desktop.file(ctx, token, true)
	if err != nil {
		return result, err
	}
	if err = atomicWrite(grant.Path, data); err != nil {
		return result, diskError(err)
	}
	result.Bytes = len(data)
	return result, nil
}

// Print 打开当前真实原生窗口的系统打印预览；返回只表示已请求，打印机、纸张与另存 PDF 由用户在对话框选择。
func (d *Documents) Print(ctx context.Context) error {
	if err := Require(ctx, "printing"); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return Fail("E_UNSUPPORTED", "当前 Wails Windows 版本的页内打印预览不可用，请使用 Documents.printReport 打开本地报表预览")
	}
	if d.desktop.app == nil || caller(ctx) == "" {
		return Fail("E_WINDOW_NOT_FOUND", "打印需要原生桌面窗口")
	}
	window, ok := d.desktop.app.Window.GetByName(caller(ctx))
	if !ok {
		return Fail("E_WINDOW_NOT_FOUND", "调用窗口已关闭")
	}
	if err := window.Print(); err != nil {
		return Fail("E_UNSUPPORTED", "当前平台无法打开打印预览")
	}
	return nil
}

// OpenPrintPreview 通过仅监听回环地址的临时预览页打开系统默认浏览器，不依赖 HTML 文件关联，不在磁盘持久保存报表。
func (d *Documents) OpenPrintPreview(ctx context.Context, report BusinessReport) error {
	if err := Require(ctx, "printing"); err != nil {
		return err
	}
	if d.desktop.app == nil || caller(ctx) == "" {
		return Fail("E_WINDOW_NOT_FOUND", "打印预览需要原生桌面窗口")
	}
	if _, ok := d.desktop.app.Window.GetByName(caller(ctx)); !ok {
		return Fail("E_WINDOW_NOT_FOUND", "调用窗口已关闭")
	}
	data, err := renderPrintReport(report)
	if err != nil {
		return err
	}
	address, err := d.previewURL(data)
	if err != nil {
		return err
	}
	if err = d.desktop.app.Browser.OpenURL(address); err != nil {
		return Fail("E_SYSTEM", "无法打开默认浏览器，请检查本机浏览器设置")
	}
	return nil
}

// previewURL 保存最多八份内存报表并生成随机访问地址；服务器只绑定 127.0.0.1，不暴露业务 RPC 或文件系统。
func (d *Documents) previewURL(data []byte) (string, error) {
	d.previewMu.Lock()
	defer d.previewMu.Unlock()
	if d.closed {
		return "", Fail("E_SHUTTING_DOWN", "文档中心正在退出")
	}
	if d.previewServer == nil {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return "", Fail("E_SYSTEM", "无法创建本机报表预览")
		}
		d.previewBase = "http://" + listener.Addr().String()
		d.reports = map[string][]byte{}
		d.previewServer = &http.Server{Handler: http.HandlerFunc(d.servePreview), ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 4096}
		server := d.previewServer
		// 生命周期由 Close 管理，关闭监听器后 Serve 正常返回，不启动任何外部网络连接。
		go func() { _ = server.Serve(listener) }()
	}
	id, err := randomID()
	if err != nil {
		return "", err
	}
	if len(d.previews) >= 8 {
		delete(d.reports, d.previews[0])
		d.previews = d.previews[1:]
	}
	d.reports[id] = append([]byte(nil), data...)
	d.previews = append(d.previews, id)
	return d.previewBase + "/report/" + id, nil
}

// servePreview 只返回持有随机地址的内存报表；校验 Host 防止重绑定，不提供 CORS、目录列表或任意路径读取。
func (d *Documents) servePreview(w http.ResponseWriter, r *http.Request) {
	d.previewMu.Lock()
	host := strings.TrimPrefix(d.previewBase, "http://")
	data := d.reports[strings.TrimPrefix(r.URL.Path, "/report/")]
	closed := d.closed
	d.previewMu.Unlock()
	if r.Host != host {
		http.Error(w, "invalid host", http.StatusForbidden)
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if closed || !strings.HasPrefix(r.URL.Path, "/report/") || r.URL.RawQuery != "" || data == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	if r.Method == "GET" {
		_, _ = w.Write(data)
	}
}

// Close 停止回环预览服务并释放内存报表；用户已打开的浏览器页不会被强关，重新加载需再次生成预览。
func (d *Documents) Close() {
	d.previewMu.Lock()
	d.closed = true
	server := d.previewServer
	d.reports = nil
	d.previews = nil
	d.previewMu.Unlock()
	if server != nil {
		_ = server.Close()
	}
}
