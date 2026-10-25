package host

import (
	"bytes"
	"context"
	"hybrid-infra/go/setting"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// iconFixture 生成小型透明 PNG，只用于图标加载测试，不依赖用户文件或网络资源。
func iconFixture(t *testing.T, size int, fill color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.SetNRGBA(x, y, fill)
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// TestIconValidation 拒绝非 PNG、截断内容、过大尺寸与过大的编码文件，避免系统 API 解码不受控数据。
func TestIconValidation(t *testing.T) {
	valid := iconFixture(t, 32, color.NRGBA{R: 180, A: 255})
	if err := validateIcon(valid); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{nil, []byte("<svg/>"), valid[:30], iconFixture(t, 8, color.NRGBA{}), iconFixture(t, 1025, color.NRGBA{}), make([]byte, maxIconBytes+1)} {
		if err := validateIcon(data); err == nil {
			t.Fatal("invalid icon accepted")
		}
	}
}

// TestConfiguredIcons 验证相对路径、托盘继承、独立覆盖以及坏文件不会交付半套新图标。
func TestConfiguredIcons(t *testing.T) {
	dir := t.TempDir()
	app := iconFixture(t, 32, color.NRGBA{G: 160, A: 255})
	tray := iconFixture(t, 32, color.NRGBA{B: 200, A: 255})
	_ = os.WriteFile(filepath.Join(dir, "app.png"), app, 0600)
	_ = os.WriteFile(filepath.Join(dir, "tray.png"), tray, 0600)
	defaults := iconPair{tray, tray}
	pair, err := loadIconPair(context.Background(), setting.IconConfig{Application: "app.png"}, dir, defaults)
	if err != nil || !bytes.Equal(pair.application, app) || !bytes.Equal(pair.tray, app) {
		t.Fatalf("inheritance failed: %v", err)
	}
	pair, err = loadIconPair(context.Background(), setting.IconConfig{Application: "app.png", Tray: "tray.png"}, dir, defaults)
	if err != nil || !bytes.Equal(pair.tray, tray) {
		t.Fatal(err)
	}
	if _, err = loadIconPair(context.Background(), setting.IconConfig{Application: "app.png", Tray: "missing.png"}, dir, defaults); err == nil {
		t.Fatal("partial pair accepted")
	}
	pair, err = loadIconPair(context.Background(), setting.IconConfig{}, dir, defaults)
	if err != nil || !bytes.Equal(pair.application, defaults.application) {
		t.Fatal("default reset failed")
	}
}

// TestRemoteIconLoading 通过本机服务器验证下载、错误响应、跨源重定向和取消边界，不访问公网。
func TestRemoteIconLoading(t *testing.T) {
	data := iconFixture(t, 32, color.NRGBA{R: 100, G: 180, A: 255})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/icon":
			_, _ = w.Write(data)
		case "/oversized":
			w.Header().Set("Content-Length", "3000000")
		case "/cross":
			http.Redirect(w, r, "http://localhost:1/untrusted", http.StatusFound)
		default:
			http.Error(w, "missing", 404)
		}
	}))
	defer server.Close()
	if _, err := loadIconPair(context.Background(), setting.IconConfig{Application: server.URL + "/icon"}, "", iconPair{}); err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"/oversized", "/cross", "/missing"} {
		if _, err := loadIcon(context.Background(), server.URL+route, ""); err == nil {
			t.Fatalf("accepted %s", route)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := loadIcon(ctx, server.URL+"/icon", ""); err == nil {
		t.Fatal("cancellation ignored")
	}
	for _, address := range []string{"file:///tmp/logo.png", "https://user:pass@example.com/logo.png", "http://example.com/logo.png", "data:image/png;base64,"} {
		if err := (setting.IconConfig{Application: address}).Validate(); err == nil {
			t.Fatalf("unsafe icon address: %s", strings.Split(address, ":")[0])
		}
	}
}
