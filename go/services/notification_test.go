package services

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"hybrid-infra/go/host"
	"hybrid-infra/go/setting"
)

// notificationContext 通过真实权限中间件构造来源与窗口身份，避免测试依赖私有上下文键或真实桌面。
func notificationContext(t *testing.T, window string) context.Context {
	t.Helper()
	policy := host.NewPolicy(&setting.PkgInfo{})
	policy.SetWindowCapabilities(1, []string{"ui"}, window)
	policy.Register(&UiService{}, "ui", nil)
	request := httptest.NewRequest("POST", "http://wails.localhost/wails/runtime", strings.NewReader(`{"object":0,"args":{"methodName":"hybrid-infra/go/services.UiService.NotificationReady"}}`))
	request.Header.Set("Origin", "http://wails.localhost")
	request.Header.Set("X-Wails-Window-ID", "1")
	var ctx context.Context
	response := httptest.NewRecorder()
	policy.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { ctx = r.Context() })).ServeHTTP(response, request)
	if ctx == nil {
		t.Fatalf("could not create authorized test context: %d", response.Code)
	}
	return ctx
}

type notificationWindow struct {
	application.Window
	mu     sync.Mutex
	bounds application.Rect
	closed atomic.Int32
}

// Close 记录关闭次数，不操作真实用户窗口。
func (w *notificationWindow) Close() { w.closed.Add(1) }

// SetBounds 保存布局结果，允许测试并发重排与关闭而不依赖 Wails 主线程。
func (w *notificationWindow) SetBounds(bounds application.Rect) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.bounds = bounds
}

// Bounds 读取测试窗口位置快照，不暴露可变内部数据。
func (w *notificationWindow) Bounds() application.Rect {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.bounds
}

// TestNotificationDefaultsAndLayout 验证四秒、右上角和默认拖拽，并检查负坐标工作区、向下堆叠、换列与容量限制。
func TestNotificationDefaultsAndLayout(t *testing.T) {
	req, err := normalizeNotify(NotifyReq{Text: "完成"})
	if err != nil || req.Place != "topRight" || req.DurationSeconds != 4 || !draggableDefault(req.Draggable) {
		t.Fatalf("defaults: %+v %v", req, err)
	}
	disabled := false
	if draggableDefault(&disabled) {
		t.Fatal("explicit draggable=false ignored")
	}
	for _, req := range []NotifyReq{{}, {Text: "x", Place: "unknown"}, {Text: "x", DurationSeconds: -1}, {Text: "x", DurationSeconds: math.NaN()}, {Text: "x", DurationSeconds: 3601}} {
		if _, err := normalizeNotify(req); err == nil {
			t.Fatalf("accepted invalid notification: %+v", req)
		}
	}
	area := application.Rect{X: -1280, Y: -100, Width: 1280, Height: 900}
	for _, place := range []string{"topRight", "topLeft", "topCenter", "center", "bottomRight", "bottomLeft", "bottomCenter"} {
		positions, err := notificationBounds(area, place, 3)
		if err != nil {
			t.Fatal(err)
		}
		for i, p := range positions {
			if p.X < area.X || p.Y < area.Y || p.X+p.Width > area.X+area.Width || p.Y+p.Height > area.Y+area.Height {
				t.Fatalf("%s slot %d outside work area: %+v", place, i, p)
			}
		}
		if place == "topRight" && positions[1].Y <= positions[0].Y {
			t.Fatal("top-right notifications did not stack downward")
		}
	}
	positions, err := notificationBounds(area, "topRight", 8)
	if err != nil || positions[7].X >= positions[0].X {
		t.Fatal("overflow did not create a left column")
	}
	if _, err = notificationBounds(application.Rect{Width: 400, Height: 160}, "topRight", 2); err == nil {
		t.Fatal("work area capacity ignored")
	}
}

// TestNotificationReadyAndOwnership 验证只有通知自身可以开始计时，重复就绪不延长寿命，其他窗口不能关闭通知。
func TestNotificationReadyAndOwnership(t *testing.T) {
	w := &notificationWindow{}
	entry := &noticeEntry{id: "notice-test", origin: "http://wails.localhost", owner: "parent", window: w, duration: 100 * time.Millisecond}
	service := &UiService{notices: []*noticeEntry{entry}}
	if err := service.NotificationReady(notificationContext(t, "parent"), entry.id); err == nil {
		t.Fatal("parent started display timer")
	}
	ctx := notificationContext(t, entry.id)
	if err := service.NotificationReady(ctx, entry.id); err != nil {
		t.Fatal(err)
	}
	timer := entry.timer
	if err := service.NotificationReady(ctx, entry.id); err != nil || entry.timer != timer {
		t.Fatal("duplicate ready replaced display timer")
	}
	if err := service.CloseNotification(notificationContext(t, "other"), entry.id); err == nil {
		t.Fatal("other window closed notification")
	}
	deadline := time.Now().Add(time.Second)
	for w.closed.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if w.closed.Load() != 1 {
		t.Fatal("display timer did not close exactly once")
	}
	service.removeNotice(entry.id)
}

// TestNotificationReflowAndOwnerCleanup 验证中间通知关闭后收拢，手动拖离不被拉回，来源窗口关闭后清理剩余通知。
func TestNotificationReflowAndOwnerCleanup(t *testing.T) {
	area := application.Rect{Width: 1280, Height: 900}
	a, b := &notificationWindow{}, &notificationWindow{}
	first := &noticeEntry{id: "a", owner: "parent", window: a, area: area, place: "topRight", started: true}
	second := &noticeEntry{id: "b", owner: "parent", window: b, area: area, place: "topRight", started: true}
	service := &UiService{notices: []*noticeEntry{first, second}}
	service.reflowNotices()
	old := b.Bounds()
	service.noticeMu.Lock()
	service.notices = service.notices[1:]
	service.noticeMu.Unlock()
	service.reflowNotices()
	if b.Bounds().Y >= old.Y {
		t.Fatal("remaining notification did not fill released slot")
	}
	moved := b.Bounds()
	moved.X -= 50
	b.SetBounds(moved)
	service.noticeMoved("b", moved)
	service.reflowNotices()
	if b.Bounds() != moved {
		t.Fatal("dragged notification was pulled back")
	}
	service.closeOwnerNotifications("parent")
	deadline := time.Now().Add(time.Second)
	for b.closed.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if b.closed.Load() != 1 {
		t.Fatal("owner close did not release notification")
	}
}

// TestNotificationScriptAndDragData 实际执行通知填充脚本，验证文本安全、拖拽属性及顶部进度时长；关闭计时由 Go 管理。
func TestNotificationScriptAndDragData(t *testing.T) {
	script := `const assert=require('node:assert/strict');const elements=new Map();const bar={style:{}};
globalThis.document={readyState:'complete',documentElement:{dataset:{}},getElementById(id){if(!elements.has(id))elements.set(id,{});return elements.get(id)},querySelector(){return bar}};
globalThis.window={dispatchEvent(event){assert.equal(event.type,'hybrid:dialog-config')}};
globalThis.setTimeout=()=>{throw new Error('Notification should not use frontend close timer')};
` + dialogScript(dialogContent{Body: "<script>danger</script>\n纯文本", Notification: true, ID: "notice", DurationSeconds: 4, Draggable: false}) + `
assert.equal(document.documentElement.dataset.uiDraggable,'false');assert.equal(elements.get('hybrid-notify-text').textContent,'<script>danger</script>\n纯文本');assert.equal(bar.style.animation,'hybrid-countdown 4s linear forwards');assert.equal(window.__HYBRID_DIALOG_DATA__.id,'notice');`
	cmd := exec.Command("node", "-")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("notification script: %v\n%s", err, out)
	}
}
