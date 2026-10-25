//go:build windows

package host

import (
	"github.com/lxn/win"
	"github.com/wailsapp/wails/v3/pkg/application"
	"runtime"
	"unsafe"
)

// windowsWindowIcons 持有自己创建的大小图标；多个窗口共享句柄，切换完成后统一释放。
type windowsWindowIcons struct{ small, big win.HICON }

// createPNGIcon 让 Windows 从经过校验的 PNG 创建独立 HICON，不使用 LR_SHARED，因而必须由宿主释放。
func createPNGIcon(data []byte, size int) (win.HICON, error) {
	handle, _, _ := user32.NewProc("CreateIconFromResourceEx").Call(uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), 1, 0x00030000, uintptr(size), uintptr(size), 0)
	runtime.KeepAlive(data)
	if handle == 0 {
		return 0, Fail("E_ICON_APPLY", "Windows 无法创建图标")
	}
	return win.HICON(handle), nil
}

// newNativeWindowIcons 提前创建标题栏和任务栏所需的两个尺寸；第二次创建失败时释放第一个，保持旧图标有效。
func newNativeWindowIcons(data []byte) (nativeWindowIcons, error) {
	small, err := createPNGIcon(data, 32)
	if err != nil {
		return nil, err
	}
	big, err := createPNGIcon(data, 64)
	if err != nil {
		win.DestroyIcon(small)
		return nil, err
	}
	return &windowsWindowIcons{small, big}, nil
}

// Apply 为当前原生窗口设置 ICON_SMALL 与 ICON_BIG，兼容标题栏、Alt+Tab 和运行中的任务栏按钮。
func (i *windowsWindowIcons) Apply(window application.Window) {
	if handle := window.NativeWindow(); handle != nil {
		win.SendMessage(win.HWND(uintptr(handle)), win.WM_SETICON, 0, uintptr(i.small))
		win.SendMessage(win.HWND(uintptr(handle)), win.WM_SETICON, 1, uintptr(i.big))
	}
}

// Close 只销毁本对象拥有的图标，不销毁 WM_SETICON 返回的 Wails 资源句柄。
func (i *windowsWindowIcons) Close() {
	if i.small != 0 {
		win.DestroyIcon(i.small)
		i.small = 0
	}
	if i.big != 0 {
		win.DestroyIcon(i.big)
		i.big = 0
	}
}

// clearWindowIcons 在释放共享句柄前让窗口回到编译资源默认图标；仅用于应用退出。
func clearWindowIcons(window application.Window) {
	if handle := window.NativeWindow(); handle != nil {
		win.SendMessage(win.HWND(uintptr(handle)), win.WM_SETICON, 0, 0)
		win.SendMessage(win.HWND(uintptr(handle)), win.WM_SETICON, 1, 0)
	}
}
