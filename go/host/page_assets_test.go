package host

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLocalPageAssets 验证从 EXE 相对目录加载资源、刷新读盘、内置路由隔离，以及拒绝路径越界和非网页文件。
func TestLocalPageAssets(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"index.html": "<h1>local-v1</h1>", "app.js": "export const local = true", "settings.json": "secret", ".private.html": "secret"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	config := testConfig()
	config.PageMode, config.LocalRoot, config.Url = "local", ".", "/index.html"
	embedded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Source", "embedded")
		_, _ = w.Write([]byte("internal:" + r.URL.Path))
	})
	assets, err := NewPageAssets(config, embedded, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer assets.Close()
	t.Chdir(t.TempDir())
	for _, requestPath := range []string{"/local/", "/local/index.html?test=1", "/local/app.js"} {
		response := httptest.NewRecorder()
		assets.ServeHTTP(response, httptest.NewRequest(http.MethodGet, requestPath, nil))
		if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Type") == "" {
			t.Fatalf("%s: %d %+v", requestPath, response.Code, response.Header())
		}
	}
	for _, requestPath := range []string{"/shell/docs.html", "/wails/runtime.js", "/Alert.html", "/"} {
		response := httptest.NewRecorder()
		assets.ServeHTTP(response, httptest.NewRequest(http.MethodGet, requestPath, nil))
		if response.Header().Get("X-Source") != "embedded" {
			t.Fatalf("internal route intercepted: %s", requestPath)
		}
	}
	for _, requestPath := range []string{"/local/settings.json", "/local/.private.html", "/local/../index.html", "/local/%2e%2e/index.html", "/local/C:/index.html", "/local/missing.html", "/local/foo\\bar.html"} {
		response := httptest.NewRecorder()
		assets.ServeHTTP(response, httptest.NewRequest(http.MethodGet, requestPath, nil))
		if response.Code != 404 {
			t.Fatalf("%s: expected 404, got %d", requestPath, response.Code)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>local-v2</h1>"), 0600); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	assets.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/local/index.html", nil))
	if !strings.Contains(response.Body.String(), "local-v2") {
		t.Fatal("edited local page was not read from disk")
	}
	response = httptest.NewRecorder()
	assets.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/local/index.html", nil))
	if response.Code != 405 {
		t.Fatal("local assets accepted a write method")
	}
	response = httptest.NewRecorder()
	assets.ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/local/index.html", nil))
	if response.Code != 200 || response.Body.Len() != 0 {
		t.Fatal("HEAD returned a body or failed")
	}
}

// TestLocalPageRootAndEntry 验证默认 web 目录、入口缺失时明确失败，以及内置/HTTP 模式不读取外部目录。
func TestLocalPageRootAndEntry(t *testing.T) {
	dir := t.TempDir()
	config := testConfig()
	config.PageMode, config.Url = "local", "/"
	if _, err := NewPageAssets(config, http.NotFoundHandler(), dir); err == nil {
		t.Fatal("missing web directory accepted")
	}
	if err := os.Mkdir(filepath.Join(dir, "web"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewPageAssets(config, http.NotFoundHandler(), dir); err == nil {
		t.Fatal("missing index.html accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, "web", "index.html"), []byte("default"), 0600); err != nil {
		t.Fatal(err)
	}
	assets, err := NewPageAssets(config, http.NotFoundHandler(), dir)
	if err != nil {
		t.Fatal(err)
	}
	assets.Close()
	for _, mode := range []string{"builtin", "http"} {
		config.PageMode = mode
		assets, err := NewPageAssets(config, http.NotFoundHandler(), filepath.Join(dir, "does-not-exist"))
		if err != nil || assets.root != nil {
			t.Fatalf("%s tried to open local files: %v", mode, err)
		}
		assets.Close()
	}
}

// TestLocalPageSymlinkCannotEscape 验证符号链接不能把外部文件带进静态目录；系统不允许创建链接时单独跳过本项。
func TestLocalPageSymlinkCannotEscape(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("local"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.html"), []byte("outside secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.html"), filepath.Join(dir, "linked.html")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	config := testConfig()
	config.PageMode, config.LocalRoot, config.Url = "local", ".", "/index.html"
	assets, err := NewPageAssets(config, http.NotFoundHandler(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer assets.Close()
	response := httptest.NewRecorder()
	assets.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/local/linked.html", nil))
	if response.Code != 404 {
		t.Fatal("symbolic link escaped local root")
	}
}

// TestLocalPageActivationRoutes 验证协议、通知和快捷键路由继续指向外置目录，hash 路由保留原入口文件。
func TestLocalPageActivationRoutes(t *testing.T) {
	config := testConfig()
	config.PageMode, config.LocalRoot, config.Url = "local", ".", "/index.html"
	controller := New(config, t.TempDir())
	window := &fakeWindow{name: "main-window"}
	controller.windows[window.name] = &entry{window: window, status: "ready", target: config.PageURL()}
	defer func() {
		if timer := controller.windows[window.name].timer; timer != nil {
			timer.Stop()
		}
	}()
	for route, expected := range map[string]string{"#workspace": "/local/index.html#workspace", "/details.html?id=1": "/local/details.html?id=1", "/": "/local/"} {
		if err := controller.ActivateRoute(route); err != nil {
			t.Fatal(err)
		}
		if window.url.Load() != expected {
			t.Fatalf("%s: %v", route, window.url.Load())
		}
	}
}
