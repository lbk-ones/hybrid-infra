package main

import (
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"os"
	"runtime"
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

//go:embed frontend/public/app-icon.png frontend/public/tray-icon.png
var iconAssets embed.FS

// init 向 Wails 注册 time 事件的数据类型，使前端接收的时间事件按字符串处理。
func init() { application.RegisterEvent[string]("time") }

// main 读取配置并组装 Wails 应用、权限中间件、宿主管理器和前端服务；按需启用单实例及托盘，最后启动应用事件循环。
func main() {
	// 发布工具读取编译后的真实版本，提前返回，不初始化数据目录或 WebView。
	if len(os.Args) == 2 && os.Args[1] == "--hybrid-release-info" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"version": host.Version, "platform": runtime.GOOS, "arch": runtime.GOARCH, "dataSchema": host.DataSchema})
		return
	}
	if handled, err := host.RunUpdateWorker(os.Args[1:]); handled {
		if err != nil {
			log.Print(err)
			os.Exit(1)
		}
		return
	}
	config, err := setting.New().Parse()
	if err != nil {
		log.Fatal(err)
	}
	dir, err := setting.DataDir()
	if err != nil {
		log.Fatal(err)
	}
	controller := host.New(config, dir)
	maintenance := host.NewMaintenance(controller, dir)
	controller.OnMainRuntimeReady(maintenance.MarkUpdateHealthy)
	appIcon, err := iconAssets.ReadFile("frontend/public/app-icon.png")
	if err != nil {
		log.Fatal(err)
	}
	trayIcon, err := iconAssets.ReadFile("frontend/public/tray-icon.png")
	if err != nil {
		log.Fatal(err)
	}
	options := application.Options{
		Icon: appIcon,
		Name: "hybrid-infra", Description: "Hybrid desktop host",
		Assets: application.AssetOptions{Handler: application.BundledAssetFileServer(assets), Middleware: application.ChainMiddleware(utils.AssetMiddleware(config.TrustedOrigins()), controller.Policy.Middleware)},
		// 仅记录错误码，避免将底层错误中的业务内容写入诊断。
		LogLevel: slog.Level(config.LogLevel), MarshalError: func(err error) []byte { maintenance.RecordError(err); return host.MarshalError(err) },
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
	// Wails 已完成单实例转交，只有主实例可迁移或恢复数据，避免第二次启动改写正在使用的 KV。
	releaseData, err := maintenance.PrepareData()
	if err != nil {
		log.Fatal(err)
	}
	defer releaseData()
	controller.Attach(app)
	if err = controller.InstallIcons(appIcon, trayIcon); err != nil {
		log.Fatal(err)
	}
	windowOptions := controller.InitialOptions()
	windowOptions.Mac = application.MacWindow{InvisibleTitleBarHeight: 50, Backdrop: application.MacBackdropTranslucent, TitleBar: application.MacTitleBarHiddenInset}
	mainWindow := app.Window.NewWithOptions(windowOptions)
	controller.TrackMain(mainWindow)
	controller.InstallSplash()
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) { controller.HandleActivation(os.Args[1:]) })
	app.Event.OnApplicationEvent(events.Common.ApplicationLaunchedWithUrl, func(e *application.ApplicationEvent) { controller.HandleActivation([]string{e.Context().URL()}) })
	if err = services.Register(app, controller, maintenance); err != nil {
		log.Fatal(err)
	}
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) { maintenance.StartMaintenance() })
	if config.TrayEnabled || config.CloseBehavior == "hide" {
		controller.InstallTray(trayIcon)
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
