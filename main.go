package main

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	"hybrid-infra/go/host"
	"hybrid-infra/go/services"
	"hybrid-infra/go/setting"
	"hybrid-infra/go/utils"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

// init 向 Wails 注册 time 事件的数据类型，使前端接收的时间事件按字符串处理。
func init() { application.RegisterEvent[string]("time") }

// main 读取配置并组装 Wails 应用、权限中间件、宿主管理器和前端服务；按需启用单实例及托盘，最后启动应用事件循环。
func main() {
	config, err := setting.New().Parse()
	if err != nil {
		log.Fatal(err)
	}
	dir, err := setting.DataDir()
	if err != nil {
		log.Fatal(err)
	}
	controller := host.New(config, dir)
	options := application.Options{
		Name: "hybrid-infra", Description: "Hybrid desktop host",
		Assets:   application.AssetOptions{Handler: application.BundledAssetFileServer(assets), Middleware: application.ChainMiddleware(utils.AssetMiddleware(config.TrustedOrigins()), controller.Policy.Middleware)},
		LogLevel: slog.Level(config.LogLevel), MarshalError: host.MarshalError,
		Mac:               application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: config.CloseBehavior != "hide"},
		Windows:           application.WindowsOptions{DisableQuitOnLastWindowClosed: config.CloseBehavior == "hide"},
		Linux:             application.LinuxOptions{DisableQuitOnLastWindowClosed: config.CloseBehavior == "hide"},
		ShouldQuit:        controller.ShouldQuit,
		RawMessageHandler: controller.NativeMessage,
	}
	if config.SingleInstance {
		instanceKey := fmt.Sprintf("hybrid-infra-%x", sha256.Sum256([]byte(dir)))
		// 第二实例只转交固定协议路由，其余启动参数不作为系统命令执行。
		options.SingleInstance = &application.SingleInstanceOptions{UniqueID: instanceKey, OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
			controller.WakeMain()
			controller.HandleActivation(data.Args)
		}}
	}
	app := application.New(options)
	controller.Attach(app)
	windowOptions := controller.InitialOptions()
	windowOptions.Mac = application.MacWindow{InvisibleTitleBarHeight: 50, Backdrop: application.MacBackdropTranslucent, TitleBar: application.MacTitleBarHiddenInset}
	mainWindow := app.Window.NewWithOptions(windowOptions)
	controller.TrackMain(mainWindow)
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) { controller.HandleActivation(os.Args[1:]) })
	app.Event.OnApplicationEvent(events.Common.ApplicationLaunchedWithUrl, func(e *application.ApplicationEvent) { controller.HandleActivation([]string{e.Context().URL()}) })
	if err = services.Register(app, controller); err != nil {
		log.Fatal(err)
	}
	if config.TrayEnabled || config.CloseBehavior == "hide" {
		icon, readErr := assets.ReadFile("frontend/dist/wails.png")
		if readErr != nil {
			log.Fatal(readErr)
		}
		controller.InstallTray(icon)
	}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case now := <-ticker.C:
				app.Event.Emit("time", now.Format("2006/01/02 15:04:05"))
				controller.UpdateTrayStatus()
			case <-app.Context().Done():
				return
			}
		}
	}()
	if err = app.Run(); err != nil {
		log.Fatal(err)
	}
}
