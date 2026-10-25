package host

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"hybrid-infra/go/setting"
)

// TestSettingsCenterPinsConfiguration 验证读写路径和身份在启动后固定，返回真实来源与待重启字段，不热更新权限。
func TestSettingsCenterPinsConfiguration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app-a.json")
	t.Setenv("HYBRID_SETTINGS", path)
	t.Setenv("HYBRID_APP_ID", "")
	if err := os.WriteFile(path, []byte(`{"appId":"settings-a","title":"旧标题"}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := setting.New().Parse()
	if err != nil {
		t.Fatal(err)
	}
	desktop := NewDesktop(nil, New(config, dir), dir)
	initial, err := desktop.GetSettings()
	if err != nil || initial.RestartRequired || initial.AppID != "settings-a" || initial.ProtocolScheme != "hybrid-settings-a" || initial.ConfigPath != path || initial.ConfigSource != "environment" {
		t.Fatalf("initial snapshot %+v %v", initial, err)
	}
	other := filepath.Join(dir, "app-b.json")
	if err := os.WriteFile(other, []byte(`{"appId":"settings-b"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYBRID_SETTINGS", other)
	pending := *config
	pending.Title = "新标题"
	pending.ShellPrograms = []string{}
	pending.AllowedOrigins = []string{}
	pending.NavigationOrigins = []string{}
	pending.Permissions = map[string][]string{}
	onlyTitle, err := desktop.SaveSettings(pending)
	if err != nil || !slices.Equal(onlyTitle.PendingFields, []string{"title"}) {
		t.Fatalf("empty lists caused false restart fields: %v %v", onlyTitle.PendingFields, err)
	}
	pending.DesktopCapabilities = []string{"files"}
	snapshot, err := desktop.SaveSettings(pending)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ConfigPath != path || !snapshot.RestartRequired || !slices.Contains(snapshot.PendingFields, "title") || !slices.Contains(snapshot.PendingFields, "desktopCapabilities") || config.Title != "旧标题" || len(config.DesktopCapabilities) != 0 {
		t.Fatal("settings were redirected or hot applied")
	}
	unrelated, err := setting.ReadConfig(other, "settings-b")
	if err != nil || unrelated.Title != "Hybrid App" {
		t.Fatal("another application's config was changed")
	}
	pending.AppID = "settings-b"
	if _, err := desktop.SaveSettings(pending); err == nil {
		t.Fatal("runtime identity change accepted")
	}
}

// TestApplicationProtocolRoutes 验证应用只接收自己的协议，不让另一业务应用的唤醒链接改变本应用页面。
func TestApplicationProtocolRoutes(t *testing.T) {
	config := testConfig()
	config.AppID = "erp-a"
	controller := New(config, t.TempDir())
	window := &fakeWindow{name: "main-window"}
	controller.windows[window.name] = &entry{window: window, status: "ready", target: config.Url}
	defer func() {
		if timer := controller.windows[window.name].timer; timer != nil {
			timer.Stop()
		}
	}()
	controller.HandleActivation([]string{"hybrid-erp-b://open?route=%2Fother"})
	if window.url.Load() != nil {
		t.Fatal("another app's protocol accepted")
	}
	controller.HandleActivation([]string{"hybrid-erp-a://open?route=%23workspace"})
	if window.url.Load() != "https://app.test/portal#workspace" {
		t.Fatalf("own app protocol failed: %v", window.url.Load())
	}
}
