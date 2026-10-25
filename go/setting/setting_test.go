package setting

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWindowStartupModeValidation 验证旧配置默认自动启动，四种显示模式可保存，拼写错误会明确拒绝。
func TestWindowStartupModeValidation(t *testing.T) {
	for _, mode := range []string{"", "auto", "normal", "maximized", "fullscreen", "wrong"} {
		config, err := ReadConfig("", DefaultAppID)
		if err != nil {
			t.Fatal(err)
		}
		config.WindowState = mode
		err = config.Validate()
		if mode == "wrong" {
			if err == nil {
				t.Fatal("accepted invalid window mode")
			}
		} else if err != nil || (mode == "" && config.WindowState != "auto") {
			t.Fatalf("mode=%q config=%q err=%v", mode, config.WindowState, err)
		}
	}
}

// TestSplashDefaultsAndDisable 验证缺省配置开启启动屏，同时显式 false 不会被默认值覆盖。
func TestSplashDefaultsAndDisable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv("HYBRID_SETTINGS", path)
	for _, sample := range []struct {
		json    string
		enabled bool
	}{{`{}`, true}, {`{"splashEnabled":false}`, false}, {`{"splashEnabled":true}`, true}} {
		if err := os.WriteFile(path, []byte(sample.json), 0600); err != nil {
			t.Fatal(err)
		}
		config, err := New().Parse()
		if err != nil || config.SplashEnabled != sample.enabled {
			t.Fatalf("splash enabled=%v expected=%v err=%v", config.SplashEnabled, sample.enabled, err)
		}
	}
}

// TestExplicitConfigAndDataDirectory 验证环境变量指定的配置文件和用户数据目录生效，并自动纳入远程主页面来源。
func TestExplicitConfigAndDataDirectory(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "custom.json")
	t.Setenv("HYBRID_SETTINGS", configPath)
	t.Setenv("HYBRID_DATA_DIR", filepath.Join(dir, "data"))
	if err := os.WriteFile(configPath, []byte(`{"title":"远程应用","url":"https://app.example.com/portal","allowedOrigins":["http://localhost:5173"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := New().Parse()
	if err != nil {
		t.Fatal(err)
	}
	if config.Title != "远程应用" {
		t.Fatal("configuration not loaded")
	}
	origins := config.TrustedOrigins()
	if len(origins) != 2 || origins[1] != "https://app.example.com" {
		t.Fatalf("origins: %v", origins)
	}
	data, err := DataDir()
	if err != nil || data != filepath.Join(dir, "data") {
		t.Fatalf("data dir: %s, %v", data, err)
	}
}

// TestInvalidConfigFailsExplicitly 验证错误的 JSON、危险地址、无效尺寸和来源配置会明确报错，显式配置文件缺失也不会被静默忽略。
func TestInvalidConfigFailsExplicitly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv("HYBRID_SETTINGS", path)
	for _, data := range []string{`{`, `{"url":"javascript:alert(1)"}`, `{"url":"//example.com"}`, `{"width":-1}`, `{"allowedOrigins":["*"]}`, `{"allowedOrigins":["https://app.example.com/path"]}`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := New().Parse(); err == nil {
			t.Errorf("accepted invalid config: %s", data)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := New().Parse(); err == nil {
		t.Fatal("missing explicit config must fail")
	}
}

// TestUserConfigWorksOutsideCheckout 验证从非项目目录启动程序时，仍可读取稳定用户数据目录中的配置文件。
func TestUserConfigWorksOutsideCheckout(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HYBRID_SETTINGS", "")
	t.Setenv("HYBRID_DATA_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"title":"user config"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	config, err := New().Parse()
	if err != nil || config.Title != "user config" {
		t.Fatalf("config=%v err=%v", config, err)
	}
}

// TestPhaseOneDefaultsAndValidation 验证第一阶段配置的默认值，以及关闭方式、导航来源、超时和能力名称等非法配置的拒绝行为。
func TestPhaseOneDefaultsAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv("HYBRID_SETTINGS", path)
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := New().Parse()
	if err != nil {
		t.Fatal(err)
	}
	if !config.SingleInstance || !config.RememberWindows || config.CloseBehavior != "exit" || config.ExternalLinks != "browser" || config.LoadTimeoutSeconds != 30 {
		t.Fatalf("unexpected phase-one defaults: %+v", config)
	}
	for _, input := range []string{`{"closeBehavior":"minimise"}`, `{"externalLinks":"allow"}`, `{"minWidth":100}`, `{"loadTimeoutSeconds":0}`, `{"navigationOrigins":["*"]}`, `{"permissions":{"https://app.test":["unknown-capability"]}}`} {
		if err := os.WriteFile(path, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := New().Parse(); err == nil {
			t.Errorf("accepted invalid settings: %s", input)
		}
	}
}
