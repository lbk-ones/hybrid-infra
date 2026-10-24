package host

import (
	"bytes"
	"context"
	"hybrid-infra/go/setting"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// desktopContext 构造已通过来源认证的窗口身份，避免测试访问真实用户页面或配置。
func desktopContext(window string) context.Context {
	return context.WithValue(context.Background(), principalKey{}, Principal{Origin: "https://app.test", WindowName: window, Capabilities: Capabilities})
}

// desktopFixture 为每个测试创建独立数据目录和管理器；不创建原生窗口或使用用户真实文件。
func desktopFixture(t *testing.T) (*Desktop, context.Context) {
	t.Helper()
	dir := t.TempDir()
	return NewDesktop(nil, New(testConfig(), dir), dir), desktopContext("test-window")
}

// TestDesktopCapabilitiesAreOptIn 验证第二阶段能力不会随可信来源自动扩散，显式来源授权仍然可以覆盖本地能力清单。
func TestDesktopCapabilitiesAreOptIn(t *testing.T) {
	config := testConfig()
	config.DesktopCapabilities = []string{"files", "downloads"}
	p := NewPolicy(config)
	if slices.Contains(p.Grants("https://app.test"), "files") {
		t.Fatal("remote origin inherited local file permission")
	}
	if !slices.Contains(p.Grants("http://wails.localhost:9245"), "files") {
		t.Fatal("explicit local permission missing")
	}
	config.Permissions = map[string][]string{"https://app.test": {"host", "clipboard"}, "http://wails.localhost:9245": {}}
	if !slices.Contains(p.Grants("https://app.test"), "clipboard") || len(p.Grants("http://wails.localhost:9245")) != 0 {
		t.Fatal("explicit permission override ignored")
	}
}

// TestFileGrantsEnforceOwnershipAndBounds 验证读写、跨窗口隔离、大小限制、子目录跳转拒绝与凭证撤销。
func TestFileGrantsEnforceOwnershipAndBounds(t *testing.T) {
	d, ctx := desktopFixture(t)
	path := filepath.Join(d.dir, "report.txt")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	readonly, err := d.grant(ctx, path, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.WriteFile(ctx, readonly.Token, []byte("changed")); err == nil {
		t.Fatal("readonly file was overwritten")
	}
	if _, err = d.ReadFile(desktopContext("other-window"), readonly.Token); err == nil {
		t.Fatal("cross-window file read allowed")
	}
	writable, err := d.grant(ctx, path, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.WriteFile(ctx, writable.Token, []byte("导出内容")); err != nil {
		t.Fatal(err)
	}
	data, err := d.ReadFile(ctx, readonly.Token)
	if err != nil || string(data) != "导出内容" {
		t.Fatalf("read failed: %s %v", data, err)
	}
	if err = d.WriteFile(ctx, writable.Token, make([]byte, maxFileBytes+1)); err == nil {
		t.Fatal("oversized write accepted")
	}
	if err = os.WriteFile(path, make([]byte, maxFileBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = d.ReadFile(ctx, readonly.Token); err == nil {
		t.Fatal("oversized read accepted")
	}
	directory, err := d.grant(ctx, d.dir, true, false)
	if err != nil {
		t.Fatal(err)
	}
	items, err := d.ListDirectory(ctx, directory.Token)
	if err != nil || len(items) != 1 {
		t.Fatalf("directory listing: %v %v", items, err)
	}
	if _, err = d.OpenChild(ctx, directory.Token, "../report.txt"); err == nil {
		t.Fatal("directory traversal accepted")
	}
	child, err := d.OpenChild(ctx, directory.Token, "report.txt")
	if err != nil || child.Writable {
		t.Fatalf("unexpected child permission: %+v %v", child, err)
	}
	d.windowClosed("test-window")
	if _, err = d.ReadFile(ctx, readonly.Token); err == nil {
		t.Fatal("closed window retained file permission")
	}
}

// awaitDownload 等待后台任务进入终态，测试超时立即失败；只读取快照，不与工作协程直接共享可变字段。
func awaitDownload(t *testing.T, d *Desktop, ctx context.Context, id string) DownloadInfo {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, item := range d.ListDownloads(ctx) {
			if item.ID == id && item.State != "running" {
				return item
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("download did not finish")
	return DownloadInfo{}
}

// TestDownloadsCommitCancelRetryAndClean 使用本地 HTTP 服务验证完整下载、取消、重试、失败不覆盖原文件和退出保护释放。
func TestDownloadsCommitCancelRetryAndClean(t *testing.T) {
	d, ctx := desktopFixture(t)
	// 慢响应先返回请求头再等待取消，用来验证取消确实中断网络读取而不是仅改变界面状态。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte("完整业务文件"))
		case "/slow":
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		default:
			http.Error(w, "failed", 500)
		}
	}))
	defer server.Close()
	path := filepath.Join(d.dir, "download.txt")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	g, err := d.grant(ctx, path, false, true)
	if err != nil {
		t.Fatal(err)
	}
	task, err := d.StartDownload(ctx, server.URL+"/ok", g.Token)
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitDownload(t, d, ctx, task.ID); result.State != "completed" {
		t.Fatalf("success: %+v", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "完整业务文件" {
		t.Fatalf("partial file: %q", data)
	}
	failed, err := d.StartDownload(ctx, server.URL+"/error", g.Token)
	if err != nil {
		t.Fatal(err)
	}
	if result := awaitDownload(t, d, ctx, failed.ID); result.State != "failed" {
		t.Fatalf("expected failure: %+v", result)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "完整业务文件" {
		t.Fatal("failed download damaged existing file")
	}
	retried, err := d.RetryDownload(ctx, failed.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = awaitDownload(t, d, ctx, retried.ID)
	slow, err := d.StartDownload(ctx, server.URL+"/slow", g.Token)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Controller.Tasks()) == 0 {
		t.Fatal("download has no exit protection")
	}
	if err = d.CancelDownload(desktopContext("other-window"), slow.ID); err == nil {
		t.Fatal("cross-window cancellation allowed")
	}
	if err = d.CancelDownload(ctx, slow.ID); err != nil {
		t.Fatal(err)
	}
	if result := awaitDownload(t, d, ctx, slow.ID); result.State != "cancelled" {
		t.Fatalf("cancel result: %+v", result)
	}
	if len(d.Controller.Tasks()) != 0 {
		t.Fatal("download exit protection leaked")
	}
	if len(d.ListDownloads(desktopContext("other-window"))) != 0 {
		t.Fatal("download information leaked")
	}
	if matches, _ := filepath.Glob(filepath.Join(d.dir, ".hybrid-download-*")); len(matches) != 0 {
		t.Fatalf("temporary files leaked: %v", matches)
	}
	for _, raw := range []string{"file:///etc/passwd", "https://user:secret@example.test/file", "javascript:alert(1)"} {
		if _, err = d.StartDownload(ctx, raw, g.Token); err == nil {
			t.Fatalf("invalid URL accepted: %s", raw)
		}
	}
}

// TestPreferencesAndStartupSettings 验证偏好持久化、无效配置不覆盖磁盘及启动配置仅在重启后生效的快照。
func TestPreferencesAndStartupSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	t.Setenv("HYBRID_DATA_DIR", dir)
	t.Setenv("HYBRID_SETTINGS", path)
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := setting.New().Parse()
	if err != nil {
		t.Fatal(err)
	}
	d := NewDesktop(nil, New(config, dir), dir)
	p := Preferences{Theme: "dark", Locale: "en-US", Notifications: false}
	if _, err = d.SavePreferences(p); err != nil {
		t.Fatal(err)
	}
	fresh := NewDesktop(nil, d.Controller, dir)
	if fresh.GetPreferences() != p {
		t.Fatal("preferences were not persisted")
	}
	if _, err = d.SavePreferences(Preferences{Theme: "invalid"}); err == nil {
		t.Fatal("invalid preferences accepted")
	}
	pending := *config
	pending.Title = "新标题"
	snapshot, err := d.SaveSettings(pending)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.RestartRequired || snapshot.Active.Title == snapshot.Saved.Title || config.Title == pending.Title {
		t.Fatal("startup configuration was hot-applied")
	}
	before, _ := os.ReadFile(path)
	pending.ExternalLinks = "invalid"
	if _, err = d.SaveSettings(pending); err == nil {
		t.Fatal("invalid settings accepted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("invalid settings replaced valid configuration")
	}
	if _, err = d.ResetPreferences(); err != nil || d.GetPreferences() != defaultPreferences() {
		t.Fatal("preferences reset failed")
	}
}

// TestEntryRoutesAndImageConversion 验证系统入口不会接受外部地址、编码路径绕过，以及剪贴板位图转换保留像素。
func TestEntryRoutesAndImageConversion(t *testing.T) {
	for _, raw := range []string{"/#workspace", "#orders/42", "/orders?id=42"} {
		if err := Route(raw); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{"https://evil.test", "//evil.test", "/%2f%2fevil.test", "/../secret", "/%2e%2e/secret", "/\\evil", "javascript:alert(1)", "/\x00"} {
		if err := Route(raw); err == nil {
			t.Fatalf("invalid route accepted: %q", raw)
		}
	}
	original := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	original.Set(0, 0, color.NRGBA{R: 220, G: 10, B: 30, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, original); err != nil {
		t.Fatal(err)
	}
	dib, err := pngDIB(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	result, err := encodeDIB(dib[40:], 2, 2, 32)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := decoded.At(0, 0).RGBA()
	if r>>8 != 220 || g>>8 != 10 || b>>8 != 30 {
		t.Fatal("DIB image rows or channel order changed")
	}
	if _, err = encodeDIB([]byte{1}, 2, 2, 32); err == nil {
		t.Fatal("truncated bitmap accepted")
	}
}
