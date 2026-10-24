package host

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// TestCaptureGeometryAndDPI 验证默认参数、负坐标、多屏边界、尺寸上限及高 DPI 选区换算，不访问真实屏幕。
func TestCaptureGeometryAndDPI(t *testing.T) {
	req, err := normalizeScreenshot(ScreenshotReq{})
	if err != nil || req.Mode != "region" || req.Output != "base64" {
		t.Fatal("capture defaults incorrect")
	}
	screens := []CaptureDisplay{{ID: "left", Physical: CaptureRegion{-1000, 0, 1000, 800}}, {ID: "right", Physical: CaptureRegion{0, 0, 1600, 900}, Primary: true}}
	if err = validateCaptureRegion(CaptureRegion{-100, 10, 300, 200}, screens); err != nil {
		t.Fatal(err)
	}
	for _, r := range []CaptureRegion{{0, 0, 0, 100}, {-1200, 0, 300, 100}, {0, 0, 9000, 9000}, {1 << 25, 0, 10, 10}} {
		if err = validateCaptureRegion(r, screens); err == nil {
			t.Fatalf("invalid capture accepted: %+v", r)
		}
	}
	actual, err := physicalSelection(CaptureRegion{-2000, 0, 2000, 1200}, 100, 50, 200, 100, 1000, 600)
	if err != nil || actual != (CaptureRegion{-1800, 100, 400, 200}) {
		t.Fatalf("DPI selection: %+v %v", actual, err)
	}
	if _, err = physicalSelection(screens[0].Physical, -1, 0, 10, 10, 1000, 800); err == nil {
		t.Fatal("negative viewport selection accepted")
	}
	if _, err = chooseCaptureDisplay(screens, "missing", ""); err == nil {
		t.Fatal("unknown screen silently selected")
	}
	if display, err := chooseCaptureDisplay(screens, "", "left"); err != nil || display.ID != "left" {
		t.Fatal("caller display was not selected")
	}
}

// TestCaptureCompositionAndOutputs 使用测试像素验证跨屏拼接及 Base64/剪贴板输出选择，writer 不接触系统剪贴板。
func TestCaptureCompositionAndOutputs(t *testing.T) {
	screens := []CaptureDisplay{{Physical: CaptureRegion{-20, 0, 20, 20}}, {Physical: CaptureRegion{0, 0, 20, 20}}}
	img, err := capturePixels(CaptureRegion{-10, 0, 20, 10}, screens, func(region image.Rectangle) (*image.RGBA, error) {
		result := image.NewRGBA(image.Rect(0, 0, region.Dx(), region.Dy()))
		for y := 0; y < result.Bounds().Dy(); y++ {
			for x := 0; x < result.Bounds().Dx(); x++ {
				if region.Min.X < 0 {
					result.Set(x, y, color.RGBA{R: 255, A: 255})
				} else {
					result.Set(x, y, color.RGBA{B: 255, A: 255})
				}
			}
		}
		return result, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if img.RGBAAt(0, 0).R != 255 || img.RGBAAt(19, 0).B != 255 {
		t.Fatal("cross-display composition reversed or shifted")
	}
	for _, output := range []string{"base64", "clipboard", "both"} {
		writes := 0
		value, err := encodeScreenshot(img, CaptureRegion{-10, 0, 20, 10}, output, func(data []byte) error {
			writes++
			config, err := png.DecodeConfig(bytes.NewReader(data))
			if err != nil || config.Width != 20 || config.Height != 10 {
				t.Error("invalid PNG clipboard payload")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if (output == "base64" && writes != 0) || (output != "base64" && writes != 1) {
			t.Fatal("clipboard output selection ignored")
		}
		if output == "clipboard" {
			if value.Base64 != "" || !value.Clipboard {
				t.Fatal("clipboard-only returned image data")
			}
		} else {
			data, err := base64.StdEncoding.DecodeString(value.Base64)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = png.Decode(bytes.NewReader(data)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

type captureTestWindow struct {
	application.Window
	id   uint
	name string
}

// ID 返回测试中的真实来源窗口编号，不接受消息体声明的窗口身份。
func (w *captureTestWindow) ID() uint { return w.id }

// Name 返回测试遮罩窗口名。
func (w *captureTestWindow) Name() string { return w.name }

// TestCaptureNativeMessageAndCancellation 验证随机凭证、窗口身份、顶层来源及一次性结果，关闭时不会取消另一个会话。
func TestCaptureNativeMessageAndCancellation(t *testing.T) {
	d, ctx := desktopFixture(t)
	_ = ctx
	selection := &captureSelection{token: "secret", owner: "parent", name: "mask", windowID: 9, display: CaptureDisplay{Physical: CaptureRegion{0, 0, 2000, 1200}}, answer: make(chan captureAnswer, 1)}
	d.selection = selection
	d.Controller.OnNativeMessageHandler(d.captureMessage)
	source := &application.OriginInfo{Origin: "http://wails.localhost", TopOrigin: "http://wails.localhost"}
	window := &captureTestWindow{id: 9, name: "mask"}
	d.Controller.NativeMessage(window, `hybrid:{"type":"capture-select","token":"wrong","x":10,"y":10,"width":100,"height":50,"viewportWidth":1000,"viewportHeight":600}`, source)
	select {
	case <-selection.answer:
		t.Fatal("wrong token accepted")
	default:
	}
	d.Controller.NativeMessage(window, `hybrid:{"type":"capture-select","token":"secret","x":10,"y":10,"width":100,"height":50,"viewportWidth":1000,"viewportHeight":600}`, source)
	result := <-selection.answer
	if result.region != (CaptureRegion{20, 20, 200, 100}) {
		t.Fatalf("wrong physical crop: %+v", result.region)
	}
	d.Controller.NativeMessage(window, `hybrid:{"type":"capture-cancel","token":"secret"}`, source)
	select {
	case <-selection.answer:
		t.Fatal("selection delivered twice")
	default:
	}
	d.selection = &captureSelection{owner: "new-parent", name: "new-mask", answer: make(chan captureAnswer, 1)}
	d.captureWindowClosed("parent")
	select {
	case <-d.selection.answer:
		t.Fatal("unrelated capture cancelled")
	default:
	}
	d.captureWindowClosed("new-parent")
	if result := <-d.selection.answer; result.err == nil {
		t.Fatal("closing owner did not cancel selection")
	}
}

// TestCapturePermissionBeforeSampling 验证没有 screenshot 或 clipboard 授权时，在读取屏幕或创建窗口之前就拒绝调用。
func TestCapturePermissionBeforeSampling(t *testing.T) {
	d := &Desktop{}
	if _, err := d.Screenshot(context.Background(), ScreenshotReq{}); err == nil {
		t.Fatal("unauthorized capture accepted")
	}
	ctx := bridgeContext("https://test", "window", "screenshot")
	if _, err := d.Screenshot(ctx, ScreenshotReq{Output: "clipboard"}); err == nil {
		t.Fatal("clipboard write accepted without permission")
	}
}
