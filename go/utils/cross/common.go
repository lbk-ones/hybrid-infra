// Package cross uses Wails' platform screen abstraction instead of duplicating
// Win32, Cocoa and X11 implementations. It requires a running Wails application.
package cross

import (
	"errors"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type BOX struct{ Left, Top, Width, Height int32 }

func screenBox(physical bool) (BOX, error) {
	app := application.Get()
	if app == nil || app.Screen == nil {
		return BOX{}, errors.New("screen manager is not ready")
	}
	screen := app.Screen.GetPrimary()
	if screen == nil {
		return BOX{}, errors.New("primary screen is not available")
	}
	area := screen.WorkArea
	if physical {
		area = screen.PhysicalWorkArea
	}
	if area.Width <= 0 || area.Height <= 0 {
		return BOX{}, errors.New("invalid primary screen work area")
	}
	return BOX{Left: int32(area.X), Top: int32(area.Y), Width: int32(area.Width), Height: int32(area.Height)}, nil
}

// GetScreenSize returns logical coordinates for window placement.
func GetScreenSize() (BOX, error) { return screenBox(false) }

// GetPhysicalScreenSize returns pixel coordinates for screen capture.
func GetPhysicalScreenSize() (BOX, error) { return screenBox(true) }
