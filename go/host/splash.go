package host

import (
	"context"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"hybrid-infra/go/utils"
)

const SplashWindowName = "hybrid-infra-splash"
const SplashPage = "/shell/splash.html"

// StartupState 只向启动屏提供应用名称、版本和加载阶段，不泄露业务地址、配置或错误详情。
type StartupState struct {
	Title   string `json:"title"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

// startupScreen 管理启动屏、主页面就绪和三个有界计时器；所有原生操作通过回调在锁外执行。
type startupScreen struct {
	mu                            sync.Mutex
	shownAt                       time.Time
	minimum                       time.Duration
	completed, finished, stopped  bool
	fallback, completion, closing *time.Timer
	show, reveal, leave, close    func()
}

// start 安装启动屏自身的兜底超时；其 WebView 无法就绪时也会展示主窗口，不无限隐藏应用。
func (s *startupScreen) start(timeout time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished || s.stopped || !s.shownAt.IsZero() || s.fallback != nil {
		return
	}
	s.fallback = time.AfterFunc(timeout, func() { s.finish(true) })
}

// present 标记启动屏已可绘制并显示，只执行一次；主页面提前就绪时仍保留短暂的可读展示时间。
func (s *startupScreen) present() {
	s.mu.Lock()
	if s.finished || s.stopped || !s.shownAt.IsZero() {
		s.mu.Unlock()
		return
	}
	s.shownAt = time.Now()
	if s.fallback != nil {
		s.fallback.Stop()
	}
	s.mu.Unlock()
	s.show()
	s.finish(false)
}

// complete 通知主页面已就绪或已转入错误恢复页，不依赖启动屏页面的轮询结果决定展示时机。
func (s *startupScreen) complete() {
	s.mu.Lock()
	s.completed = true
	s.mu.Unlock()
	s.finish(false)
}

// inactive 判断启动屏是否已经完成或应用正在退出，供排队到 UI 线程的显示回调再次核对。
func (s *startupScreen) inactive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finished || s.stopped
}

// finish 完成一次主窗口交接；force 用于用户跳过或启动屏自身超时，不受最短展示时间限制。
func (s *startupScreen) finish(force bool) {
	s.mu.Lock()
	if s.finished || s.stopped || !force && (!s.completed || s.shownAt.IsZero()) {
		s.mu.Unlock()
		return
	}
	if !force {
		remaining := s.minimum - time.Since(s.shownAt)
		if remaining > 0 {
			if s.completion == nil {
				s.completion = time.AfterFunc(remaining, func() { s.finish(false) })
			}
			s.mu.Unlock()
			return
		}
	}
	s.finished = true
	if s.fallback != nil {
		s.fallback.Stop()
	}
	if s.completion != nil {
		s.completion.Stop()
	}
	animated := !s.shownAt.IsZero()
	s.mu.Unlock()
	s.reveal()
	if animated {
		s.leave()
		s.mu.Lock()
		if !s.stopped {
			s.closing = time.AfterFunc(180*time.Millisecond, s.close)
		}
		s.mu.Unlock()
	} else {
		s.close()
	}
}

// stop 在应用退出时取消所有计时器，防止退出过程中再次显示主窗口或访问已经销毁的窗口。
func (s *startupScreen) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	s.finished = true
	for _, timer := range []*time.Timer{s.fallback, s.completion, s.closing} {
		if timer != nil {
			timer.Stop()
		}
	}
}

// InstallSplash 创建不参与窗口记忆和业务授权的独立启动屏；未开启时不创建额外窗口。
func (c *Controller) InstallSplash() {
	if !c.config.SplashEnabled {
		return
	}
	window := c.app.Window.NewWithOptions(application.WebviewWindowOptions{Name: SplashWindowName, Title: c.config.Title + " · 正在启动", URL: SplashPage, Width: 480, Height: 360, Frameless: true, DisableResize: true, Hidden: true, InitialPosition: application.WindowCentered, BackgroundColour: application.NewRGB(15, 43, 37), Windows: application.WindowsWindow{HiddenOnTaskbar: true, DisableFramelessWindowDecorations: true}, Permissions: webPermissions()})
	screen := &startupScreen{minimum: 600 * time.Millisecond}
	c.splash = screen
	// UI 队列执行前再次核对退出状态，避免就绪与手动跳过并发时把已经完成的启动屏重新显示。
	screen.show = func() {
		application.InvokeAsync(func() {
			if !c.quitting.Load() && !screen.inactive() {
				window.Show()
				window.Focus()
			}
		})
	}
	screen.reveal = func() {
		application.InvokeAsync(func() {
			if !c.quitting.Load() {
				if main := c.getEntry("main-window"); main != nil {
					wake(main.window)
				}
			}
		})
	}
	screen.leave = func() {
		if !c.quitting.Load() {
			window.ExecJS("document.documentElement.classList.add('splash-leaving')")
		}
	}
	screen.close = func() {
		if !c.quitting.Load() {
			window.Close()
		}
	}
	c.track(window, utils.NewWindowOptions{Name: SplashWindowName, Title: c.config.Title + " · 正在启动", Url: SplashPage, Width: 480, Height: 360}, false, false, []string{"host"})
	window.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) { screen.present() })
	// Alt+F4 只跳过启动屏并展示主窗口；应用整体退出时不拦截 Wails 的销毁流程。
	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if !c.quitting.Load() && !screen.inactive() {
			event.Cancel()
			go screen.finish(true)
		}
	})
	c.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) { screen.start(4 * time.Second) })
	c.app.OnShutdown(screen.stop)
}

// finishStartup 将主窗口的就绪或失败通知交给启动屏管理器；普通子窗口不会影响启动流程。
func (c *Controller) finishStartup(id string) {
	if id == "main-window" && c.splash != nil {
		c.splash.complete()
	}
}

// GetStartupState 校验真实调用窗口，仅允许内置启动屏读取不含业务信息的加载阶段。
func (c *Controller) GetStartupState(ctx context.Context) (StartupState, error) {
	if caller(ctx) != SplashWindowName {
		return StartupState{}, Fail("E_PERMISSION_DENIED", "此接口仅供内置启动屏使用")
	}
	state := StartupState{Title: c.config.Title, Version: Version, Status: "starting"}
	c.mu.RLock()
	if main := c.windows["main-window"]; main != nil {
		state.Status = main.status
	}
	c.mu.RUnlock()
	return state, nil
}

// DismissSplash 只允许启动屏本身发起跳过；其他网页即使拥有 host 能力，也不能控制内部启动窗口。
func (c *Controller) DismissSplash(ctx context.Context) error {
	if caller(ctx) != SplashWindowName {
		return Fail("E_PERMISSION_DENIED", "只能从内置启动屏跳过启动流程")
	}
	if c.splash != nil {
		c.splash.finish(true)
	}
	return nil
}
