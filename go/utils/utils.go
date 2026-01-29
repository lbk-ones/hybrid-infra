package utils

import (
	"encoding/json"
	"github.com/kbinani/screenshot"
	"hybrid-infra/go/utils/cross"
	"image"
	"image/png"
	"log"
	"os"
	"strings"
)

type NewWindowOptions struct {
	Name            string `json:"name"`            // 窗口名称
	Width           int    `json:"width"`           // 宽度
	Height          int    `json:"height"`          // 高度
	Title           string `json:"title"`           // 标题
	Place           string `json:"place"`           // 位置 center、bottomLeft、bottomRight、topRight、topLeft
	FrameLess       bool   `json:"frameLess"`       // 无边框
	Url             string `json:"url"`             // 打开地址
	IsFullscreen    bool   `json:"isFullscreen"`    // 是否全屏
	AlwaysOnTop     bool   `json:"alwaysOnTop"`     // 是否在顶层
	HiddenOnTaskbar bool   `json:"hiddenOnTaskbar"` // 在任务栏隐藏图标
	DisableResize   bool   `json:"disableResize"`   // 是否禁止缩放
	F5Reload        bool   `json:"f5Reload"`        // f5键刷新
	F12DevTools     bool   `json:"f12DevTools"`     // f12DevTools
}

// StrWithDefault 工具函数1：字符串空值判断，返回原值或默认值（替代 str == "" ? str : def）
func StrWithDefault(str, def string) string {
	trim := strings.TrimSpace(str)
	if trim != "" {
		return trim
	}
	return def
}

// GetJsonStr
func GetJsonStr(obj interface{}) string {
	bytes, _ := json.Marshal(obj)
	return string(bytes)
}

// Screenshot 截图
func Screenshot(path string) {
	box, _ := cross.GetScreenSize()
	width := box.Width
	height := box.Height

	// 2. 截取工作区区域，而非全屏
	bounds := image.Rect(int(box.Left), int(box.Top), int(width), int(height))
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		log.Fatalf("截图失败: %v", err)
	}

	// 3. 保存图片
	file, err := os.Create(path)
	if err != nil {
		log.Fatalf("创建文件失败: %v", err)
	}
	defer file.Close()
	_ = png.Encode(file, img)
}
