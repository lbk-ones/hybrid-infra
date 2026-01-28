package utils

import "github.com/wailsapp/wails/v3/pkg/application"

func CreateUrlWindow(width, height int, url string) application.Window {
	app := application.Get()
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		InitialPosition: application.WindowCentered,
		Width:           width,
		Height:          height,
		Frameless:       true,
		DisableResize:   true,
		URL:             url,
		AlwaysOnTop:     true,
		BackgroundType:  application.BackgroundTypeTransparent,
		Windows: application.WindowsWindow{
			HiddenOnTaskbar:                   true,
			DisableFramelessWindowDecorations: true,
		},
	})
	return window
}
func CreateHtmlWindow(width, height int, html string) application.Window {
	app := application.Get()
	window := app.Window.New()
	window.SetTitle("Ui")
	window.SetSize(width, height)
	window.SetHTML(html)
	window.SetFrameless(true)
	window.SetResizable(false)
	return window
}
