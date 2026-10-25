package host

import (
	"encoding/json"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// TestInitialOptionsDefersDisplayMode 确认 Wails 创建阶段始终使用普通模式，目标模式延后到原生窗口定位完成再应用。
func TestInitialOptionsDefersDisplayMode(t *testing.T) {
	for _, mode := range []string{"maximized", "fullscreen"} {
		config := testConfig()
		config.WindowState = mode
		c := New(config, t.TempDir())
		options := c.InitialOptions()
		if options.StartState != application.WindowStateNormal || !c.mainStartPending.Load() {
			t.Fatalf("mode applied before native positioning: %+v", options)
		}
		if mode == "fullscreen" && c.mainStartState != application.WindowStateFullscreen || mode == "maximized" && c.mainStartState != application.WindowStateMaximised {
			t.Fatalf("desired mode lost: %v", c.mainStartState)
		}
	}
}

// TestWindowRestoreModes 验证显式模式优先、自动跟随记忆，以及旧版宽高为零的启动规则。
func TestWindowRestoreModes(t *testing.T) {
	normal := application.Rect{X: 120, Y: 80, Width: 900, Height: 650}
	areas := []application.Rect{{Width: 1920, Height: 1040}}
	for _, test := range []struct {
		name, mode string
		saved      WindowState
		remember   bool
		want       application.WindowState
	}{
		{"remember full", "auto", WindowState{Bounds: normal, Fullscreen: true}, true, application.WindowStateFullscreen},
		{"remember max", "auto", WindowState{Bounds: normal, Maximised: true}, true, application.WindowStateMaximised},
		{"force normal", "normal", WindowState{Bounds: normal, Fullscreen: true}, true, application.WindowStateNormal},
		{"force full", "fullscreen", WindowState{Bounds: normal, Maximised: true}, true, application.WindowStateFullscreen},
		{"force max", "maximized", WindowState{Bounds: normal, Fullscreen: true}, true, application.WindowStateMaximised},
		{"ignore memory", "auto", WindowState{Bounds: normal, Fullscreen: true}, false, application.WindowStateNormal},
	} {
		t.Run(test.name, func(t *testing.T) {
			restore, mode := windowRestorePlan(test.mode, 1000, 720, 800, 600, test.saved, test.remember, areas)
			if mode != test.want || restore.Fullscreen != (mode == application.WindowStateFullscreen) || restore.Maximised != (mode == application.WindowStateMaximised) {
				t.Fatalf("mode=%v restore=%+v", mode, restore)
			}
			if test.remember && restore.Bounds != normal {
				t.Fatalf("lost ordinary rectangle: %+v", restore.Bounds)
			}
		})
	}
	_, mode := windowRestorePlan("auto", 0, 0, 800, 600, WindowState{}, false, areas)
	if mode != application.WindowStateMaximised {
		t.Fatal("legacy zero size must maximize")
	}
}

// TestWindowRestoreRepairsLegacyBounds 验证旧记录的全屏尺寸配旧坐标、已移除显示器、负坐标和小屏幕均能恢复到可见区域。
func TestWindowRestoreRepairsLegacyBounds(t *testing.T) {
	for _, test := range []struct {
		name         string
		bounds, area application.Rect
	}{
		{"old fullscreen size", application.Rect{X: 500, Y: 180, Width: 1920, Height: 1080}, application.Rect{Width: 1920, Height: 1040}},
		{"removed monitor", application.Rect{X: 2800, Y: 200, Width: 1200, Height: 900}, application.Rect{Width: 1280, Height: 720}},
		{"negative monitor", application.Rect{X: -1700, Y: -100, Width: 1400, Height: 1000}, application.Rect{X: -1600, Width: 1600, Height: 900}},
		{"small display", application.Rect{X: 300, Y: 200, Width: 1200, Height: 900}, application.Rect{Width: 640, Height: 480}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, _ := windowRestorePlan("auto", 1280, 800, 800, 600, WindowState{Bounds: test.bounds}, true, []application.Rect{test.area})
			if got.Bounds.Intersect(test.area) != got.Bounds || got.Bounds.Width <= 0 || got.Bounds.Height <= 0 {
				t.Fatalf("offscreen: %+v", got)
			}
		})
	}
}

type geometryWindow struct {
	application.Window
	bounds                           application.Rect
	maximised, fullscreen, minimised bool
}

// IsMaximised 返回模拟的最大化标志，复现窗口切换而不打开真实窗口。
func (w *geometryWindow) IsMaximised() bool { return w.maximised }

// IsFullscreen 返回模拟全屏标志，验证全屏矩形不会覆盖普通矩形。
func (w *geometryWindow) IsFullscreen() bool { return w.fullscreen }

// IsMinimised 返回模拟最小化标志，验证最小化不丢失原显示模式。
func (w *geometryWindow) IsMinimised() bool { return w.minimised }

// Bounds 返回当前模式的模拟矩形，让错误采样在断言中暴露。
func (w *geometryWindow) Bounds() application.Rect { return w.bounds }

// TestCaptureKeepsNormalBoundsAcrossModes 验证普通、全屏、最小化、最大化和恢复过程中的状态能持久化并正确重读。
func TestCaptureKeepsNormalBoundsAcrossModes(t *testing.T) {
	dir := t.TempDir()
	normal := application.Rect{X: 300, Y: 120, Width: 1000, Height: 720}
	w := &geometryWindow{bounds: normal}
	c := &Controller{config: testConfig(), store: newStateStore(dir), windows: map[string]*entry{"main-window": {window: w, remember: true, normal: normal}}}
	c.capture("main-window")
	w.bounds = application.Rect{Width: 1920, Height: 1080}
	w.fullscreen = true
	c.capture("main-window")
	w.minimised, w.fullscreen = true, false
	c.capture("main-window")
	got, _ := c.store.get("main-window")
	if got.Bounds != normal || !got.Fullscreen || got.Maximised {
		t.Fatalf("fullscreen/minimize corrupted normal bounds: %+v", got)
	}
	if err := c.store.save(); err != nil {
		t.Fatal(err)
	}
	if read, _ := newStateStore(dir).get("main-window"); read != got {
		t.Fatalf("state did not roundtrip: %+v", read)
	}
	w.minimised, w.maximised = false, true
	c.capture("main-window")
	got, _ = c.store.get("main-window")
	if got.Bounds != normal || got.Fullscreen || !got.Maximised {
		t.Fatalf("maximized corrupted normal bounds: %+v", got)
	}
	w.maximised = false
	w.bounds = application.Rect{X: 80, Y: 60, Width: 950, Height: 700}
	c.capture("main-window")
	got, _ = c.store.get("main-window")
	if got.Bounds != w.bounds || got.Maximised || got.Fullscreen {
		t.Fatalf("restore failed: %+v", got)
	}
	var legacy WindowState
	if err := json.Unmarshal([]byte(`{"bounds":{"X":20,"Y":30,"Width":900,"Height":700},"maximised":true}`), &legacy); err != nil || legacy.Fullscreen || !legacy.Maximised {
		t.Fatalf("legacy state failed: %+v %v", legacy, err)
	}
}
