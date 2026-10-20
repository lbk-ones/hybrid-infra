package services

import (
	"fmt"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/kvstore"
	logService "github.com/wailsapp/wails/v3/pkg/services/log"
	"hybrid-infra/go/setting"
	"os"
	"path/filepath"
)

func Register(app *application.App) error {
	dir, err := setting.DataDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	app.RegisterService(application.NewService(&GreetService{}))
	app.RegisterService(application.NewService(kvstore.NewWithConfig(&kvstore.Config{
		Filename: filepath.Join(dir, "store.json"),
		AutoSave: true,
	})))
	app.RegisterService(application.NewService(logService.New()))
	app.RegisterService(application.NewService(NewUiService()))
	return nil
}
