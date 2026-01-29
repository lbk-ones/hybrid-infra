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
	box, _ := cross.GetScreenSize()
	app := application.Get()
	place := option.Place
	width := option.Width
	height := option.Height
	isFullScreen := startState(option)
	options := application.WebviewWindowOptions{
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
	if isFullScreen != application.WindowStateMaximised {
		options.Width = width
		options.Height = height
	}
	if option.FrameLess {
		options.Windows.DisableFramelessWindowDecorations = true
	} else {
		options.Windows.DisableFramelessWindowDecorations = false
	}
	if place == PlaceCenter {
		options.InitialPosition = application.WindowCentered
	} else if place != "" {
		var X, Y int
		if place == PlaceBottomRight {
			X = int(box.Width) - width
			Y = int(box.Top) + (int(box.Height) - height)
		} else if place == PlaceBottomLeft {
			X = int(box.Left)
			Y = int(box.Top) + (int(box.Height) - height)
		} else if place == PlaceTopLeft {
			X = int(box.Left)
			Y = int(box.Top) + int(box.Left)
		} else if place == PlaceTopRight {
			X = int(box.Left) + (int(box.Width) - width)
			Y = int(box.Top)
		}
		options.InitialPosition = application.WindowXY
		options.X = X
		options.Y = Y
	}
	window = app.Window.NewWithOptions(options)
	window.OnWindowEvent(events.Common.WindowRuntimeReady, func(event *application.WindowEvent) {
		window.ExecJS("document.addEventListener('contextmenu', function(e) { e.preventDefault();})")
	})
	return window
}
func startState(option NewWindowOptions) application.WindowState {
	if option.IsFullscreen {
		return application.WindowStateMaximised
	}
	return application.WindowStateNormal
}
