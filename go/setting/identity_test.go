package setting

import (
	"os"
	"path/filepath"
	"testing"
)

// TestApplicationIdentityIsolation 验证自定义身份使用不同数据目录和协议，默认身份保留原目录，且拒绝危险名称。
func TestApplicationIdentityIsolation(t *testing.T) {
	t.Setenv("HYBRID_DATA_DIR", "")
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := DataDir(DefaultAppID)
	if err != nil || legacy != filepath.Join(base, DefaultAppID) {
		t.Fatalf("legacy directory: %s %v", legacy, err)
	}
	a, err := DataDir("erp-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := DataDir("erp-b")
	if err != nil || a == b || a == legacy || a != filepath.Join(base, DefaultAppID, "apps", "erp-a") {
		t.Fatal("application directories not isolated")
	}
	if (&PkgInfo{AppID: "erp-a"}).ProtocolScheme() != "hybrid-erp-a" || (&PkgInfo{}).ProtocolScheme() != DefaultAppID {
		t.Fatal("protocol compatibility lost")
	}
	for _, invalid := range []string{"../escape", "ERP", "a/b", "1-app", "a b", ""} {
		if _, err := DataDir(invalid); err == nil {
			t.Fatalf("invalid identity accepted: %s", invalid)
		}
	}
}

// TestIdentityBootstrapAndUserConfig 验证环境身份选择专属用户配置，覆盖默认配置时不能偷偷更换所属应用。
func TestIdentityBootstrapAndUserConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APPDATA", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("HOME", root)
	t.Setenv("HYBRID_SETTINGS", "")
	t.Setenv("HYBRID_DATA_DIR", "")
	t.Setenv("HYBRID_APP_ID", "erp-a")
	t.Chdir(t.TempDir())
	dir, err := DataDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"appId":"erp-a","title":"独立配置"}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := New().Parse()
	if err != nil || config.Identity() != "erp-a" || config.Title != "独立配置" {
		t.Fatalf("identity load: %+v %v", config, err)
	}
	loaded, source := config.ConfigLocation()
	if loaded != path || source != "user" {
		t.Fatalf("wrong source %s %s", loaded, source)
	}
	if err := os.WriteFile(path, []byte(`{"appId":"erp-b"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New().Parse(); err == nil {
		t.Fatal("user configuration changed bootstrap identity")
	}
	t.Setenv("HYBRID_APP_ID", "erp-b")
	other, err := DataDir()
	if err != nil || other == dir {
		t.Fatal("different identity selected same user directory")
	}
}

// TestExplicitIdentityConfig 验证显式配置可引导身份，未设置身份的旧文件仍兼容；环境身份不允许与文件冲突。
func TestExplicitIdentityConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.json")
	t.Setenv("HYBRID_SETTINGS", path)
	t.Setenv("HYBRID_APP_ID", "")
	if err := os.WriteFile(path, []byte(`{"appId":"local-tool"}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := New().Parse()
	if err != nil || config.Identity() != "local-tool" {
		t.Fatalf("explicit identity %v", err)
	}
	_, source := config.ConfigLocation()
	if source != "environment" {
		t.Fatal("explicit source lost")
	}
	t.Setenv("HYBRID_APP_ID", "another-tool")
	if _, err := New().Parse(); err == nil {
		t.Fatal("identity mismatch accepted")
	}
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, err = New().Parse()
	if err != nil || config.Identity() != "another-tool" {
		t.Fatalf("environment identity not inherited: %v", err)
	}
	custom := t.TempDir()
	t.Setenv("HYBRID_DATA_DIR", custom)
	dir, err := DataDir("another-tool")
	if err != nil || dir != custom {
		t.Fatal("explicit data directory override changed")
	}
}

// TestProtocolIdentityArgument 验证协议启动参数只能选择合法身份，拒绝路径、空值和冲突的重复参数。
func TestProtocolIdentityArgument(t *testing.T) {
	id, err := argumentIdentity([]string{"--hybrid-app-id=erp-a", "hybrid-erp-a://open?route=%23workspace"})
	if err != nil || id != "erp-a" {
		t.Fatalf("protocol identity %s %v", id, err)
	}
	for _, args := range [][]string{{"--hybrid-app-id="}, {"--hybrid-app-id=../escape"}, {"--hybrid-app-id=a", "--hybrid-app-id=b"}} {
		if _, err := argumentIdentity(args); err == nil {
			t.Fatalf("invalid argument accepted: %v", args)
		}
	}
}
