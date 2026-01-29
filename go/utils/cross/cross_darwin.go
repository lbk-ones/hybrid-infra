//go:build darwin

package cross

// 引用macOS Cocoa/Quartz框架（系统原生，cgo调用）
/*
   #cgo LDFLAGS: -framework Cocoa -framework CoreGraphics
   #include <CoreGraphics/CoreGraphics.h>
   #include <Cocoa/Cocoa.h>

   // 获取指定显示器的可用工作区（排除菜单栏、Dock）
   CGRect getMacWorkArea(CGDirectDisplayID displayID) {
       NSArray *screens = [NSScreen screens];
       NSScreen *screen = [screens objectAtIndex:0];
       if (displayID < [screens count]) {
           screen = [screens objectAtIndex:displayID];
       }
       // visibleFrame：排除菜单栏和Dock的可用区域
       return [screen visibleFrame];
   }
*/
import "C"

// GetScreenSize macOS平台获取指定显示器的工作区
func GetScreenSize() (BOX, error) {
	// 获取指定显示器的CGDirectDisplayID（Quartz框架标识）
	displayID := C.CGDirectDisplayID(0)
	// 调用cgo方法获取可用工作区（visibleFrame）
	cRect := C.getMacWorkArea(displayID)

	// 转换CGRect为Go的WorkArea（macOS坐标原点在左下角，转换为左上角）
	// 获取屏幕主分辨率，修正坐标方向
	screenBounds := screenshot.GetDisplayBounds(index)
	top := int(screenBounds.Max.Y) - int(cRect.size.height) - int(cRect.origin.y)

	return BOX{
		Left:   int(cRect.origin.x),
		Top:    top,
		Width:  int(cRect.size.width),
		Height: int(cRect.size.height),
	}, nil
}
