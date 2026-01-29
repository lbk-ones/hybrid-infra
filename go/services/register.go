package services

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/kvstore"
	logService "github.com/wailsapp/wails/v3/pkg/services/log"
)

func Register(app *application.App) {
	app.RegisterService(application.NewService(&GreetService{}))
	app.RegisterService(application.NewService(kvstore.NewWithConfig(&kvstore.Config{
		Filename: "store.json",
		AutoSave: true,
	})))
	app.RegisterService(application.NewService(logService.New()))
	app.RegisterService(application.NewService(NewUiService()))
}
