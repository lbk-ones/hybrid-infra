package main

import (
	"embed"
	_ "embed"
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
	pkgInfo := setting.New().Parse()

	app := application.New(application.Options{
		Name:        "hybrid-infra",
		Description: "A HybridProjects application",
		Assets: application.AssetOptions{
			Handler:    application.BundledAssetFileServer(assets),
			Middleware: utils.HttpMiddleWare,
		},
		LogLevel: slog.Level(pkgInfo.LogLevel),
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		RawMessageHandler: func(window application.Window, message string, originInfo *application.OriginInfo) {
			raw_message_handler.NewRawMessageHandler().HandleMessage(window, message, originInfo)
		},
	})

	// 本来想加启动屏 但是wails3 对于启动屏的支持实在是有点拉跨。。。 算了吧那就

	mainWindow := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:  "main-window",
		Title: pkgInfo.Title,
		// 启动全屏
		StartState: application.WindowStateMaximised,
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
				window.OpenDevTools()
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
	// 宽高
	width := pkgInfo.Width
	height := pkgInfo.Height
	if width != 0 || height != 0 {
		mainWindow.SetSize(width, height)
	}

	// register service
	services.Register(app)

	// 每一秒向客户端发送当前时间
	go func() {
		for {
			now := time.Now().Format("2006/01/02 15:04:05")
			app.Event.Emit("time", now)
			time.Sleep(time.Second)
		}
	}()

	// Run the application. This blocks until the application has been exited.
	err := app.Run()

	// If an error occurred while running the application, log it and exit.
	if err != nil {
		log.Fatal(err)
	}
}
