package host

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
	"hybrid-infra/go/setting"
	"hybrid-infra/go/utils"
)

type testAPI struct{}

// Ping 提供不执行实际工作的测试方法，用于验证方法名称和方法 ID 的权限检查。
func (*testAPI) Ping() {}

// testConfig 构造宿主测试使用的固定配置，避免测试依赖开发机上的真实用户配置。
func testConfig() *setting.PkgInfo {
	return &setting.PkgInfo{Title: "Test", Url: "https://app.test/portal", CloseBehavior: "exit", ExternalLinks: "browser", RememberWindows: true, MinWidth: 800, MinHeight: 600, LoadTimeoutSeconds: 3}
}

// rpc 构造本地 RPC 请求并经过权限中间件，返回响应状态和下游是否被调用，供授权测试复用。
func rpc(t *testing.T, p *Policy, origin, body string, mutate func(*http.Request)) (int, bool) {
	t.Helper()
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if err := Require(r.Context(), "host"); err != nil {
			t.Error(err)
		}
		w.WriteHeader(204)
	})
	req := httptest.NewRequest("POST", "http://wails.localhost/wails/runtime", strings.NewReader(body))
	req.Header.Set("Origin", origin)
	if mutate != nil {
		mutate(req)
	}
	response := httptest.NewRecorder()
	p.Middleware(next).ServeHTTP(response, req)
	return response.Code, called
}

// TestRuntimePolicyCannotBeBypassedByRawBindingsOrRuntime 验证直接调用方法 ID、方法名或原始 runtime 不能绕过来源和能力检查，同时检查调试、分块及请求大小限制。
func TestRuntimePolicyCannotBeBypassedByRawBindingsOrRuntime(t *testing.T) {
	config := testConfig()
	config.Permissions = map[string][]string{"https://limited.test": {"host"}, "https://denied.test": {}}
	p := NewPolicy(config)
	p.Register(&testAPI{}, "store", nil)
	method := "hybrid-infra/go/host.testAPI.Ping"
	hash := fnv.New32a()
	hash.Write([]byte(method))
	id := hash.Sum32()
	tests := []struct {
		origin, body string
		allowed      bool
	}{
		{"http://wails.localhost", `{"object":8,"method":1}`, true},
		{"https://app.test", `{"object":8,"method":1}`, true},
		{"https://limited.test", `{"object":8,"method":1}`, true},
		{"https://evil.test", `{"object":8,"method":1}`, false},
		{"", `{"object":8,"method":1}`, false},
		{"null", `{"object":8,"method":1}`, false},
		{"http://wails.localhost.evil.test", `{"object":8,"method":1}`, false},
		{"https://denied.test", `{"object":8,"method":1}`, false},
		{"https://limited.test", fmt.Sprintf(`{"object":0,"method":0,"args":{"methodID":%d}}`, id), false},
		{"https://limited.test", fmt.Sprintf(`{"object":0,"method":0,"args":{"methodName":%q}}`, method), false},
		{"https://app.test", fmt.Sprintf(`{"object":0,"method":0,"args":{"methodID":%d}}`, id), true},
		{"https://app.test", `{"object":0,"method":0,"args":{"methodID":1}}`, false},
		{"https://app.test", `{"object":2,"method":2}`, false},
		{"https://app.test", `{"object":1,"method":0}`, false},
		{"https://app.test", `{"object":5,"method":0}`, false},
		{"https://app.test", fmt.Sprintf(`{"object":6,"method":%d}`, application.WindowOpenDevTools), false},
	}
	for _, tc := range tests {
		status, called := rpc(t, p, tc.origin, tc.body, nil)
		if called != tc.allowed {
			t.Errorf("origin=%s body=%s status=%d called=%v", tc.origin, tc.body, status, called)
		}
	}
	status, called := rpc(t, p, "https://app.test", `{"object":8,"method":1}`, func(r *http.Request) { r.Header.Set("X-Wails-Chunk-ID", "attempt") })
	if status != 413 || called {
		t.Fatal("chunked calls bypassed inspection")
	}
	status, called = rpc(t, p, "https://app.test", strings.Repeat("x", 1024*1024+1), nil)
	if status != 413 || called {
		t.Fatal("oversized request accepted")
	}
	if err := Require(context.Background(), "host"); err == nil {
		t.Fatal("non-HTTP transport inherited privileges without authorization")
	}
}

// TestNavigationPolicy 验证应用内导航与外链的区分，拒绝危险协议，并确认仅允许导航的来源不会自动得到 RPC 能力。
func TestNavigationPolicy(t *testing.T) {
	config := testConfig()
	config.NavigationOrigins = []string{"https://readonly.test"}
	p := NewPolicy(config)
	for _, raw := range []string{"/", "/shell/manage.html", "https://app.test/details", "https://readonly.test/path", "http://wails.localhost:9245/", "wails://wails.localhost/"} {
		inside, err := p.Navigation(raw)
		if err != nil || !inside {
			t.Errorf("trusted URL %q: %v %v", raw, inside, err)
		}
	}
	if grants := p.Grants("https://readonly.test"); len(grants) != 0 {
		t.Fatal("navigation permission granted RPC permissions")
	}
	if inside, err := p.Navigation("https://outside.test/"); err != nil || inside {
		t.Fatal("external HTTP URL not classified as browser link")
	}
	for _, raw := range []string{"javascript:alert(1)", "file:///secret", "//evil.test", "https://user:pass@app.test", "/\\evil.test", "data:text/html,hi"} {
		if _, err := p.Navigation(raw); err == nil {
			t.Errorf("accepted unsafe URL %q", raw)
		}
	}
}

// TestRestoreWindowAfterMonitorRemoved 验证显示器移除、负坐标、超大尺寸和小工作区下的窗口恢复，确保窗口仍处于可见区域。
func TestRestoreWindowAfterMonitorRemoved(t *testing.T) {
	areas := []application.Rect{{X: 0, Y: 0, Width: 1920, Height: 1040}, {X: -1600, Y: 0, Width: 1600, Height: 900}}
	for _, rect := range []application.Rect{{X: 5000, Y: 3000, Width: 1000, Height: 800}, {X: -1800, Y: -200, Width: 900, Height: 600}, {X: 0, Y: 0, Width: 6000, Height: 4000}, {Width: -1, Height: -1}} {
		fitted := FitBounds(rect, areas, 800, 600)
		visible := false
		for _, area := range areas {
			if fitted.Intersect(area) == fitted {
				visible = true
			}
		}
		if !visible || fitted.Width <= 0 || fitted.Height <= 0 {
			t.Errorf("window was not placed in a work area: %+v", fitted)
		}
	}
	small := FitBounds(application.Rect{Width: 1280, Height: 800}, []application.Rect{{Width: 640, Height: 480}}, 800, 600)
	if small.Width != 640 || small.Height != 480 {
		t.Fatal("minimum size prevented fitting a smaller display")
	}
}

// TestStateWritesAtomicallyAndRecoversCorruption 验证窗口状态可多次替换保存并重新读取，损坏文件不会被当作有效状态继续使用。
func TestStateWritesAtomicallyAndRecoversCorruption(t *testing.T) {
	dir := t.TempDir()
	store := newStateStore(dir)
	wanted := WindowState{application.Rect{X: -800, Y: 40, Width: 800, Height: 600}, true}
	store.put("main-window", wanted)
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	store.put("detail", wanted)
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	if got, ok := newStateStore(dir).get("main-window"); !ok || got != wanted {
		t.Fatalf("state not restored: %+v", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "window-state.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	corrupted := newStateStore(dir)
	if _, ok := corrupted.get("main-window"); ok {
		t.Fatal("invalid state reused")
	}
}

type fakeWindow struct {
	application.Window
	name    string
	focused atomic.Int32
	url     atomic.Value
}

// Name 返回测试窗口名，用它模拟宿主登记和任务归属所需的窗口身份。
func (w *fakeWindow) Name() string { return w.name }

// IsMinimised 让测试窗口始终处于未最小化状态，避免测试依赖真实窗口系统。
func (w *fakeWindow) IsMinimised() bool { return false }

// IsMaximised 让测试窗口始终处于普通状态，便于验证窗口信息查询和复用。
func (w *fakeWindow) IsMaximised() bool { return false }

// IsVisible 让测试窗口固定返回可见，用于构造不依赖原生环境的窗口快照。
func (w *fakeWindow) IsVisible() bool { return true }

// Show 模拟显示窗口并返回自身，不实际创建操作系统窗口。
func (w *fakeWindow) Show() application.Window { return w }

// Focus 原子记录聚焦次数，用于检查并发复用时每次请求是否都唤醒同一个窗口。
func (w *fakeWindow) Focus() { w.focused.Add(1) }

// SetURL 记录测试中要求加载的地址，用于确认失败时是否转入本地错误页。
func (w *fakeWindow) SetURL(url string) application.Window { w.url.Store(url); return w }

// Bounds 返回固定的测试窗口尺寸，让位置查询可以在没有真实桌面的情况下运行。
func (w *fakeWindow) Bounds() application.Rect { return application.Rect{Width: 1000, Height: 720} }

// testContext 把测试窗口放入 Wails 调用上下文，模拟 Go 服务收到的窗口信息。
func testContext(w application.Window) context.Context {
	return context.WithValue(context.Background(), application.WindowKey, w)
}

// TestExistingWindowIDIsReusedConcurrently 验证并发打开相同 ID 时只复用已有窗口，并为每次请求聚焦该窗口。
func TestExistingWindowIDIsReusedConcurrently(t *testing.T) {
	controller := New(testConfig(), t.TempDir())
	w := &fakeWindow{name: "detail"}
	controller.windows[w.name] = &entry{window: w, options: utils.NewWindowOptions{Name: w.name, Url: "/details"}, status: "ready"}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			info, err := controller.Open(utils.NewWindowOptions{Name: "detail", Url: "/details"})
			if err != nil || info.ID != "detail" {
				t.Errorf("reuse failed: %v %v", info, err)
			}
		})
	}
	wg.Wait()
	if len(controller.windows) != 1 || w.focused.Load() != 20 {
		t.Fatal("reused window was not consistently focused")
	}
}

// TestTaskOwnershipExitGuardAndShutdownRace 验证任务只允许所属窗口释放、有任务时拒绝退出，以及退出开始后不能再登记新任务。
func TestTaskOwnershipExitGuardAndShutdownRace(t *testing.T) {
	controller := New(testConfig(), t.TempDir())
	owner := testContext(&fakeWindow{name: "owner"})
	other := testContext(&fakeWindow{name: "other"})
	task, err := controller.BeginTask(owner, "导出文件")
	if err != nil {
		t.Fatal(err)
	}
	if err = controller.Quit(); err == nil {
		t.Fatal("quit ignored unfinished task")
	}
	if err = controller.EndTask(other, task.ID); err == nil {
		t.Fatal("another window released the task")
	}
	if err = controller.Close("missing"); err == nil {
		t.Fatal("missing window accepted")
	}
	if err = controller.EndTask(owner, task.ID); err != nil {
		t.Fatal(err)
	}
	if len(controller.Tasks()) != 0 {
		t.Fatal("completed task retained")
	}
	controller.quitting.Store(true)
	if _, err = controller.BeginTask(owner, "too late"); err == nil {
		t.Fatal("task started during shutdown")
	}
	if _, err = controller.BeginTask(context.Background(), "unowned"); err == nil {
		t.Fatal("task without window accepted")
	}
	data := MarshalError(Fail("E_BUSY", "未完成"))
	var value Error
	if json.Unmarshal(data, &value) != nil || value.Code != "E_BUSY" {
		t.Fatal("structured error lost")
	}
}

// TestLoadWatchdogDoesNotOverwriteNewerNavigation 验证旧加载计时器不会覆盖新加载或已完成页面，超时后的迟到就绪通知也不会隐藏错误页。
func TestLoadWatchdogDoesNotOverwriteNewerNavigation(t *testing.T) {
	controller := New(testConfig(), t.TempDir())
	w := &fakeWindow{name: "main-window"}
	e := &entry{window: w, status: "loading", generation: 2}
	controller.windows[w.name] = e
	controller.failLoadGeneration(w.name, "E_LOAD_TIMEOUT", "old request", 1)
	if e.status != "loading" {
		t.Fatal("stale timeout overwrote a newer request")
	}
	controller.NativeMessage(w, `hybrid:{"type":"ready"}`, &application.OriginInfo{Origin: "https://app.test/page", TopOrigin: "https://app.test/page"})
	if e.status != "ready" {
		t.Fatal("native load completion was ignored")
	}
	controller.failLoadGeneration(w.name, "E_LOAD_TIMEOUT", "completed request", 2)
	if e.status != "ready" {
		t.Fatal("timer overwrote completed navigation")
	}
	e.status = "loading"
	e.generation = 3
	controller.failLoadGeneration(w.name, "E_LOAD_TIMEOUT", "current request", 3)
	if e.status != "error" || w.url.Load() != StatusPage {
		t.Fatal("timed out page did not recover to the local status page")
	}
	controller.NativeMessage(w, `hybrid:{"type":"ready"}`, &application.OriginInfo{Origin: "https://app.test/page", TopOrigin: "https://app.test/page"})
	if e.status != "error" {
		t.Fatal("a late completion hid the error page")
	}
}

// TestNativeMessagesUseHostProvidedOrigins 验证页面自报来源不能替代原生来源元数据，并拒绝来自非应用来源的导航结果。
func TestNativeMessagesUseHostProvidedOrigins(t *testing.T) {
	controller := New(testConfig(), t.TempDir())
	w := &fakeWindow{name: "main-window"}
	e := &entry{window: w, status: "loading"}
	controller.windows[w.name] = e
	controller.NativeMessage(w, `hybrid:{"type":"ready","origin":"https://app.test"}`, &application.OriginInfo{Origin: "https://evil.test", TopOrigin: "https://app.test"})
	if e.status != "loading" {
		t.Fatal("untrusted child-frame message changed host state")
	}
	controller.NativeMessage(w, `hybrid:{"type":"ready"}`, &application.OriginInfo{Origin: "https://evil.test", TopOrigin: "https://evil.test"})
	if e.status != "error" || e.loadError.Code != "E_NAVIGATION_DENIED" {
		t.Fatal("unexpected navigation was allowed")
	}
}

// TestWindowPermissionCeilingPreventsLocalPageEscalation 验证受限子窗口加载本地页面后仍受创建者权限上限限制，关闭或未登记的窗口不能继续使用能力。
func TestWindowPermissionCeilingPreventsLocalPageEscalation(t *testing.T) {
	p := NewPolicy(testConfig())
	p.Register(&testAPI{}, "store", nil)
	p.SetWindowCapabilities(42, []string{"host", "windows", "ui.html"})
	method := "hybrid-infra/go/host.testAPI.Ping"
	hash := fnv.New32a()
	hash.Write([]byte(method))
	body := fmt.Sprintf(`{"object":0,"method":0,"args":{"methodID":%d}}`, hash.Sum32())
	status, called := rpc(t, p, "http://wails.localhost", body, func(r *http.Request) { r.Header.Set("X-Wails-Window-ID", "42") })
	if status != 403 || called {
		t.Fatal("restricted child window inherited local storage privileges")
	}
	status, called = rpc(t, p, "http://wails.localhost", `{"object":8,"method":1}`, func(r *http.Request) { r.Header.Set("X-Wails-Window-ID", "42") })
	if status != 204 || !called {
		t.Fatal("child lost granted host capability")
	}
	p.ForgetWindow(42)
	if len(p.WindowGrants("42", "http://wails.localhost")) != 0 {
		t.Fatal("closed window retained capabilities")
	}
	if len(p.WindowGrants("unknown", "http://wails.localhost")) != 0 {
		t.Fatal("unregistered window inherited local capabilities")
	}
}

// TestNativeCallerIdentityIgnoresTargetWindowName 验证请求中指定的目标窗口名不能冒充真实调用窗口，任务归属仍采用原生窗口 ID。
func TestNativeCallerIdentityIgnoresTargetWindowName(t *testing.T) {
	p := NewPolicy(testConfig())
	p.SetWindowCapabilities(42, Capabilities, "source-window")
	request := httptest.NewRequest("POST", "http://wails.localhost/wails/runtime", strings.NewReader(`{"object":8,"method":1}`))
	request.Header.Set("Origin", "http://wails.localhost")
	request.Header.Set("X-Wails-Window-ID", "42")
	request.Header.Set("X-Wails-Window-Name", "other-window")
	response := httptest.NewRecorder()
	p.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if caller(r.Context()) != "source-window" {
			t.Fatal("target header replaced native caller identity")
		}
		w.WriteHeader(204)
	})).ServeHTTP(response, request)
	if response.Code != 204 {
		t.Fatalf("request failed: %d", response.Code)
	}
}
