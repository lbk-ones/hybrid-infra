//go:build windows

package cross

import (
	"log"
	"syscall"
	"unsafe"
)

// RECT 对应 Windows 的 RECT 结构体
type RECT struct {
	Left, Top, Right, Bottom int32
}

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	systemParametersInfo = user32.NewProc("SystemParametersInfoW")
)

// GetWorkArea 获取 Windows 工作区（排除任务栏）
func getWorkArea() (RECT, error) {
	var rect RECT
	// SPI_GETWORKAREA = 0x0030
	ret, _, err := systemParametersInfo.Call(
		uintptr(0x0030),
		uintptr(0),
		uintptr(unsafe.Pointer(&rect)),
		uintptr(0),
	)
	if ret == 0 {
		return RECT{}, err
	}
	return rect, nil
}
func GetScreenSize() (BOX, error) {
	// 1. 获取工作区（已经去掉任务栏）
	workArea, err := getWorkArea()
	if err != nil {
		log.Fatalf("获取工作区失败: %v", err)
	}
	width := workArea.Right - workArea.Left
	height := workArea.Bottom - workArea.Top
	return BOX{Left: workArea.Left, Top: workArea.Top, Width: width, Height: height}, nil
}
