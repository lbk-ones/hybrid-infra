package host

import (
	"bytes"
	"context"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"hybrid-infra/go/setting"
)

const maxIconBytes = 2 << 20

// iconPair 保存两个独立 PNG；托盘留空时跟随应用图标，二者均空时使用各自内置资源。
type iconPair struct{ application, tray []byte }

// nativeWindowIcons 封装平台拥有的原生句柄；所有方法都只能在 Wails UI 线程调用。
type nativeWindowIcons interface {
	Apply(application.Window)
	Close()
}

// IconManager 将耗时加载与 UI 线程替换分开；原生句柄只由 UI 线程持有，配置副本使用独立读写锁。
type IconManager struct {
	controller *Controller
	defaults   iconPair
	op         sync.Mutex
	mu         sync.RWMutex
	active     setting.IconConfig
	native     nativeWindowIcons
	stopped    bool
}

// InstallIcons 安装品牌资源及启动回调；启动配置失败时保留内置图标，并记录不含地址的错误码。
func (c *Controller) InstallIcons(appIcon, trayIcon []byte) error {
	if err := validateIcon(appIcon); err != nil {
		return err
	}
	if err := validateIcon(trayIcon); err != nil {
		return err
	}
	manager := &IconManager{controller: c, defaults: iconPair{appIcon, trayIcon}}
	c.icons = manager
	// 先应用内置图标，再异步读取配置，避免网络地址阻塞主窗口创建和单实例转交。
	c.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go func() {
			manager.op.Lock()
			defer manager.op.Unlock()
			if err := manager.apply(manager.defaults, setting.IconConfig{}); err != nil {
				c.app.Logger.Warn("apply default icon", "code", "E_ICON_APPLY")
				return
			}
			if c.config.Icons != (setting.IconConfig{}) {
				if err := manager.reload(c.app.Context(), c.config.Icons); err != nil {
					c.app.Logger.Warn("load configured icon", "code", "E_ICON_LOAD")
				}
			}
		}()
	})
	c.app.OnShutdown(func() {
		application.InvokeSync(func() {
			manager.stopped = true
			c.mu.RLock()
			windows := make([]application.Window, 0, len(c.windows))
			for _, entry := range c.windows {
				windows = append(windows, entry.window)
			}
			c.mu.RUnlock()
			for _, window := range windows {
				clearWindowIcons(window)
			}
			if manager.native != nil {
				manager.native.Close()
				manager.native = nil
			}
		})
	})
	return nil
}

// validateIcon 限制 PNG 编码大小和解码尺寸，防止压缩图片消耗过量内存或把其他文件传入系统图标 API。
func validateIcon(data []byte) error {
	if len(data) == 0 || len(data) > maxIconBytes {
		return Fail("E_ICON_INVALID", "图标必须是大小不超过 2 MiB 的 PNG")
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width != config.Height || config.Width < 16 || config.Width > 1024 {
		return Fail("E_ICON_INVALID", "图标必须是 16–1024 像素的正方形 PNG")
	}
	if _, err = png.Decode(bytes.NewReader(data)); err != nil {
		return Fail("E_ICON_INVALID", "PNG 图标内容损坏")
	}
	return nil
}

// loadIcon 只读取显式配置的本地普通文件或 HTTPS；网络最多 8 秒、2 MiB，重定向不跨来源或降级协议。
func loadIcon(ctx context.Context, address, base string) ([]byte, error) {
	var reader io.ReadCloser
	if u, err := url.Parse(address); err == nil && (u.Scheme == "https" || u.Scheme == "http") {
		client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 3 || req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host || req.URL.User != nil {
				return Fail("E_ICON_LOAD", "图标重定向超出原始来源")
			}
			return nil
		}}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return nil, Fail("E_ICON_LOAD", "图标地址无效")
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, Fail("E_ICON_LOAD", "无法下载图标")
		}
		if response.StatusCode != 200 || response.ContentLength > maxIconBytes {
			_ = response.Body.Close()
			return nil, Fail("E_ICON_LOAD", "图标响应异常或超过大小限制")
		}
		reader = response.Body
	} else {
		path := address
		if !filepath.IsAbs(path) {
			path = filepath.Join(base, path)
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, Fail("E_ICON_LOAD", "无法读取本地图标")
		}
		stat, err := file.Stat()
		if err != nil || !stat.Mode().IsRegular() || stat.Size() > maxIconBytes {
			_ = file.Close()
			return nil, Fail("E_ICON_INVALID", "本地图标必须是不超过 2 MiB 的普通文件")
		}
		reader = file
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxIconBytes+1))
	if err != nil {
		return nil, Fail("E_ICON_LOAD", "图标读取中断")
	}
	if err = validateIcon(data); err != nil {
		return nil, err
	}
	return data, nil
}

// loadIconPair 两个资源均校验通过才交付；自定义托盘留空时复用自定义应用图标。
func loadIconPair(ctx context.Context, config setting.IconConfig, base string, defaults iconPair) (iconPair, error) {
	if err := config.Validate(); err != nil {
		return iconPair{}, Fail("E_ICON_INVALID", err.Error())
	}
	result := defaults
	var err error
	if config.Application != "" {
		result.application, err = loadIcon(ctx, config.Application, base)
		if err != nil {
			return iconPair{}, err
		}
		result.tray = result.application
	}
	if config.Tray != "" {
		result.tray, err = loadIcon(ctx, config.Tray, base)
		if err != nil {
			return iconPair{}, err
		}
	}
	return result, nil
}

// Reload 根据固定配置文件的位置解析相对路径；加载失败保持原图标，不修改启动权限或其他配置。
func (m *IconManager) Reload(ctx context.Context, config setting.IconConfig) error {
	m.op.Lock()
	defer m.op.Unlock()
	return m.reload(ctx, config)
}

// reload 在持有操作锁时加载并应用图标，使启动加载和用户重载按顺序执行，不会互相覆盖。
func (m *IconManager) reload(ctx context.Context, config setting.IconConfig) error {
	path, _ := m.controller.config.ConfigLocation()
	if path == "" {
		path = filepath.Join(m.controller.dataDir, "settings.json")
	}
	pair, err := loadIconPair(ctx, config, filepath.Dir(path), m.defaults)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return Fail("E_CANCELLED", "图标加载已取消")
	}
	return m.apply(pair, config)
}

// apply 在主线程一次替换所有现有窗口与托盘；先创建新句柄，全部窗口切换后才释放旧句柄。
func (m *IconManager) apply(pair iconPair, config setting.IconConfig) error {
	return application.InvokeSyncWithError(func() error {
		if m.stopped {
			return Fail("E_CANCELLED", "应用正在退出")
		}
		next, err := newNativeWindowIcons(pair.application)
		if err != nil {
			return err
		}
		c := m.controller
		c.mu.RLock()
		windows := make([]application.Window, 0, len(c.windows))
		for _, entry := range c.windows {
			windows = append(windows, entry.window)
		}
		c.mu.RUnlock()
		previous := m.native
		m.native = next
		for _, window := range windows {
			next.Apply(window)
		}
		if c.tray != nil {
			c.tray.SetIcon(pair.tray)
			c.tray.SetDarkModeIcon(pair.tray)
		}
		c.app.SetIcon(pair.application)
		m.mu.Lock()
		m.active = config
		m.mu.Unlock()
		if previous != nil {
			previous.Close()
		}
		return nil
	})
}

// ApplyWindow 将当前图标应用到后来创建的窗口；读取句柄和替换始终在 UI 线程，避免与重载并发释放。
func (m *IconManager) ApplyWindow(window application.Window) {
	application.InvokeAsync(func() {
		if !m.stopped && m.native != nil {
			m.native.Apply(window)
		}
	})
}

// ActiveConfig 返回已经成功应用的图标配置副本，用于区分已保存但未重载的设置。
func (m *IconManager) ActiveConfig() setting.IconConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active
}
