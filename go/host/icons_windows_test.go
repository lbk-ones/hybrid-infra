//go:build windows

package host

import (
	"github.com/lxn/win"
	"image/color"
	"testing"
)

// TestWindowsIconOwnership 验证 PNG 可创建大小 HICON，系统能读取其信息，并且重复释放不会销毁借用资源。
func TestWindowsIconOwnership(t *testing.T) {
	data := iconFixture(t, 64, color.NRGBA{G: 180, A: 255})
	icons, err := newNativeWindowIcons(data)
	if err != nil {
		t.Fatal(err)
	}
	defer icons.Close()
	native := icons.(*windowsWindowIcons)
	if native.small == 0 || native.big == 0 || native.small == native.big {
		t.Fatal("independent icon handles expected")
	}
	for _, handle := range []win.HICON{native.small, native.big} {
		var info win.ICONINFO
		if !win.GetIconInfo(handle, &info) {
			t.Fatal("Windows could not read icon")
		}
		if info.HbmMask != 0 {
			win.DeleteObject(win.HGDIOBJ(info.HbmMask))
		}
		if info.HbmColor != 0 {
			win.DeleteObject(win.HGDIOBJ(info.HbmColor))
		}
	}
	icons.Close()
	icons.Close()
	if native.small != 0 || native.big != 0 {
		t.Fatal("owned handles were not released")
	}
}
