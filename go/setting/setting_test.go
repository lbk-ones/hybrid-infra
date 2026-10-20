package setting

import (
	"os"
	"path/filepath"
	"testing"
)

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
