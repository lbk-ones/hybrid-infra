package main

import (
	"embed"
	raw_message_handler "hybrid-infra/go/raw-message-handler"
	"hybrid-infra/go/services"
	"hybrid-infra/go/setting"
	"hybrid-infra/go/utils"
	"log"
	"log/slog"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	application.RegisterEvent[string]("time")
}

// main function serves as the application's entry point. It initializes the application, creates a window,
func main() {

	// get pkg info
	pkgInfo, err := setting.New().Parse()
	if err != nil {
		log.Fatal(err)
	}

	app := application.New(application.Options{
		Name:        "hybrid-infra",
		Description: "A HybridProjects application",
		Assets: application.AssetOptions{
			Handler:    application.BundledAssetFileServer(assets),
			Middleware: utils.AssetMiddleware(pkgInfo.TrustedOrigins()),
		},
		LogLevel: slog.Level(pkgInfo.LogLevel),
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		RawMessageHandler: func(window application.Window, message string, originInfo *application.OriginInfo) {
			raw_message_handler.NewRawMessageHandler().HandleMessage(window, message, originInfo)
		},
	})

	state := application.WindowStateMaximised
	if pkgInfo.Width > 0 && pkgInfo.Height > 0 {
		state = application.WindowStateNormal
	}
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:       "main-window",
		Title:      pkgInfo.Title,
		StartState: state,
		Width:      pkgInfo.Width,
		Height:     pkgInfo.Height,
		// 是否无边框模式
		Frameless: pkgInfo.Frameless,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(255, 255, 255),
		URL:              pkgInfo.Url,
		KeyBindings: map[string]func(window application.Window){
			"F12": func(window application.Window) {
				if pkgInfo.Debug {
					window.OpenDevTools()
				}
			},
			"F5": func(window application.Window) {
				window.Reload()
			},
			"ctrl+f5": func(window application.Window) {
				window.ForceReload()
			},
			"shift+ctrl+c": func(window application.Window) {
				window.Center()
			},
		},
	})
	// register service
	if err := services.Register(app); err != nil {
		log.Fatal(err)
	}

	// 每一秒向客户端发送当前时间
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case now := <-ticker.C:
				app.Event.Emit("time", now.Format("2006/01/02 15:04:05"))
			case <-app.Context().Done():
				return
			}
		}
	}()

	// Run the application. This blocks until the application has been exited.
	err = app.Run()

	// If an error occurred while running the application, log it and exit.
	if err != nil {
		log.Fatal(err)
	}
}
