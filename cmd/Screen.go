package main

import (
	"fmt"
	"github.com/kbinani/screenshot"
)

func main() {
	// 获取系统中所有激活的显示器数量
	displayCount := screenshot.NumActiveDisplays()
	if displayCount == 0 {
		fmt.Println("未检测到可用的显示器")
		return
	}

	// 获取主屏幕（索引0）的边界信息
	// Bounds结构体包含Min和Max两个点，分别代表屏幕的左上角和右下角坐标
	bounds := screenshot.GetDisplayBounds(0)

	// 计算屏幕宽度：Max.X - Min.X
	width := bounds.Dx()
	// 计算屏幕高度：Max.Y - Min.Y
	height := bounds.Dy()

	// 输出屏幕分辨率
	fmt.Printf("屏幕宽度：%d 像素\n", width)
	fmt.Printf("屏幕高度：%d 像素\n", height)

	// 可选：如果有多个屏幕，可以遍历所有屏幕获取分辨率
	fmt.Println("\n所有屏幕的分辨率：")
	for i := 0; i < displayCount; i++ {
		b := screenshot.GetDisplayBounds(i)
		fmt.Printf("屏幕 %d：%d x %d\n", i, b.Dx(), b.Dy())
	}
}
