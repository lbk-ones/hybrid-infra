package host

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/kbinani/screenshot"
	"github.com/wailsapp/wails/v3/pkg/application"
	"hybrid-infra/go/utils"
)

const maxCapturePixels int64 = 16 * 1024 * 1024
const maxCapturePNG = 20 * 1024 * 1024

// CaptureRegion 使用虚拟桌面的物理像素坐标；负坐标用于主屏左侧或上侧的显示器。
type CaptureRegion struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// CaptureDisplay 同时返回物理像素和 Wails 逻辑坐标，调用者无需猜测高 DPI 缩放比例。
type CaptureDisplay struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Primary  bool          `json:"primary"`
	Physical CaptureRegion `json:"physical"`
	Logical  CaptureRegion `json:"logical"`
}

// ScreenshotReq 支持 region 坐标截图或 select 手动框选；output 为 base64、clipboard 或 both，默认 base64。
type ScreenshotReq struct {
	Mode      string `json:"mode"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	DisplayID string `json:"displayId"`
	Output    string `json:"output"`
}

// ScreenshotResult 只在选择对应输出时携带 Base64；PNG 不落磁盘，clipboard 表示已成功写入系统剪贴板。
type ScreenshotResult struct {
	Region    CaptureRegion `json:"region"`
	MIME      string        `json:"mime"`
	Base64    string        `json:"base64,omitempty"`
	Clipboard bool          `json:"clipboard"`
}
type captureAnswer struct {
	region CaptureRegion
	err    error
}
type captureSelection struct {
	token, owner, name string
	windowID           uint
	display            CaptureDisplay
	once               sync.Once
	answer             chan captureAnswer
}

// regionFromRect 将 Wails 屏幕矩形转成前端统一的小写字段，不改变坐标单位。
func regionFromRect(r application.Rect) CaptureRegion {
	return CaptureRegion{r.X, r.Y, r.Width, r.Height}
}

// captureRect 将已校验的物理像素区域转成 Go 图像矩形，右下角由起点加宽高计算。
func captureRect(r CaptureRegion) image.Rectangle {
	return image.Rect(r.X, r.Y, r.X+r.Width, r.Y+r.Height)
}

// CaptureDisplays 查询可截图显示器，只有 screenshot 能力可读取；物理区域包含任务栏而不只是工作区。
func (d *Desktop) CaptureDisplays(ctx context.Context) ([]CaptureDisplay, error) {
	if err := Require(ctx, "screenshot"); err != nil {
		return nil, err
	}
	result := []CaptureDisplay{}
	for _, s := range d.app.Screen.GetAll() {
		if s.PhysicalBounds.Width > 0 && s.PhysicalBounds.Height > 0 {
			result = append(result, CaptureDisplay{ID: s.ID, Name: s.Name, Primary: s.IsPrimary, Physical: regionFromRect(s.PhysicalBounds), Logical: regionFromRect(s.Bounds)})
		}
	}
	if len(result) == 0 {
		return nil, Fail("E_DISPLAY", "未发现可截图的显示器")
	}
	return result, nil
}

// normalizeScreenshot 验证模式和输出类型；剪贴板输出的额外权限由 Screenshot 在采集之前检查。
func normalizeScreenshot(req ScreenshotReq) (ScreenshotReq, error) {
	if req.Mode == "" {
		req.Mode = "region"
	}
	if req.Output == "" {
		req.Output = "base64"
	}
	if req.Mode != "region" && req.Mode != "select" {
		return req, Fail("E_INVALID_ARGUMENT", "截图 mode 必须是 region 或 select")
	}
	if req.Output != "base64" && req.Output != "clipboard" && req.Output != "both" {
		return req, Fail("E_INVALID_ARGUMENT", "截图 output 必须是 base64、clipboard 或 both")
	}
	return req, nil
}

// validateCaptureRegion 拒绝超大分配、整数溢出和虚拟桌面之外的截图；允许跨相邻屏幕，屏幕间空隙输出黑色。
func validateCaptureRegion(region CaptureRegion, displays []CaptureDisplay) error {
	if region.Width < 1 || region.Height < 1 || region.Width > 16384 || region.Height > 16384 || region.X < -1<<24 || region.X > 1<<24 || region.Y < -1<<24 || region.Y > 1<<24 || int64(region.Width)*int64(region.Height) > maxCapturePixels {
		return Fail("E_INVALID_ARGUMENT", "截图宽高必须为正，单边最多 16384，合计最多 1600 万像素")
	}
	target := captureRect(region)
	var bounds image.Rectangle
	intersects := false
	for _, display := range displays {
		r := captureRect(display.Physical)
		bounds = bounds.Union(r)
		if !r.Intersect(target).Empty() {
			intersects = true
		}
	}
	if !intersects || !target.In(bounds) {
		return Fail("E_INVALID_ARGUMENT", "截图区域超出虚拟桌面")
	}
	return nil
}

// capturePixels 按显示器交集采集并拼接像素，跨屏空隙填黑；capture 参数可替换为测试图像，避免测试读取用户桌面。
func capturePixels(region CaptureRegion, displays []CaptureDisplay, capture func(image.Rectangle) (*image.RGBA, error)) (*image.RGBA, error) {
	if err := validateCaptureRegion(region, displays); err != nil {
		return nil, err
	}
	target := captureRect(region)
	result := image.NewRGBA(image.Rect(0, 0, region.Width, region.Height))
	draw.Draw(result, result.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	for _, display := range displays {
		part := target.Intersect(captureRect(display.Physical))
		if part.Empty() {
			continue
		}
		img, err := capture(part)
		if err != nil {
			if errors.Is(err, screenshot.ErrUnsupported) {
				return nil, Fail("E_UNSUPPORTED", "当前平台不支持屏幕采集")
			}
			return nil, Fail("E_CAPTURE", "屏幕采集失败，请检查系统屏幕录制权限")
		}
		dest := part.Sub(target.Min)
		draw.Draw(result, dest, img, img.Bounds().Min, draw.Src)
	}
	return result, nil
}

// Screenshot 执行坐标截图或等待框选，默认返回 PNG Base64；写剪贴板另需 clipboard 权限，每次最多一个截图操作。
func (d *Desktop) Screenshot(ctx context.Context, req ScreenshotReq) (ScreenshotResult, error) {
	if err := Require(ctx, "screenshot"); err != nil {
		return ScreenshotResult{}, err
	}
	req, err := normalizeScreenshot(req)
	if err != nil {
		return ScreenshotResult{}, err
	}
	if req.Output != "base64" {
		if err = Require(ctx, "clipboard"); err != nil {
			return ScreenshotResult{}, err
		}
	}
	if !d.captureMu.TryLock() {
		return ScreenshotResult{}, Fail("E_BUSY", "已有截图或框选正在进行")
	}
	defer d.captureMu.Unlock()
	_, owner := CallerIdentity(ctx)
	window, ok := d.app.Window.GetByName(owner)
	if !ok {
		return ScreenshotResult{}, Fail("E_WINDOW_NOT_FOUND", "截图必须从桌面窗口发起")
	}
	displays, err := d.CaptureDisplays(ctx)
	if err != nil {
		return ScreenshotResult{}, err
	}
	region := CaptureRegion{req.X, req.Y, req.Width, req.Height}
	var img image.Image
	if req.Mode == "select" {
		img, region, err = d.selectCapture(ctx, req, displays, window)
	} else {
		img, err = capturePixels(region, displays, screenshot.CaptureRect)
	}
	if err != nil {
		return ScreenshotResult{}, err
	}
	if ctx.Err() != nil {
		return ScreenshotResult{}, Fail("E_CANCELLED", "截图调用已取消")
	}
	return encodeScreenshot(img, region, req.Output, func(data []byte) error {
		if ctx.Err() != nil {
			return Fail("E_CANCELLED", "截图调用已取消")
		}
		_, err := clipboardImage(data, true, uintptr(window.NativeWindow()))
		return err
	})
}

// encodeScreenshot 统一 PNG 编码和输出选择，writer 只在需要剪贴板时调用；测试可替换 writer 而不改动用户剪贴板。
func encodeScreenshot(img image.Image, region CaptureRegion, output string, writer func([]byte) error) (ScreenshotResult, error) {
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		return ScreenshotResult{}, Fail("E_CAPTURE", "无法编码 PNG")
	}
	if buffer.Len() > maxCapturePNG {
		return ScreenshotResult{}, Fail("E_TOO_LARGE", "PNG 超过 20 MiB，请缩小截图区域")
	}
	result := ScreenshotResult{Region: region, MIME: "image/png"}
	if output != "base64" {
		if err := writer(buffer.Bytes()); err != nil {
			return ScreenshotResult{}, err
		}
		result.Clipboard = true
	}
	if output != "clipboard" {
		result.Base64 = base64.StdEncoding.EncodeToString(buffer.Bytes())
	}
	return result, nil
}

// chooseCaptureDisplay 优先使用显式显示器，其次使用调用窗口所在屏幕，再回退主屏；未知显示器不会悄悄截图其他屏幕。
func chooseCaptureDisplay(displays []CaptureDisplay, id, current string) (CaptureDisplay, error) {
	if id != "" {
		for _, s := range displays {
			if s.ID == id {
				return s, nil
			}
		}
		return CaptureDisplay{}, Fail("E_INVALID_ARGUMENT", "指定显示器不存在")
	}
	for _, s := range displays {
		if s.ID == current {
			return s, nil
		}
	}
	for _, s := range displays {
		if s.Primary {
			return s, nil
		}
	}
	if len(displays) > 0 {
		return displays[0], nil
	}
	return CaptureDisplay{}, Fail("E_DISPLAY", "没有可用显示器")
}

// selectCapture 先冻结指定显示器，再打开不可拖拽遮罩；选择结果只裁剪冻结图像，避免把遮罩自身拍进截图。
func (d *Desktop) selectCapture(ctx context.Context, req ScreenshotReq, displays []CaptureDisplay, parent application.Window) (image.Image, CaptureRegion, error) {
	current := ""
	if screen, err := parent.GetScreen(); err == nil && screen != nil {
		current = screen.ID
	}
	display, err := chooseCaptureDisplay(displays, req.DisplayID, current)
	if err != nil {
		return nil, CaptureRegion{}, err
	}
	frozen, err := capturePixels(display.Physical, displays, screenshot.CaptureRect)
	if err != nil {
		return nil, CaptureRegion{}, err
	}
	var buffer bytes.Buffer
	if err = png.Encode(&buffer, frozen); err != nil || buffer.Len() > maxCapturePNG {
		return nil, CaptureRegion{}, Fail("E_TOO_LARGE", "框选背景过大，请使用坐标截图")
	}
	token, err := randomID()
	if err != nil {
		return nil, CaptureRegion{}, err
	}
	_, owner := CallerIdentity(ctx)
	selection := &captureSelection{token: token, owner: owner, name: "hybrid-infra-capture-" + token, display: display, answer: make(chan captureAnswer, 1)}
	d.selectionMu.Lock()
	d.selection = selection
	d.selectionMu.Unlock()
	w := d.Controller.CreateDialog(ctx, utils.NewWindowOptions{Name: selection.name, Title: "框选截图", Url: "/Capture.html", Width: display.Logical.Width, Height: display.Logical.Height, FrameLess: true, AlwaysOnTop: true, DisableResize: true})
	d.selectionMu.Lock()
	selection.windowID = w.ID()
	d.selectionMu.Unlock()
	defer func() {
		d.selectionMu.Lock()
		if d.selection == selection {
			d.selection = nil
		}
		d.selectionMu.Unlock()
		w.Close()
	}()
	w.SetBounds(application.Rect{X: display.Logical.X, Y: display.Logical.Y, Width: display.Logical.Width, Height: display.Logical.Height})
	w.Fullscreen()
	data, _ := json.Marshal(map[string]any{"token": token, "image": "data:image/png;base64," + base64.StdEncoding.EncodeToString(buffer.Bytes()), "width": display.Physical.Width, "height": display.Physical.Height})
	w.ExecJS(`window.__HYBRID_CAPTURE_DATA__=` + string(data) + `;window.dispatchEvent(new Event('hybrid:capture-config'));`)
	w.Show()
	w.Focus()
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	select {
	case answer := <-selection.answer:
		if answer.err != nil {
			return nil, CaptureRegion{}, answer.err
		}
		local := captureRect(answer.region).Sub(image.Pt(display.Physical.X, display.Physical.Y))
		return frozen.SubImage(local), answer.region, nil
	case <-ctx.Done():
		return nil, CaptureRegion{}, Fail("E_CANCELLED", "截图调用已取消")
	case <-d.app.Context().Done():
		return nil, CaptureRegion{}, Fail("E_CANCELLED", "应用正在退出")
	case <-timer.C:
		return nil, CaptureRegion{}, Fail("E_TIMEOUT", "框选超过两分钟，已取消")
	}
}

// captureMessage 只接受内部遮罩窗口带随机凭证的原生消息；顶层来源由 Controller 预检，不通过普通 HTTP 广播传递屏幕图像。
func (d *Desktop) captureMessage(window application.Window, message string, source *application.OriginInfo) bool {
	var data struct {
		Type                                               string `json:"type"`
		Token                                              string `json:"token"`
		X, Y, Width, Height, ViewportWidth, ViewportHeight float64
	}
	if !strings.HasPrefix(message, "hybrid:") || json.Unmarshal([]byte(strings.TrimPrefix(message, "hybrid:")), &data) != nil || !strings.HasPrefix(data.Type, "capture-") {
		return false
	}
	if source == nil || !localOrigin(source.Origin) {
		return true
	}
	d.selectionMu.Lock()
	selection := d.selection
	if selection == nil || selection.windowID != window.ID() || selection.name != window.Name() || selection.token != data.Token {
		d.selectionMu.Unlock()
		return true
	}
	d.selectionMu.Unlock()
	if data.Type == "capture-cancel" {
		selection.once.Do(func() { selection.answer <- captureAnswer{err: Fail("E_CANCELLED", "已取消框选")} })
		return true
	}
	if data.Type == "capture-select" {
		region, err := physicalSelection(selection.display.Physical, data.X, data.Y, data.Width, data.Height, data.ViewportWidth, data.ViewportHeight)
		if err == nil {
			selection.once.Do(func() { selection.answer <- captureAnswer{region: region} })
		}
	}
	return true
}

// physicalSelection 按遮罩真实 viewport 比例转换物理像素，支持高 DPI；边界向外取整并限制在所选显示器内。
func physicalSelection(display CaptureRegion, x, y, width, height, vw, vh float64) (CaptureRegion, error) {
	if display.Width < 1 || display.Height < 1 {
		return CaptureRegion{}, Fail("E_INVALID_ARGUMENT", "显示器尺寸无效")
	}
	for _, n := range []float64{x, y, width, height, vw, vh} {
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return CaptureRegion{}, Fail("E_INVALID_ARGUMENT", "框选坐标无效")
		}
	}
	if vw <= 0 || vh <= 0 || vw > 100000 || vh > 100000 || x < 0 || y < 0 || width <= 0 || height <= 0 || x+width > vw+0.01 || y+height > vh+0.01 {
		return CaptureRegion{}, Fail("E_INVALID_ARGUMENT", "框选超出遮罩区域")
	}
	left := max(0, int(math.Floor(x*float64(display.Width)/vw)))
	top := max(0, int(math.Floor(y*float64(display.Height)/vh)))
	right := min(display.Width, int(math.Ceil((x+width)*float64(display.Width)/vw)))
	bottom := min(display.Height, int(math.Ceil((y+height)*float64(display.Height)/vh)))
	if right <= left || bottom <= top {
		return CaptureRegion{}, Fail("E_INVALID_ARGUMENT", "框选区域过小")
	}
	return CaptureRegion{display.X + left, display.Y + top, right - left, bottom - top}, nil
}

// cancelSelection 取消当前遮罩等待，实际窗口由 selectCapture 的 defer 统一关闭；多次取消只交付一次结果。
func (d *Desktop) cancelSelection() {
	d.selectionMu.Lock()
	selection := d.selection
	d.selectionMu.Unlock()
	if selection != nil {
		selection.once.Do(func() { selection.answer <- captureAnswer{err: Fail("E_CANCELLED", "框选窗口或来源已关闭")} })
	}
}

// captureWindowClosed 在遮罩或来源窗口关闭时取消等待，避免页面离开后继续持有冻结的屏幕图像。
func (d *Desktop) captureWindowClosed(name string) {
	d.selectionMu.Lock()
	selection := d.selection
	matches := selection != nil && (selection.owner == name || selection.name == name)
	d.selectionMu.Unlock()
	if matches {
		selection.once.Do(func() { selection.answer <- captureAnswer{err: Fail("E_CANCELLED", "框选窗口或来源已关闭")} })
	}
}
