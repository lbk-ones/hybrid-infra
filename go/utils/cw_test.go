package utils

import (
	"hybrid-infra/go/utils/cross"
	"testing"
)

// TestWindowPositionWithOffsetWorkArea 验证带偏移及负坐标的工作区角落定位，同时检查未知位置和超大窗口的回退行为。
func TestWindowPositionWithOffsetWorkArea(t *testing.T) {
	box := cross.BOX{Left: -1920, Top: 40, Width: 1920, Height: 1040}
	for _, tc := range []struct {
		place string
		x, y  int
	}{
		{PlaceTopLeft, -1920, 40}, {PlaceTopRight, -300, 40},
		{PlaceBottomLeft, -1920, 930}, {PlaceBottomRight, -300, 930},
	} {
		x, y, ok := windowPosition(box, 300, 150, tc.place)
		if !ok || x != tc.x || y != tc.y {
			t.Errorf("%s: got (%d,%d,%v)", tc.place, x, y, ok)
		}
	}
	if _, _, ok := windowPosition(box, 300, 150, "unknown"); ok {
		t.Fatal("unknown place must fall back to center")
	}
	x, y, ok := windowPosition(box, 3000, 2000, PlaceBottomRight)
	if !ok || x != -1920 || y != 40 {
		t.Fatal("oversize window must stay at work area origin")
	}
}
