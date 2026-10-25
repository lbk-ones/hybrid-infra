package services

import (
	"fmt"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/kvstore"
	logService "github.com/wailsapp/wails/v3/pkg/services/log"
	"hybrid-infra/go/host"
	"hybrid-infra/go/setting"
	"os"
	"path/filepath"
)

// Register 创建应用数据目录，注册前端可调用的服务，并同步登记每个服务方法所需的能力，保证绑定与授权一致。
func Register(app *application.App, controller *host.Controller, maintenance *host.Maintenance) error {
	dir, err := setting.DataDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	// 同时登记 RPC 能力与 Wails 绑定，避免服务可调用但忘记配置所需权限。
	register := func(service application.Service, capability string, overrides map[string]string) {
		controller.Policy.Register(service.Instance(), capability, overrides)
		app.RegisterService(service)
	}
	register(application.NewService(&GreetService{}), "host", nil)
	register(application.NewService(kvstore.NewWithConfig(&kvstore.Config{
		Filename: filepath.Join(dir, "store.json"),
		AutoSave: true,
	})), "store", nil)
	register(application.NewService(logService.New()), "log", nil)
	register(application.NewService(NewUiService(controller)), "ui", map[string]string{"Show": "ui.html", "OpenWindowByUrl": "windows"})
	register(application.NewService(&HostService{controller}), "host", map[string]string{"BeginTask": "lifecycle", "EndTask": "lifecycle", "GetPendingTasks": "lifecycle", "Quit": "lifecycle"})
	register(application.NewService(&WindowService{controller}), "windows", nil)
	desktop := host.NewDesktop(app, controller, dir)
	maintenance.AttachDesktop(desktop)
	register(application.NewService(&MaintenanceService{manager: maintenance}), "diagnostics", map[string]string{"ListBackups": "backups", "CreateBackup": "backups", "StageRestore": "backups", "GetUpdateState": "updates", "CheckUpdates": "updates", "DownloadUpdate": "updates", "CancelUpdate": "updates", "InstallUpdate": "updates", "RollbackUpdate": "updates"})
	app.OnShutdown(maintenance.CancelUpdate)
	bus := host.NewEventBus()
	commands := host.NewCommandManager(controller)
	controller.OnResourceWindowClosed(bus.CloseWindow)
	controller.OnResourceWindowClosed(commands.CloseWindow)
	app.OnShutdown(bus.Shutdown)
	app.OnShutdown(commands.Shutdown)
	register(application.NewService(&CacheService{cache: host.NewSharedCache()}), "cache", nil)
	register(application.NewService(&EventService{bus: bus}), "events", nil)
	register(application.NewService(&ShellService{commands: commands}), "shell", nil)
	register(application.NewService(&DesktopService{desktop: desktop}), "files", map[string]string{
		"CaptureDisplays": "screenshot", "Screenshot": "screenshot",
		"StartDownload": "downloads", "ListDownloads": "downloads", "CancelDownload": "downloads", "RetryDownload": "downloads", "OpenDownload": "downloads", "RemoveDownload": "downloads",
		"ClipboardText": "clipboard", "ClipboardImage": "clipboard", "Notify": "notifications", "SetShortcut": "shortcuts", "ConfigureProtocol": "protocol", "Credential": "credentials",
		"GetSettings": "settings", "SaveSettings": "settings", "SavePreferences": "settings", "ResetPreferences": "settings", "ReloadIcons": "settings",
	})
	return nil
}
