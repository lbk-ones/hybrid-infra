package host

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// startupFixture 创建不接触真实桌面的启动状态机，使用通道观察异步交接和资源释放。
func startupFixture(t *testing.T) (*startupScreen, chan struct{}, chan struct{}, *atomic.Int32) {
	t.Helper()
	shown := &atomic.Int32{}
	reveal := make(chan struct{}, 4)
	closed := make(chan struct{}, 4)
	screen := &startupScreen{minimum: 25 * time.Millisecond, show: func() { shown.Add(1) }, reveal: func() { reveal <- struct{}{} }, leave: func() {}, close: func() { closed <- struct{}{} }}
	t.Cleanup(screen.stop)
	return screen, reveal, closed, shown
}

// awaitStartup 在短时限内等待一次状态交接，超时直接报错，避免测试挂住。
func awaitStartup(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("startup transition timed out")
	}
}

// TestSplashWaitsForBothPages 验证主页面先完成时仍等启动屏就绪，并且重复事件只交接一次。
func TestSplashWaitsForBothPages(t *testing.T) {
	screen, reveal, closed, shown := startupFixture(t)
	screen.start(time.Second)
	screen.complete()
	select {
	case <-reveal:
		t.Fatal("main shown before splash runtime ready")
	default:
	}
	started := time.Now()
	screen.present()
	screen.present()
	awaitStartup(t, reveal)
	if time.Since(started) < screen.minimum || shown.Load() != 1 {
		t.Fatal("minimum display or duplicate presentation")
	}
	screen.complete()
	screen.finish(true)
	awaitStartup(t, closed)
	select {
	case <-reveal:
		t.Fatal("duplicate reveal")
	default:
	}
}

// TestSplashFallbackAndSkip 验证启动屏本身未就绪或被用户跳过时，主窗口仍能显示，且不会补弹启动屏。
func TestSplashFallbackAndSkip(t *testing.T) {
	for _, force := range []bool{false, true} {
		screen, reveal, closed, shown := startupFixture(t)
		if force {
			screen.finish(true)
		} else {
			screen.start(10 * time.Millisecond)
		}
		awaitStartup(t, reveal)
		awaitStartup(t, closed)
		screen.present()
		if shown.Load() != 0 {
			t.Fatal("finished splash was shown again")
		}
	}
}

// TestSplashShutdownStopsTimers 确认退出后兜底计时器不会重新显示主窗口。
func TestSplashShutdownStopsTimers(t *testing.T) {
	screen, reveal, _, _ := startupFixture(t)
	screen.start(10 * time.Millisecond)
	screen.stop()
	select {
	case <-reveal:
		t.Fatal("shutdown revealed main window")
	case <-time.After(35 * time.Millisecond):
	}
	screen.present()
	screen.complete()
	if !screen.inactive() {
		t.Fatal("shutdown state lost")
	}
}

// TestSplashConfigurationAndCaller 验证配置关闭时主窗口可见且不呈现品牌引导，启动屏控制接口拒绝其他窗口。
func TestSplashConfigurationAndCaller(t *testing.T) {
	config := testConfig()
	controller := New(config, t.TempDir())
	off := controller.InitialOptions()
	if off.Hidden || off.URL != StatusPage+"?startup=quiet" {
		t.Fatalf("disabled splash options: %+v", off)
	}
	config.SplashEnabled = true
	on := controller.InitialOptions()
	if !on.Hidden || on.URL != StatusPage {
		t.Fatal("enabled splash did not hide main window")
	}
	for _, name := range []string{"", "main-window", "business"} {
		ctx := context.WithValue(context.Background(), principalKey{}, Principal{WindowName: name})
		if _, err := controller.GetStartupState(ctx); err == nil {
			t.Fatal("startup state accessible from foreign window")
		}
		if err := controller.DismissSplash(ctx); err == nil {
			t.Fatal("foreign window dismissed splash")
		}
	}
	ctx := context.WithValue(context.Background(), principalKey{}, Principal{WindowName: SplashWindowName})
	state, err := controller.GetStartupState(ctx)
	if err != nil || state.Title != config.Title || state.Status != "starting" {
		t.Fatalf("startup metadata: %+v %v", state, err)
	}
}

// TestMainReadyAndErrorCompleteSplash 验证主页面就绪及加载错误都结束启动流程，而子窗口不能提前结束。
func TestMainReadyAndErrorCompleteSplash(t *testing.T) {
	for _, failed := range []bool{false, true} {
		controller := New(testConfig(), t.TempDir())
		screen, _, _, _ := startupFixture(t)
		controller.splash = screen
		window := &fakeWindow{name: "main-window"}
		controller.windows[window.name] = &entry{window: window, status: "loading", generation: 1}
		controller.finishStartup("other")
		screen.mu.Lock()
		early := screen.completed
		screen.mu.Unlock()
		if early {
			t.Fatal("child window ended startup")
		}
		if failed {
			controller.failLoadGeneration(window.name, "E_LOAD_TIMEOUT", "test", 1)
		} else if err := controller.PageReady(testContext(window)); err != nil {
			t.Fatal(err)
		}
		screen.mu.Lock()
		complete := screen.completed
		screen.mu.Unlock()
		if !complete {
			t.Fatal("main completion did not release splash")
		}
	}
}
