package utils

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"hybrid-infra/go/utils/cross"
)

const (
	PlaceCenter       = "center"
	PlaceTopLeft      = "topLeft"
	PlaceTopRight     = "topRight"
	PlaceTopCenter    = "topCenter"
	PlaceBottomCenter = "bottomCenter"
	PlaceBottomLeft   = "bottomLeft"
	PlaceBottomRight  = "bottomRight"
)

// CreateUrlWindow 使用公共配置创建隐藏的原生窗口，处理默认尺寸、全屏状态和工作区定位；登记、权限及展示由上层负责。
func CreateUrlWindow(option NewWindowOptions) application.Window {
	var window *application.WebviewWindow
	app := application.Get()
	place := option.Place
	width := option.Width
	height := option.Height
	if width <= 0 {
		width = 300
	}
	if height <= 0 {
		height = 150
	}
	isFullScreen := startState(option)
	options := application.WebviewWindowOptions{
		Hidden:        true,
		Title:         option.Title,
		Name:          option.Name,
		StartState:    isFullScreen,
		Frameless:     option.FrameLess,     // default true
		DisableResize: option.DisableResize, // default true
		URL:           option.Url,
		Permissions: map[application.PermissionType]application.Permission{
			application.PermissionCamera:        application.PermissionDefault,
			application.PermissionMicrophone:    application.PermissionDefault,
			application.PermissionGeolocation:   application.PermissionDefault,
			application.PermissionNotifications: application.PermissionDefault,
			application.PermissionClipboardRead: application.PermissionDefault,
		},
		AlwaysOnTop:    option.AlwaysOnTop, // default true
		BackgroundType: application.BackgroundTypeTransparent,
		KeyBindings:    map[string]func(window application.Window){},
		Windows: application.WindowsWindow{
			HiddenOnTaskbar: option.HiddenOnTaskbar,
			//DisableFramelessWindowDecorations: true,
		},
	}
	if option.F5Reload {
		options.KeyBindings["F5"] = func(window application.Window) {
			window.Reload()
		}
	}
	if option.F12DevTools {
		options.KeyBindings["F12"] = func(window application.Window) {
			window.OpenDevTools()
		}
	}
	if isFullScreen == application.WindowStateNormal {
		options.Width = width
		options.Height = height
	}
	if option.FrameLess {
		options.Windows.DisableFramelessWindowDecorations = true
	} else {
		options.Windows.DisableFramelessWindowDecorations = false
	}
	options.InitialPosition = application.WindowCentered
	if place != "" && place != PlaceCenter && isFullScreen == application.WindowStateNormal {
		box, err := cross.GetScreenSize()
		if err != nil {
			app.Logger.Warn("Cannot determine work area; centering window", "error", err)
		} else if x, y, ok := windowPosition(box, width, height, place); ok {
			options.InitialPosition = application.WindowXY
			options.X, options.Y = x, y
		}
	}
	window = app.Window.NewWithOptions(options)
	window.OnWindowEvent(events.Common.WindowRuntimeReady, func(event *application.WindowEvent) {
		window.ExecJS("document.addEventListener('contextmenu', function(e) { e.preventDefault();})")
	})
	return window
}

// startState 把窗口配置中的全屏标志转换成 Wails 启动状态，未启用全屏时返回普通窗口状态。
func startState(option NewWindowOptions) application.WindowState {
	if option.IsFullscreen {
		return application.WindowStateFullscreen
	}
	return application.WindowStateNormal
}

// windowPosition 根据工作区和角落位置计算窗口坐标，支持显示器负坐标；未知位置返回 false，让调用方采用居中策略。
func windowPosition(box cross.BOX, width, height int, place string) (int, int, bool) {
	x, y := int(box.Left), int(box.Top)
	switch place {
	case PlaceTopLeft:
	case PlaceTopRight:
		x += max(0, int(box.Width)-width)
	case PlaceTopCenter:
		x += max(0, (int(box.Width)-width)/2)
	case PlaceBottomCenter:
		x += max(0, (int(box.Width)-width)/2)
		y += max(0, int(box.Height)-height)
	case PlaceBottomLeft:
		y += max(0, int(box.Height)-height)
	case PlaceBottomRight:
		x += max(0, int(box.Width)-width)
		y += max(0, int(box.Height)-height)
	default:
		return 0, 0, false
	}
	return x, y, true
}
