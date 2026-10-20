package utils

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"hybrid-infra/go/utils/cross"
)

const (
	PlaceCenter      = "center"
	PlaceTopLeft     = "topLeft"
	PlaceTopRight    = "topRight"
	PlaceBottomLeft  = "bottomLeft"
	PlaceBottomRight = "bottomRight"
)

// CreateUrlWindow center、bottomLeft、bottomRight、topRight、topLeft
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
		Hidden:         true,
		Title:          option.Title,
		Name:           option.Name,
		StartState:     isFullScreen,
		Frameless:      option.FrameLess,     // default true
		DisableResize:  option.DisableResize, // default true
		URL:            option.Url,
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
func startState(option NewWindowOptions) application.WindowState {
	if option.IsFullscreen {
		return application.WindowStateFullscreen
	}
	return application.WindowStateNormal
}

func windowPosition(box cross.BOX, width, height int, place string) (int, int, bool) {
	x, y := int(box.Left), int(box.Top)
	switch place {
	case PlaceTopLeft:
	case PlaceTopRight:
		x += max(0, int(box.Width)-width)
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
