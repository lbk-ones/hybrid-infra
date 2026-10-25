package host

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"hybrid-infra/go/setting"
	"hybrid-infra/go/utils"
)

// Version 可在发布构建时通过 -X hybrid-infra/go/host.Version=版本号 注入，普通开发构建保持当前基线。
var Version = "0.1.0"

const ProtocolVersion = 1
const StatusPage = "/shell/status.html"

var validID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,79}$`)

// WindowInfo 是返回给前端的窗口快照，包含窗口身份、原生尺寸和页面加载状态。
type WindowInfo struct {
	ID         string           `json:"id"`
	Title      string           `json:"title"`
	URL        string           `json:"url"`
	Visible    bool             `json:"visible"`
	Maximised  bool             `json:"maximised"`
	Fullscreen bool             `json:"fullscreen"`
	Bounds     application.Rect `json:"bounds"`
	Status     string           `json:"status"`
	Error      *Error           `json:"error,omitempty"`
}

// entry 保存一个窗口的内部管理状态，不直接暴露给前端。
type entry struct {
	window        application.Window
	options       utils.NewWindowOptions
	status        string
	loadError     *Error
	target        string
	timer         *time.Timer
	generation    uint64           // 当前加载编号，用于识别过期的超时回调。
	remember      bool             // 是否需要保存该窗口的位置和尺寸，临时弹框不保存。
	normal        application.Rect // 最近一次普通窗口状态的矩形，最大化时也保留它。
	closeApproved bool             // 用户是否已经确认关闭有任务的子窗口。
}

// Task 表示一个尚未完成的业务任务；这里只登记退出保护，不负责执行业务工作。
type Task struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	WindowID  string    `json:"windowId"`
	CreatedAt time.Time `json:"createdAt"`
}

// Controller 统一管理桌面宿主的窗口、页面加载、任务退出保护和状态保存。
// 前端通过 go/services 的权限入口间接使用它，不直接绑定本类型的全部方法。
type Controller struct {
	app               *application.App
	config            *setting.PkgInfo
	dataDir           string
	taskCenter        *TaskCenter
	Policy            *Policy
	store             *stateStore
	mu                sync.RWMutex // 保护窗口登记、加载状态和任务列表，不应在持锁时调用原生窗口接口。
	openMu            sync.Mutex   // 串行处理窗口创建与复用，防止相同 ID 被并发创建两次。
	windows           map[string]*entry
	tasks             map[string]Task
	sequence          atomic.Uint64
	quitting          atomic.Bool
	promptOpen        atomic.Bool
	mainStartPending  atomic.Bool // 原生窗口先定位，再应用启动模式；期间不把过渡普通状态写入记忆。
	mainStartState    application.WindowState
	tray              *application.SystemTray
	trayStatus        *application.MenuItem
	trayTasks         int
	closeHandlers     []func(string)
	nativeHandlers    []func(application.Window, string, *application.OriginInfo) bool
	mainReadyHandlers []func()
	icons             *IconManager
	splash            *startupScreen
}

// New 创建宿主管理器，读取已有窗口状态，并初始化窗口、任务和权限登记表。此时尚未绑定 Wails 应用。
func New(config *setting.PkgInfo, dir string) *Controller {
	return &Controller{config: config, dataDir: dir, Policy: NewPolicy(config), store: newStateStore(dir), windows: map[string]*entry{}, tasks: map[string]Task{}}
}

// DataDirectory 返回启动时固定的数据目录，服务注册和运行中的设置读写不重新解析部署身份。
func (c *Controller) DataDirectory() string { return c.dataDir }

// AttachTaskCenter 在运行前接入统一任务观察器，并按真实窗口关闭事件清理排队、回调租约和内存重试参数。
func (c *Controller) AttachTaskCenter(center *TaskCenter) {
	c.taskCenter = center
	c.OnResourceWindowClosed(center.CloseWindow)
}

// Attach 绑定 Wails 应用，监听启动、显示器变化和系统唤醒事件；应用退出时停止加载计时器并保存窗口状态。
func (c *Controller) Attach(app *application.App) {
	c.app = app
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		c.restoreMainStart(false)
		c.clampAll()
	})
	app.Event.OnApplicationEvent(events.Mac.ApplicationDidChangeScreenParameters, func(*application.ApplicationEvent) { c.clampAll() })
	app.Event.OnApplicationEvent(events.Common.SystemDidWake, func(*application.ApplicationEvent) { c.clampAll() })
	app.OnShutdown(func() {
		c.quitting.Store(true)
		c.mu.Lock()
		for _, e := range c.windows {
			if e.timer != nil {
				e.timer.Stop()
			}
		}
		c.mu.Unlock()
		if c.config.RememberWindows {
			if err := c.store.save(); err != nil {
				app.Logger.Error("save window state", "error", err)
			}
		}
	})
}

// Config 返回宿主当前使用的配置，供应用启动及服务装配时查询。
func (c *Controller) Config() *setting.PkgInfo { return c.config }

// OnResourceWindowClosed 在应用启动阶段登记资源清理函数；窗口从管理表移除、释放窗口锁后调用，避免资源锁与窗口锁相互等待。
func (c *Controller) OnResourceWindowClosed(handler func(string)) {
	c.closeHandlers = append(c.closeHandlers, handler)
}

// OnNativeMessageHandler 在启动阶段登记内部原生页面消息处理器；统一校验顶层来源后才调用，处理器仍须验证窗口和随机会话凭证。
func (c *Controller) OnNativeMessageHandler(handler func(application.Window, string, *application.OriginInfo) bool) {
	c.nativeHandlers = append(c.nativeHandlers, handler)
}

// OnMainRuntimeReady 在启动阶段登记本地 WebView 就绪回调，用于更新后的健康确认，不依赖远程业务站点是否在线。
func (c *Controller) OnMainRuntimeReady(handler func()) {
	c.mainReadyHandlers = append(c.mainReadyHandlers, handler)
}

// reserveMaintenance 拒绝有业务任务时重启并阻止新任务；准备失败时调用返回函数撤销保留。
func (c *Controller) reserveMaintenance() (func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.tasks) > 0 || c.quitting.Load() {
		return nil, Fail("E_BUSY", "仍有业务任务或应用正在退出，无法执行维护重启")
	}
	c.quitting.Store(true)
	return func() { c.quitting.Store(false) }, nil
}

// EnvironmentInfo 区分真正由原生窗口发起的调用与普通浏览器连接；不能仅凭页面地址或 JavaScript 全局变量确认环境。
type EnvironmentInfo struct {
	Kind            string `json:"kind"`
	IsDesktopShell  bool   `json:"isDesktopShell"`
	Platform        string `json:"platform"`
	Version         string `json:"version"`
	ProtocolVersion int    `json:"protocolVersion"`
}

// Environment 使用中间件保存的原生窗口身份返回环境证明；普通浏览器接入 HTTP 桥时标为 browser-connected。
func (c *Controller) Environment(ctx context.Context) EnvironmentInfo {
	p, _ := ctx.Value(principalKey{}).(Principal)
	native := p.WindowName != ""
	kind := "browser-connected"
	if native {
		kind = "desktop-shell"
	}
	return EnvironmentInfo{Kind: kind, IsDesktopShell: native, Platform: runtime.GOOS, Version: Version, ProtocolVersion: ProtocolVersion}
}

// areas 读取所有显示器的有效工作区，并把主显示器排在最前；恢复窗口时用它作为位置回退区域。
func (c *Controller) areas() []application.Rect {
	if c.app == nil {
		return nil
	}
	screens := append([]*application.Screen(nil), c.app.Screen.GetAll()...)
	result := make([]application.Rect, 0, len(screens))
	// 原来使用的显示器都不存在时，优先把窗口移回主显示器。
	sort.SliceStable(screens, func(i, j int) bool { return screens[i].IsPrimary && !screens[j].IsPrimary })
	for _, s := range screens {
		if s.WorkArea.Width > 0 && s.WorkArea.Height > 0 {
			result = append(result, s.WorkArea)
		}
	}
	return result
}

// InitialOptions 先创建普通主窗口并恢复安全矩形；启动模式交给 restoreMainStart，避开 Wails 先全屏再设置旧坐标造成的偏移。
func (c *Controller) InitialOptions() application.WebviewWindowOptions {
	saved, ok := c.store.get("main-window")
	restore, state := windowRestorePlan(c.config.WindowState, c.config.Width, c.config.Height, c.config.MinWidth, c.config.MinHeight, saved, ok && c.config.RememberWindows, c.areas())
	c.mainStartState = state
	c.mainStartPending.Store(true)
	options := application.WebviewWindowOptions{Name: "main-window", Title: c.config.Title, Width: restore.Bounds.Width, Height: restore.Bounds.Height, MinWidth: min(c.config.MinWidth, restore.Bounds.Width), MinHeight: min(c.config.MinHeight, restore.Bounds.Height), Frameless: c.config.Frameless, StartState: application.WindowStateNormal, URL: StatusPage, BackgroundColour: application.NewRGB(246, 248, 247), Permissions: webPermissions()}
	options.Hidden = c.config.SplashEnabled
	if !c.config.SplashEnabled {
		options.URL = StatusPage + "?startup=quiet"
	}
	if ok && c.config.RememberWindows {
		options.X = restore.Bounds.X
		options.Y = restore.Bounds.Y
		options.InitialPosition = application.WindowXY
	}
	options.KeyBindings = map[string]func(application.Window){
		"F5":      func(w application.Window) { c.retryFromUI(w) },
		"ctrl+f5": func(w application.Window) { c.retryFromUI(w) },
		// 进入全屏前先保存普通矩形，F11 再次按下时恢复原窗口。
		"F11": func(w application.Window) {
			if w.IsFullscreen() {
				w.UnFullscreen()
			} else {
				c.capture(w.Name())
				w.Fullscreen()
			}
		},
		"F12": func(w application.Window) {
			if c.config.Debug {
				w.OpenDevTools()
			}
		},
	}
	return options
}

// restoreMainStart 等原生窗口定位完成后再应用启动模式；启用启动屏时延迟到交接显示，避免全屏提前盖住启动屏。
func (c *Controller) restoreMainStart(reveal bool) {
	if !c.mainStartPending.Load() || c.config.SplashEnabled && !reveal && (c.splash == nil || !c.splash.inactive()) {
		return
	}
	e := c.getEntry("main-window")
	if e == nil {
		return
	}
	// 几何修正和模式应用放在同一 UI 回合，异步窗口事件不能插入一半新、一半旧的启动状态。
	application.InvokeSync(func() {
		if !c.mainStartPending.Load() {
			return
		}
		// ApplicationStarted 可能早于异步创建原生窗口；此时不能调用 Fullscreen，否则 Wails 会改回有问题的 StartState 路径。
		if e.window.NativeWindow() == nil {
			return
		}
		c.clampOne(e)
		normal := e.window.Bounds()
		c.mu.Lock()
		e.normal = normal
		c.mu.Unlock()
		c.mainStartPending.Store(false)
		switch c.mainStartState {
		case application.WindowStateMaximised:
			e.window.Maximise()
		case application.WindowStateFullscreen:
			e.window.Fullscreen()
		}
		c.capture("main-window")
	})
}

// webPermissions 设置网页请求摄像头、麦克风等系统能力时使用平台默认处理方式；这与 Go 服务的能力授权相互独立。
func webPermissions() map[application.PermissionType]application.Permission {
	return map[application.PermissionType]application.Permission{application.PermissionMicrophone: application.PermissionDefault, application.PermissionCamera: application.PermissionDefault, application.PermissionGeolocation: application.PermissionDefault, application.PermissionNotifications: application.PermissionDefault, application.PermissionClipboardRead: application.PermissionDefault}
}

// TrackMain 将主窗口纳入统一管理，并接管关闭按钮：按配置隐藏到托盘，或在检查未完成任务后退出应用。
func (c *Controller) TrackMain(w application.Window) {
	width, height := c.config.Width, c.config.Height
	if width <= 0 || height <= 0 {
		width, height = 1280, 800
	}
	c.track(w, utils.NewWindowOptions{Name: w.Name(), Title: c.config.Title, Url: c.config.PageURL(), Width: width, Height: height}, true, true)
	w.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if c.quitting.Load() {
			return
		}
		e.Cancel()
		if c.config.CloseBehavior == "hide" {
			w.Hide()
			return
		}
		go c.RequestQuitWithPrompt()
	})
}

// track 登记窗口及其权限上限，并安装加载、尺寸变化和关闭事件处理。
// remember 决定是否记忆窗口状态，load 决定是否从本地加载页启动；ceilings 指定继承的能力上限。
func (c *Controller) track(w application.Window, options utils.NewWindowOptions, remember, load bool, ceilings ...[]string) {
	ceiling := Capabilities
	if len(ceilings) > 0 {
		ceiling = ceilings[0]
	}
	c.Policy.SetWindowCapabilities(w.ID(), ceiling, w.Name())
	e := &entry{window: w, options: options, status: "ready", target: options.Url, remember: remember}
	if load {
		e.status = "starting"
	}
	e.normal = application.Rect{Width: max(options.Width, 800), Height: max(options.Height, 600)}
	if saved, ok := c.store.get(w.Name()); ok && remember && c.config.RememberWindows && saved.Bounds.Width > 0 && saved.Bounds.Height > 0 {
		e.normal = FitBounds(saved.Bounds, c.areas(), 320, 240)
	}
	if w.Name() == "main-window" {
		saved, ok := c.store.get(w.Name())
		restore, _ := windowRestorePlan(c.config.WindowState, c.config.Width, c.config.Height, c.config.MinWidth, c.config.MinHeight, saved, ok && c.config.RememberWindows, c.areas())
		e.normal = restore.Bounds
	}
	c.mu.Lock()
	c.windows[w.Name()] = e
	c.mu.Unlock()
	w.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		if w.Name() == "main-window" {
			c.restoreMainStart(false)
		}
		if c.icons != nil {
			c.icons.ApplyWindow(w)
		}
		if w.Name() == "main-window" {
			for _, handler := range c.mainReadyHandlers {
				handler()
			}
		}
		c.mu.RLock()
		initial := e.status == "starting"
		c.mu.RUnlock()
		if initial {
			go func() { _ = c.Retry(w.Name(), false) }()
		} else {
			w.ExecJS(guardScript)
		}
	})
	for _, event := range []events.WindowEventType{events.Windows.WebViewNavigationCompleted, events.Mac.WebViewDidFinishNavigation, events.Linux.WindowLoadFinished} {
		w.OnWindowEvent(event, func(*application.WindowEvent) { w.ExecJS(guardScript) })
	}
	for _, event := range []events.WindowEventType{events.Common.WindowDidMove, events.Common.WindowDidResize, events.Common.WindowMaximise, events.Common.WindowUnMaximise, events.Common.WindowFullscreen, events.Common.WindowUnFullscreen, events.Common.WindowRestore, events.Common.WindowDPIChanged} {
		w.OnWindowEvent(event, func(*application.WindowEvent) {
			if w.Name() == "main-window" {
				c.restoreMainStart(false)
			}
			if event == events.Common.WindowDPIChanged || event == events.Common.WindowUnFullscreen || event == events.Common.WindowUnMaximise || event == events.Common.WindowRestore {
				c.clampOne(e)
			} else {
				c.capture(w.Name())
			}
		})
	}
	w.OnWindowEvent(events.IOS.WebViewDidFailNavigation, func(*application.WindowEvent) {
		c.failLoad(w.Name(), "E_LOAD_FAILED", "页面加载失败，可重试或返回首页")
	})
	// 关闭钩子早于 Wails 的内置销毁监听执行，因此仍可读取尺寸并阻止带任务的窗口关闭。
	w.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		c.capture(w.Name())
		if w.Name() == "main-window" || c.quitting.Load() {
			return
		}
		c.mu.RLock()
		approved := e.closeApproved
		c.mu.RUnlock()
		if !approved && c.hasTasks(w.Name()) {
			event.Cancel()
			go c.confirmWindowClose(e)
		}
	})
	w.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		c.Policy.ForgetWindow(w.ID())
		c.mu.Lock()
		if e.timer != nil {
			e.timer.Stop()
		}
		delete(c.windows, w.Name())
		for id, task := range c.tasks {
			if task.WindowID == w.Name() {
				delete(c.tasks, id)
			}
		}
		c.mu.Unlock()
		for _, handler := range c.closeHandlers {
			handler(w.Name())
		}
		if remember && c.config.RememberWindows {
			go func() {
				if err := c.store.save(); err != nil {
					c.app.Logger.Error("save window state", "error", err)
				}
			}()
		}
	})
}

// capture 在同一次 UI 线程采样中读取模式和尺寸，避免全屏/最大化切换时拼接旧坐标与新尺寸；只更新普通矩形，最小化不覆盖已记住的模式。
func (c *Controller) capture(id string) {
	c.mu.RLock()
	e := c.windows[id]
	c.mu.RUnlock()
	if e == nil || !e.remember || !c.config.RememberWindows {
		return
	}
	if id == "main-window" && c.mainStartPending.Load() {
		return
	}
	// 原生模式和矩形必须在同一 UI 回合取得；单元测试使用无应用的假窗口，直接执行同样的采样逻辑。
	read := func() {
		maximised, fullscreen, minimised := e.window.IsMaximised(), e.window.IsFullscreen(), e.window.IsMinimised()
		var bounds application.Rect
		if !maximised && !fullscreen && !minimised {
			bounds = e.window.Bounds()
		}
		c.mu.Lock()
		if bounds.Width > 0 && bounds.Height > 0 {
			e.normal = bounds
		}
		normal := e.normal
		c.mu.Unlock()
		if minimised {
			if previous, ok := c.store.get(id); ok {
				maximised, fullscreen = previous.Maximised, previous.Fullscreen
			}
		}
		c.store.put(id, WindowState{Bounds: normal, Maximised: maximised && !fullscreen, Fullscreen: fullscreen})
	}
	if c.app != nil {
		application.InvokeSync(read)
	} else {
		read()
	}
}

// clampOne 将普通窗口的大小和位置调整到当前显示器工作区内，避免移除显示器或改变缩放后找不到窗口。
func (c *Controller) clampOne(e *entry) {
	if e == nil {
		return
	}
	minimumWidth, minimumHeight := 320, 240
	// 临时 UI 使用自身的内容尺寸，不能把小通知在 DPI 或屏幕变化后强行扩成普通业务窗口。
	if !e.remember && e.options.DisableResize {
		minimumWidth = max(1, e.options.Width)
		minimumHeight = max(1, e.options.Height)
	}
	if e.window.Name() == "main-window" {
		minimumWidth, minimumHeight = c.config.MinWidth, c.config.MinHeight
	}
	areas := c.areas()
	// 原生窗口以全屏或最大化启动时也要修复普通矩形缓存，退出该模式后才能恢复到安全位置。
	c.mu.Lock()
	e.normal = FitBounds(e.normal, areas, minimumWidth, minimumHeight)
	c.mu.Unlock()
	if e.window.IsMaximised() || e.window.IsMinimised() || e.window.IsFullscreen() {
		c.capture(e.window.Name())
		return
	}
	bounds := e.window.Bounds()
	fitted := FitBounds(bounds, areas, minimumWidth, minimumHeight)
	if len(areas) > 0 {
		e.window.SetMinSize(min(minimumWidth, fitted.Width), min(minimumHeight, fitted.Height))
	}
	if bounds != fitted {
		e.window.SetBounds(fitted)
	}
	c.capture(e.window.Name())
}

// clampAll 逐个校正已登记窗口的位置和尺寸；先复制窗口列表，避免持有登记表锁时调用原生窗口接口。
func (c *Controller) clampAll() {
	c.mu.RLock()
	entries := make([]*entry, 0, len(c.windows))
	for _, e := range c.windows {
		entries = append(entries, e)
	}
	c.mu.RUnlock()
	for _, e := range entries {
		c.clampOne(e)
	}
}

// CreateDialog 创建并登记弹框，继承调用页面的权限上限；弹框不记忆位置，也不经过普通窗口的加载流程，展示由调用方负责。
func (c *Controller) CreateDialog(ctx context.Context, options utils.NewWindowOptions) application.Window {
	w := utils.CreateUrlWindow(options)
	options.Name = w.Name()
	c.track(w, options, false, false, CallerCapabilities(ctx))
	return w
}

// Open 供可信的 Go 端代码创建或复用窗口，使用宿主的完整能力上限；网页调用应使用 OpenFor 继承调用者权限。
func (c *Controller) Open(options utils.NewWindowOptions) (WindowInfo, error) {
	return c.open(options, Capabilities)
}

// OpenFor 根据调用上下文的能力上限创建或复用窗口，防止受限页面通过打开本地子窗口扩大权限。
func (c *Controller) OpenFor(ctx context.Context, options utils.NewWindowOptions) (WindowInfo, error) {
	return c.open(options, CallerCapabilities(ctx))
}

// open 统一执行地址检查、外链分流、窗口 ID 复用、数量限制和状态恢复；新建窗口先显示本地加载页。
// reservedNames 只允许可信 Go 代码为固定内部窗口放行保留名称，网页参数不能设置此白名单。
func (c *Controller) open(options utils.NewWindowOptions, ceiling []string, reservedNames ...string) (WindowInfo, error) {
	inside, err := c.Policy.Navigation(options.Url)
	if err != nil {
		return WindowInfo{}, err
	}
	if !inside {
		if c.config.ExternalLinks == "block" {
			return WindowInfo{}, Fail("E_NAVIGATION_DENIED", "该地址不在应用导航范围内")
		}
		if err = c.app.Browser.OpenURL(options.Url); err != nil {
			return WindowInfo{}, Fail("E_OPEN_FAILED", "无法打开系统浏览器")
		}
		return WindowInfo{URL: options.Url, Status: "external"}, nil
	}
	c.openMu.Lock()
	defer c.openMu.Unlock()
	if options.Name == "" {
		options.Name = fmt.Sprintf("window-%d", c.sequence.Add(1))
	}
	if !validID.MatchString(options.Name) || options.Name == "main-window" || strings.HasPrefix(options.Name, "hybrid-infra-") && !slices.Contains(reservedNames, options.Name) {
		return WindowInfo{}, Fail("E_INVALID_ARGUMENT", "窗口 ID 无效或为宿主保留名称")
	}
	c.mu.RLock()
	existing := c.windows[options.Name]
	count := len(c.windows)
	c.mu.RUnlock()
	if existing != nil {
		wake(existing.window)
		return c.Info(options.Name)
	}
	if count >= 50 {
		return WindowInfo{}, Fail("E_LIMIT_EXCEEDED", "同时打开的窗口不能超过 50 个")
	}
	if options.Width <= 0 {
		options.Width = 1000
	}
	if options.Height <= 0 {
		options.Height = 720
	}
	options.Width = max(options.Width, 320)
	options.Height = max(options.Height, 240)
	actual := options
	// 先建立普通窗口并恢复安全矩形，再应用模式，避免在全屏之后写入普通坐标破坏原生恢复位置。
	actual.IsFullscreen = false
	actual.Url = StatusPage
	actual.F12DevTools = actual.F12DevTools && c.config.Debug
	w := utils.CreateUrlWindow(actual)
	w.SetMinSize(320, 240)
	c.track(w, options, true, true, ceiling)
	fullscreen := options.IsFullscreen
	maximised := false
	if saved, ok := c.store.get(options.Name); ok && c.config.RememberWindows {
		w.SetBounds(FitBounds(saved.Bounds, c.areas(), 320, 240))
		fullscreen = fullscreen || saved.Fullscreen
		maximised = saved.Maximised
	}
	if fullscreen {
		w.Fullscreen()
	} else if maximised {
		w.Maximise()
	}
	w.Show()
	c.clampOne(c.getEntry(options.Name))
	return c.Info(options.Name)
}

// getEntry 按窗口 ID 查询内部登记记录；不存在时返回 nil，由上层决定如何处理。
func (c *Controller) getEntry(id string) *entry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.windows[id]
}

// wake 恢复被最小化的窗口，再显示并聚焦窗口，供窗口复用、托盘恢复和重复启动唤醒共用。
func wake(w application.Window) {
	if w.IsMinimised() {
		w.UnMinimise()
	}
	w.Show()
	w.Focus()
}

// WakeMain 唤醒已经登记的主窗口；应用尚未创建主窗口时直接返回。
func (c *Controller) WakeMain() {
	if c.splash != nil && !c.splash.inactive() {
		c.splash.finish(true)
		return
	}
	if e := c.getEntry("main-window"); e != nil {
		wake(e.window)
	}
}

// Info 读取指定窗口的页面、加载状态、可见性和原生尺寸；窗口未登记时返回 E_WINDOW_NOT_FOUND。
func (c *Controller) Info(id string) (WindowInfo, error) {
	e := c.getEntry(id)
	if e == nil {
		return WindowInfo{}, Fail("E_WINDOW_NOT_FOUND", "窗口不存在")
	}
	c.mu.RLock()
	info := WindowInfo{ID: id, Title: e.options.Title, URL: e.options.Url, Status: e.status, Error: e.loadError}
	c.mu.RUnlock()
	info.Visible = e.window.IsVisible()
	info.Maximised = e.window.IsMaximised()
	info.Fullscreen = e.window.IsFullscreen()
	info.Bounds = e.window.Bounds()
	return info, nil
}

// List 按窗口 ID 排序返回当前窗口信息；查询期间已经关闭的窗口会被跳过。
func (c *Controller) List() []WindowInfo {
	c.mu.RLock()
	ids := make([]string, 0, len(c.windows))
	for id := range c.windows {
		ids = append(ids, id)
	}
	c.mu.RUnlock()
	sort.Strings(ids)
	result := make([]WindowInfo, 0, len(ids))
	for _, id := range ids {
		if info, err := c.Info(id); err == nil {
			result = append(result, info)
		}
	}
	return result
}

// Focus 显示并聚焦指定窗口；窗口不存在时返回明确错误。
func (c *Controller) Focus(id string) error {
	e := c.getEntry(id)
	if e == nil {
		return Fail("E_WINDOW_NOT_FOUND", "窗口不存在")
	}
	wake(e.window)
	return nil
}

// Close 请求关闭指定窗口；该窗口仍有未完成任务时返回 E_BUSY，实际关闭流程仍由原生事件处理。
func (c *Controller) Close(id string) error {
	e := c.getEntry(id)
	if e == nil {
		return Fail("E_WINDOW_NOT_FOUND", "窗口不存在")
	}
	if c.hasTasks(id) {
		return Fail("E_BUSY", "该窗口仍有未完成任务")
	}
	e.window.Close()
	return nil
}

// Retry 重新加载指定窗口的目标页面，home 为 true 时改为打开内置首页。
// 有未完成任务或应用正在退出时拒绝加载；每次加载使用独立编号和超时计时器。
func (c *Controller) Retry(id string, home bool) error {
	e := c.getEntry(id)
	if e == nil {
		return Fail("E_WINDOW_NOT_FOUND", "窗口不存在")
	}
	c.mu.Lock()
	if c.quitting.Load() {
		c.mu.Unlock()
		return Fail("E_SHUTTING_DOWN", "应用正在退出")
	}
	for _, task := range c.tasks {
		if task.WindowID == id {
			c.mu.Unlock()
			return Fail("E_BUSY", "当前窗口仍有未完成任务，请先结束或取消任务")
		}
	}
	target := e.target
	if home {
		target = "/"
		e.target = target
		e.options.Url = target
	}
	e.generation = c.sequence.Add(1)
	generation := e.generation
	e.status = "loading"
	e.loadError = nil
	if e.timer != nil {
		e.timer.Stop()
	}
	e.timer = time.AfterFunc(time.Duration(c.config.LoadTimeoutSeconds)*time.Second, func() {
		c.failLoadGeneration(id, "E_LOAD_TIMEOUT", "页面加载超时，请检查网络连接后重试", generation)
	})
	c.mu.Unlock()
	e.window.SetURL(target)
	return nil
}

// failLoad 将指定窗口转入加载失败状态并打开本地错误页，不检查是哪一次加载触发的失败。
func (c *Controller) failLoad(id, code, message string) {
	c.failLoadGeneration(id, code, message, 0)
}

// failLoadGeneration 仅处理指定编号且仍在进行的加载失败，防止旧计时器覆盖新页面；generation 为 0 时跳过编号检查。
func (c *Controller) failLoadGeneration(id, code, message string, generation uint64) {
	e := c.getEntry(id)
	if e == nil {
		return
	}
	c.mu.Lock()
	if generation != 0 && (e.generation != generation || e.status != "loading") {
		c.mu.Unlock()
		return
	}
	if e.timer != nil {
		e.timer.Stop()
	}
	e.status = "error"
	e.loadError = &Error{code, message}
	e.generation++
	c.mu.Unlock()
	e.window.SetURL(StatusPage)
	c.finishStartup(id)
}

// caller 从授权上下文读取真实调用窗口名，不使用网页可填写的目标窗口名；可信 Go 调用和测试可回退到 Wails 窗口上下文。
func caller(ctx context.Context) string {
	if principal, ok := ctx.Value(principalKey{}).(Principal); ok {
		return principal.WindowName
	}
	if w, ok := ctx.Value(application.WindowKey).(application.Window); ok {
		return w.Name()
	}
	return ""
}

// LoadState 查询当前调用窗口的加载信息，供本地加载页和管理页面展示；没有关联窗口时返回错误。
func (c *Controller) LoadState(ctx context.Context) (WindowInfo, error) {
	id := caller(ctx)
	if id == "" {
		return WindowInfo{}, Fail("E_WINDOW_NOT_FOUND", "没有关联的宿主窗口")
	}
	return c.Info(id)
}

// RetryCaller 为当前调用窗口执行重新加载或返回内置首页，避免前端自行指定要操作的其他窗口。
func (c *Controller) RetryCaller(ctx context.Context, home bool) error {
	return c.Retry(caller(ctx), home)
}

// PageReady 接收当前页面的就绪通知，停止加载超时计时器；只把正在加载的状态改为就绪，不覆盖已有错误状态。
func (c *Controller) PageReady(ctx context.Context) error {
	id := caller(ctx)
	e := c.getEntry(id)
	if e == nil {
		return Fail("E_WINDOW_NOT_FOUND", "没有关联的宿主窗口")
	}
	c.mu.Lock()
	if e.timer != nil {
		e.timer.Stop()
	}
	if e.status == "loading" {
		e.status = "ready"
	}
	c.mu.Unlock()
	c.finishStartup(id)
	return nil
}

// Tasks 按创建时间返回所有未完成任务的快照，供管理页面展示退出保护状态。
func (c *Controller) Tasks() []Task {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]Task, 0, len(c.tasks))
	for _, task := range c.tasks {
		result = append(result, task)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result
}

// hasTasks 检查指定窗口是否有未完成任务；id 为空时检查整个应用，用于退出前判断。
func (c *Controller) hasTasks(id string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, t := range c.tasks {
		if id == "" || t.WindowID == id {
			return true
		}
	}
	return false
}

// BeginTask 为当前调用窗口登记一个未完成任务并生成唯一 ID；校验任务说明、数量上限以及应用是否正在退出。
func (c *Controller) BeginTask(ctx context.Context, label string) (Task, error) {
	id := caller(ctx)
	if id == "" {
		return Task{}, Fail("E_WINDOW_NOT_FOUND", "任务必须关联宿主窗口")
	}
	label = strings.TrimSpace(label)
	if label == "" || len([]rune(label)) > 120 {
		return Task{}, Fail("E_INVALID_ARGUMENT", "任务说明应为 1 至 120 字")
	}
	task := Task{fmt.Sprintf("task-%d", c.sequence.Add(1)), label, id, time.Now()}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.quitting.Load() {
		return Task{}, Fail("E_SHUTTING_DOWN", "应用正在退出")
	}
	if len(c.tasks) >= 100 {
		return Task{}, Fail("E_LIMIT_EXCEEDED", "未完成任务过多")
	}
	c.tasks[task.ID] = task
	return task, nil
}

// EndTask 释放属于当前调用窗口的任务；重复释放已经结束的任务可安全忽略，但不能结束其他窗口的任务。
func (c *Controller) EndTask(ctx context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	task, ok := c.tasks[id]
	if !ok {
		return nil
	}
	if task.WindowID != caller(ctx) {
		return Fail("E_PERMISSION_DENIED", "不能结束其他窗口的任务")
	}
	delete(c.tasks, id)
	return nil
}

// Quit 在没有未完成任务时标记应用正在退出、保存窗口状态并异步退出；仍有任务时返回 E_BUSY。
func (c *Controller) Quit() error {
	c.mu.Lock()
	if len(c.tasks) > 0 {
		c.mu.Unlock()
		return Fail("E_BUSY", "仍有未完成任务，请完成或取消后退出")
	}
	c.quitting.Store(true)
	c.mu.Unlock()
	c.saveAll()
	go c.app.Quit()
	return nil
}

// saveAll 采集所有仍在管理中的窗口状态，并在启用窗口记忆时写入用户数据目录；保存失败会记录日志。
func (c *Controller) saveAll() {
	c.mu.RLock()
	ids := make([]string, 0, len(c.windows))
	for id := range c.windows {
		ids = append(ids, id)
	}
	c.mu.RUnlock()
	for _, id := range ids {
		c.capture(id)
	}
	if c.config.RememberWindows {
		if err := c.store.save(); err != nil {
			c.app.Logger.Error("save window state", "error", err)
		}
	}
}

// RequestQuitWithPrompt 处理原生关闭按钮或托盘的退出请求；有未完成任务时只显示一个确认框，由用户决定返回还是继续退出。
func (c *Controller) RequestQuitWithPrompt() {
	if c.quitting.Load() {
		return
	}
	if !c.hasTasks("") {
		if c.Quit() == nil {
			return
		}
	}
	if !c.promptOpen.CompareAndSwap(false, true) {
		return
	}
	dialog := c.app.Dialog.Question().SetTitle("仍有未完成任务").SetMessage("有任务尚未完成，退出可能丢失当前进度。是否仍要退出？")
	if e := c.getEntry("main-window"); e != nil {
		dialog.AttachToWindow(e.window)
	}
	dialog.AddButton("仍然退出").OnClick(func() { c.promptOpen.Store(false); c.quitting.Store(true); c.saveAll(); c.app.Quit() })
	dialog.AddButton("返回应用").SetAsDefault().SetAsCancel().OnClick(func() { c.promptOpen.Store(false); c.WakeMain() })
	dialog.Show()
}

// ShouldQuit 供原生系统退出流程判断是否允许退出；有任务时先阻止退出并显示确认框，否则阻止新增任务并保存状态。
func (c *Controller) ShouldQuit() bool {
	if c.quitting.Load() {
		return true
	}
	c.mu.Lock()
	busy := len(c.tasks) > 0
	if !busy {
		c.quitting.Store(true)
	}
	c.mu.Unlock()
	if busy {
		go c.RequestQuitWithPrompt()
		return false
	}
	c.saveAll()
	return true
}

// InstallTray 创建托盘图标、任务状态及管理页快捷菜单；从托盘退出同样需要检查未完成任务。
func (c *Controller) InstallTray(icon []byte) {
	tray := c.app.SystemTray.New()
	tray.SetIcon(icon)
	tray.SetTooltip(c.config.Title)
	tray.OnClick(c.WakeMain)
	menu := application.NewMenu()
	menu.Add("开发文档").OnClick(func(*application.Context) {
		if _, err := c.OpenDeveloperDocs(); err != nil {
			c.app.Logger.Warn("open developer docs", "code", "E_DOCS_OPEN")
		}
	})
	menu.AddSeparator()
	menu.Add("显示主窗口").OnClick(func(*application.Context) { c.WakeMain() })
	menu.Add("设备接入中心").OnClick(func(*application.Context) {
		if _, err := c.Open(utils.NewWindowOptions{Name: "devices-console", Title: "设备接入中心", Url: "/shell/devices.html", Width: 1160, Height: 880}); err != nil {
			c.app.Logger.Warn("open devices center", "code", "E_DEVICES_OPEN")
		}
	})
	menu.Add("打印与文件中心").OnClick(func(*application.Context) {
		if _, err := c.Open(utils.NewWindowOptions{Name: "documents-console", Title: "打印与文件中心", Url: "/shell/documents.html", Width: 1160, Height: 880}); err != nil {
			c.app.Logger.Warn("open documents center", "code", "E_DOCUMENTS_OPEN")
		}
	})
	menu.Add("任务与数据中心").OnClick(func(*application.Context) {
		if _, err := c.Open(utils.NewWindowOptions{Name: "tasks-console", Title: "任务与数据中心", Url: "/shell/tasks.html", Width: 1160, Height: 880}); err != nil {
			c.app.Logger.Warn("open task center", "code", "E_TASKS_OPEN")
		}
	})
	menu.Add("配置中心").OnClick(func(*application.Context) {
		if _, err := c.Open(utils.NewWindowOptions{Name: "settings-console", Title: "配置中心", Url: "/shell/settings.html", Width: 1120, Height: 900}); err != nil {
			c.app.Logger.Warn("open settings center", "code", "E_SETTINGS_OPEN")
		}
	})
	menu.Add("宿主管理").OnClick(func(*application.Context) {
		_, err := c.Open(utils.NewWindowOptions{Name: "host-console", Title: "宿主管理", Url: "/shell/manage.html", Width: 1100, Height: 800})
		if err != nil {
			c.app.Logger.Warn("open host console", "error", err)
		}
	})
	menu.Add("桌面能力与下载").OnClick(func(*application.Context) {
		_, err := c.Open(utils.NewWindowOptions{Name: "desktop-console", Title: "桌面能力", Url: "/shell/desktop.html", Width: 1100, Height: 800})
		if err != nil {
			c.app.Logger.Warn("open desktop console", "error", err)
		}
	})
	c.trayStatus = menu.Add("没有未完成任务").SetEnabled(false)
	c.tray = tray
	menu.AddSeparator()
	menu.Add("退出应用").OnClick(func(*application.Context) { go c.RequestQuitWithPrompt() })
	tray.SetMenu(menu)
}

// UpdateTrayStatus 由应用秒级计时器刷新待处理任务数量，只有数量改变时才更新原生菜单和提示，避免重复系统调用。
func (c *Controller) UpdateTrayStatus() {
	if c.tray == nil {
		return
	}
	c.mu.RLock()
	count := len(c.tasks)
	c.mu.RUnlock()
	if count == c.trayTasks {
		return
	}
	c.trayTasks = count
	label := "没有未完成任务"
	if count > 0 {
		label = fmt.Sprintf("%d 个未完成任务", count)
	}
	c.trayStatus.SetLabel(label)
	c.tray.SetTooltip(c.config.Title + " · " + label)
}

// HostInfo 返回 SDK 握手所需的版本、协议、平台、关闭行为及当前调用者真正获准使用的能力。
func (c *Controller) HostInfo(ctx context.Context) map[string]any {
	return map[string]any{"name": c.config.Identity(), "appId": c.config.Identity(), "protocolScheme": c.config.ProtocolScheme(), "version": Version, "protocolVersion": ProtocolVersion, "wailsVersion": "v3.0.0-beta.33", "platform": runtime.GOOS, "capabilities": CallerCapabilities(ctx), "closeBehavior": c.config.CloseBehavior, "singleInstance": c.config.SingleInstance, "maxPayloadBytes": 1024 * 1024}
}

// NativeMessage 处理页面就绪和导航消息，使用原生提供的来源元数据核对页面身份，再执行导航分流或更新加载状态。
func (c *Controller) NativeMessage(w application.Window, message string, source *application.OriginInfo) {
	if !strings.HasPrefix(message, "hybrid:") || source == nil {
		return
	}
	sourceOrigin, topOrigin := Origin(source.Origin), Origin(source.TopOrigin)
	if sourceOrigin == "" || sourceOrigin != topOrigin {
		return
	}
	for _, handler := range c.nativeHandlers {
		if handler(w, message, source) {
			return
		}
	}
	var data struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	}
	if json.Unmarshal([]byte(strings.TrimPrefix(message, "hybrid:")), &data) != nil {
		return
	}
	e := c.getEntry(w.Name())
	if e == nil {
		return
	}
	if data.Type == "open-docs" {
		if slices.Contains(c.Policy.WindowGrants(fmt.Sprint(w.ID()), sourceOrigin), "host") {
			go func() {
				if _, err := c.OpenDeveloperDocs(); err != nil {
					c.app.Logger.Warn("open developer docs", "code", "E_DOCS_OPEN")
				}
			}()
		}
		return
	}
	if data.Type == "navigate" {
		sourceAllowed, sourceErr := c.Policy.Navigation(source.TopOrigin)
		if sourceErr != nil || !sourceAllowed {
			return
		}
		target, err := url.Parse(data.URL)
		if err != nil {
			return
		}
		base, _ := url.Parse(source.TopOrigin)
		if base == nil {
			return
		}
		target = base.ResolveReference(target)
		inside, err := c.Policy.Navigation(target.String())
		if err != nil {
			return
		}
		if inside {
			if c.hasTasks(w.Name()) {
				c.retryFromUI(w)
				return
			}
			c.mu.Lock()
			e.target = target.String()
			e.options.Url = e.target
			c.mu.Unlock()
			c.retryFromUI(w)
		} else if c.config.ExternalLinks == "browser" && slices.Contains(c.Policy.WindowGrants(fmt.Sprint(w.ID()), sourceOrigin), "windows") {
			_ = c.app.Browser.OpenURL(target.String())
		}
		return
	}
	if data.Type != "ready" {
		if data.Type == "failed" {
			c.failLoad(w.Name(), "E_LOAD_FAILED", "页面加载失败，请检查地址和网络连接")
		}
		return
	}
	inside, err := c.Policy.Navigation(source.TopOrigin)
	if err != nil || !inside {
		c.failLoad(w.Name(), "E_NAVIGATION_DENIED", "页面已离开应用导航范围，请返回首页")
		return
	}
	parsed, _ := url.Parse(source.TopOrigin)
	if parsed != nil && parsed.Path == StatusPage {
		return
	}
	c.mu.Lock()
	if e.status == "error" {
		c.mu.Unlock()
		return
	}
	if e.timer != nil {
		e.timer.Stop()
	}
	e.status = "ready"
	e.loadError = nil
	c.mu.Unlock()
	c.finishStartup(w.Name())
}

// confirmWindowClose 为仍有任务的子窗口显示关闭确认框；仅在用户确认后放行关闭，并避免同时弹出多个确认框。
func (c *Controller) confirmWindowClose(e *entry) {
	if !c.promptOpen.CompareAndSwap(false, true) {
		return
	}
	dialog := c.app.Dialog.Question().SetTitle("该窗口仍有未完成任务").SetMessage("关闭后可能丢失当前进度，是否仍要关闭？").AttachToWindow(e.window)
	dialog.AddButton("仍然关闭").OnClick(func() {
		c.promptOpen.Store(false)
		c.mu.Lock()
		e.closeApproved = true
		c.mu.Unlock()
		e.window.Close()
	})
	dialog.AddButton("返回窗口").SetAsDefault().SetAsCancel().OnClick(func() { c.promptOpen.Store(false); wake(e.window) })
	dialog.Show()
}

// retryFromUI 处理快捷键或受控链接发起的页面重新加载；被任务保护等原因拒绝时，用原生提示说明原因。
func (c *Controller) retryFromUI(w application.Window) {
	if err := c.Retry(w.Name(), false); err != nil {
		c.app.Dialog.Info().AttachToWindow(w).SetTitle("暂时无法切换页面").SetMessage(err.Error()).Show()
	}
}

// guardScript 在页面加载完成后通知宿主就绪，并把链接及 window.open 交给原生导航策略处理。
// 脚本只发送操作意图，是否接受消息仍由 NativeMessage 根据原生来源元数据决定。
const guardScript = `(() => {
 if (window.__hybridGuard) return; window.__hybridGuard = true;
 const send = value => {
  const message = 'hybrid:' + JSON.stringify(value);
  if (window.chrome?.webview?.postMessage) window.chrome.webview.postMessage(message);
  else if (window.webkit?.messageHandlers?.external?.postMessage) window.webkit.messageHandlers.external.postMessage(message);
  else if (window._wails?.invoke) window._wails.invoke(message);
 };
 // 向 F12 控制台提供固定文档入口，只发送原生意图；来源、窗口与 host 权限仍由 Go 校验。
 window.hybridDocs = () => { send({type:'open-docs'}); return '已请求打开开发文档（需要 host 权限）'; };
 if (document.documentURI.startsWith('chrome-error:') || location.protocol === 'chrome-error:') send({type:'failed'});
 else if (location.pathname !== '/shell/status.html') send({type:'ready'});
 document.addEventListener('click', e => {
  const link=e.target.closest?.('a[href]'); if (!link || e.defaultPrevented) return;
  const url=new URL(link.href,location.href);
  if (url.origin===location.origin && url.pathname===location.pathname && url.search===location.search && url.hash) return;
  e.preventDefault(); send({type:'navigate',url:url.href});
 },true);
 window.open = url => { send({type:'navigate',url:new URL(url||'/',location.href).href}); return null; };
})();`
