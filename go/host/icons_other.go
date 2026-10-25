//go:build !windows

package host

import "github.com/wailsapp/wails/v3/pkg/application"

// applicationWindowIcons 在其他桌面平台由 Wails App.SetIcon 管理应用图标，不伪造 Windows 句柄。
type applicationWindowIcons struct{}

// newNativeWindowIcons 返回无原生句柄的适配器，应用层随后调用 Wails 的平台实现。
func newNativeWindowIcons(data []byte) (nativeWindowIcons, error) {
	return &applicationWindowIcons{}, nil
}

// Apply 其他平台不发送 Windows 窗口消息。
func (i *applicationWindowIcons) Apply(window application.Window) {}

// Close 其他平台没有本对象持有的原生资源。
func (i *applicationWindowIcons) Close() {}

// clearWindowIcons 其他平台由 Wails 在应用退出时处理资源。
func clearWindowIcons(window application.Window) {}
