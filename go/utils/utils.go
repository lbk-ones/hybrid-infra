package utils

import (
	"encoding/json"
	"fmt"
	"github.com/kbinani/screenshot"
	"hybrid-infra/go/utils/cross"
	"image"
	"image/png"
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

// StrWithDefault 去掉字符串首尾空白；仍有内容时返回清理后的字符串，否则返回默认值。
func StrWithDefault(str, def string) string {
	trim := strings.TrimSpace(str)
	if trim != "" {
		return trim
	}
	return def
}

// GetJsonStr 把对象序列化成 JSON 字符串；此兼容工具不返回序列化错误，需要检查错误时应直接调用 json.Marshal。
func GetJsonStr(obj interface{}) string {
	bytes, _ := json.Marshal(obj)
	return string(bytes)
}

// Screenshot 按主显示器工作区的物理像素截图，并以 PNG 格式写入指定路径；捕获或保存失败时返回错误。
func Screenshot(path string) error {
	box, err := cross.GetPhysicalScreenSize()
	if err != nil {
		return err
	}
	width := box.Width
	height := box.Height

	// 2. 截取工作区区域，而非全屏
	bounds := image.Rect(int(box.Left), int(box.Top), int(box.Left+width), int(box.Top+height))
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		return fmt.Errorf("capture screen: %w", err)
	}

	// 3. 保存图片
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create screenshot: %w", err)
	}
	if err := png.Encode(file, img); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
