package host

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestDocumentFormatsRoundTrip 验证四种真实文件格式均能往返中文、引号和换行，XLSX 公式文本保持字符串。
func TestDocumentFormatsRoundTrip(t *testing.T) {
	desktop, ctx := desktopFixture(t)
	documents := NewDocuments(desktop)
	table := BusinessTable{Headers: []string{"订单号", "备注", "金额"}, Rows: [][]string{{"001", "中文,\"引号\"\n换行", "128.50"}, {"002", "", "0"}}}
	for _, format := range []string{"csv", "json", "xlsx", "zip"} {
		t.Run(format, func(t *testing.T) {
			data, err := encodeTable(ctx, format, table)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := documents.Parse(ctx, format, data)
			if err != nil || !reflect.DeepEqual(parsed.Headers, table.Headers) || !reflect.DeepEqual(parsed.Rows, table.Rows) {
				t.Fatalf("roundtrip: %v %+v", err, parsed)
			}
		})
	}
	formula := BusinessTable{Headers: []string{"text"}, Rows: [][]string{{"=SUM(1,2)"}, {" \t@cmd"}, {"-1"}}}
	for _, format := range []string{"csv", "xlsx", "json", "zip"} {
		data, err := encodeTable(ctx, format, formula)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := documents.Parse(ctx, format, data)
		if err != nil {
			t.Fatal(err)
		}
		for i, row := range parsed.Rows {
			expected := formula.Rows[i][0]
			if format == "csv" {
				expected = "'" + expected
			}
			if row[0] != expected {
				t.Fatalf("formula %s: %q", format, row[0])
			}
		}
	}
}

// TestDocumentValidation 验证权限、格式和矩形结构限制，不截断坏行，不接受旧 XLS、超量或损坏文件。
func TestDocumentValidation(t *testing.T) {
	desktop, ctx := desktopFixture(t)
	documents := NewDocuments(desktop)
	if _, err := documents.Parse(context.Background(), "csv", []byte("a\n1")); err == nil {
		t.Fatal("missing capability")
	}
	bad := []struct{ format, data string }{{"csv", "a,a\n1,2"}, {"csv", "a,b\n1"}, {"json", `{"headers":["a"],"rows":[["one","two"]]}`}, {"json", `{"headers":["a"],"rows":[[1]]}`}, {"xlsx", "not a workbook"}, {"xls", "test"}, {"csv", "a\n\xff"}}
	for _, value := range bad {
		if _, err := documents.Parse(ctx, value.format, []byte(value.data)); err == nil {
			t.Fatalf("accepted %+v", value)
		}
	}
	if _, err := documents.Parse(ctx, "csv", bytes.Repeat([]byte("x"), maxDocumentFile+1)); err == nil {
		t.Fatal("oversized file")
	}
	if _, err := documents.Parse(ctx, "csv", []byte("a\n"+strings.Repeat("1\n", 5001))); err == nil {
		t.Fatal("too many rows")
	}
	table := BusinessTable{Headers: []string{"a"}, Rows: [][]string{{strings.Repeat("a", 16385)}}}
	if _, err := encodeTable(ctx, "json", table); err == nil {
		t.Fatal("oversized cell")
	}
}

// tableZipFixture 创建仅用于测试的压缩包，不向磁盘释放内容，用来验证重复名称、目录穿越和解压容量。
func tableZipFixture(t *testing.T, names []string, data []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, name := range names {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

// TestDocumentArchiveLimits 验证表格 ZIP 白名单与真实解压量，不允许借压缩包执行任意磁盘解压。
func TestDocumentArchiveLimits(t *testing.T) {
	desktop, ctx := desktopFixture(t)
	documents := NewDocuments(desktop)
	data := []byte(`{"headers":["a"],"rows":[["1"]]}`)
	for _, names := range [][]string{{"../table.json"}, {"table.json", "table.json"}, {"other.json"}, {"table.json", "extra.txt"}} {
		if _, err := documents.Parse(ctx, "zip", tableZipFixture(t, names, data)); err == nil {
			t.Fatal("illegal zip accepted", names)
		}
	}
	bomb := tableZipFixture(t, []string{"table.json"}, bytes.Repeat([]byte("a"), (8<<20)+1))
	if _, err := documents.Parse(ctx, "zip", bomb); err == nil {
		t.Fatal("decompression limit bypassed")
	}
	data = tableZipFixture(t, []string{"table.json"}, data)
	reader, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	offset, _ := reader.File[0].DataOffset()
	data[offset] = 0xff // 损坏实际 deflate 数据，而不是 ZIP 中可忽略的元数据。
	if _, err := documents.Parse(ctx, "zip", data); err == nil {
		t.Fatal("corrupt zip accepted")
	}
}

// TestDocumentFileOwnership 验证真实原子导出、凭证归属、缺少复合权限和校验失败不覆盖已有目标。
func TestDocumentFileOwnership(t *testing.T) {
	desktop, ctx := desktopFixture(t)
	documents := NewDocuments(desktop)
	path := filepath.Join(t.TempDir(), "table.xlsx")
	grant, err := desktop.grant(ctx, path, false, true)
	if err != nil {
		t.Fatal(err)
	}
	table := BusinessTable{Headers: []string{"姓名"}, Rows: [][]string{{"测试"}}}
	result, err := documents.ExportFile(ctx, grant.Token, "xlsx", table)
	if err != nil || result.Bytes < 100 {
		t.Fatal(err, result)
	}
	original, _ := os.ReadFile(path)
	if len(desktop.Controller.tasks) != 0 {
		t.Fatal("exit guard leaked")
	}
	parsed, err := documents.ImportFile(ctx, grant.Token, "xlsx")
	if err != nil || parsed.Rows[0][0] != "测试" {
		t.Fatal(err, parsed)
	}
	if _, err = documents.ImportFile(bridgeContext("https://app.test", "other", "documents", "files"), grant.Token, "xlsx"); err == nil {
		t.Fatal("cross-window token")
	}
	if _, err = documents.ExportFile(bridgeContext("https://app.test", "test-window", "documents"), grant.Token, "json", table); err == nil {
		t.Fatal("files capability bypassed")
	}
	table.Headers = []string{"a", "a"}
	if _, err = documents.ExportFile(ctx, grant.Token, "csv", table); err == nil {
		t.Fatal("invalid export accepted")
	}
	current, _ := os.ReadFile(path)
	if !bytes.Equal(original, current) {
		t.Fatal("failed validation overwrote file")
	}
	if err = documents.Print(context.Background()); err == nil {
		t.Fatal("print capability bypassed")
	}
	if err = documents.Print(ctx); err == nil {
		t.Fatal("headless print pretended success")
	}
}

// TestDocumentTableJSONBudget 验证响应按 JSON 序列化后的真实体积控制，包括引号转义造成的增长。
func TestDocumentTableJSONBudget(t *testing.T) {
	table := BusinessTable{Headers: []string{"text"}, Rows: [][]string{}}
	for range 50 {
		table.Rows = append(table.Rows, []string{strings.Repeat("\"", 8192)})
	}
	encoded, _ := json.Marshal(table)
	if len(encoded) <= maxTableBytes {
		t.Fatal("fixture too small")
	}
	if validateTable(table) == nil {
		t.Fatal("response budget not enforced")
	}
}

// TestPrintReportTemplate 验证本地报表内容自动转义、固定纸张参数与权限，不将业务脚本或外部链接作为资源加载。
func TestPrintReportTemplate(t *testing.T) {
	report := BusinessReport{BusinessTable: BusinessTable{Headers: []string{"备注"}, Rows: [][]string{{`<img src="https://example.test/x" onerror="alert(1)">`}}}, Title: `<script>alert(1)</script>`, Paper: "Letter", Orientation: "landscape"}
	data, err := renderPrintReport(report)
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	if strings.Contains(html, "<img") || strings.Contains(html, "<script>") || !strings.Contains(html, "&lt;script&gt;") || !strings.Contains(html, "size:Letter landscape") || !strings.Contains(html, "default-src 'none'") {
		t.Fatal("unsafe report or wrong layout")
	}
	report.Orientation = "portrait; color:red"
	if _, err = renderPrintReport(report); err == nil {
		t.Fatal("CSS injection accepted")
	}
	desktop, _ := desktopFixture(t)
	documents := NewDocuments(desktop)
	if err = documents.OpenPrintPreview(context.Background(), report); err == nil {
		t.Fatal("preview permission bypassed")
	}
}

// TestPrintPreviewLoopback 验证随机回环预览地址、Host 限制、只读方法、历史淘汰和关闭服务，不接触真实浏览器。
func TestPrintPreviewLoopback(t *testing.T) {
	desktop, _ := desktopFixture(t)
	documents := NewDocuments(desktop)
	defer documents.Close()
	address, err := documents.previewURL([]byte("<h1>test</h1>"))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if string(data) != "<h1>test</h1>" || response.Header.Get("Cache-Control") != "no-store" || !strings.HasPrefix(address, "http://127.0.0.1:") {
		t.Fatal("invalid preview response")
	}
	request, _ := http.NewRequest("GET", address, nil)
	request.Host = "evil.test"
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("Host bypass")
	}
	response, err = http.Post(address, "text/plain", strings.NewReader("test"))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 405 {
		t.Fatal("write method allowed")
	}
	for range 8 {
		if _, err = documents.previewURL([]byte("next")); err != nil {
			t.Fatal(err)
		}
	}
	response, err = http.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatal("old report not evicted")
	}
	documents.Close()
	if _, err = documents.previewURL([]byte("late")); err == nil {
		t.Fatal("reopened after shutdown")
	}
}
