package setting

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPageModes 验证三种模式严格区分，保留旧配置推断，并正确映射本地路径、查询参数及 hash。
func TestPageModes(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv("HYBRID_SETTINGS", configPath)
	for _, sample := range []struct{ config, mode, target string }{
		{`{}`, "builtin", "/"},
		{`{"url":"https://app.example.com/"}`, "http", "https://app.example.com/"},
		{`{"pageMode":"builtin","url":"/shell/docs.html"}`, "builtin", "/shell/docs.html"},
		{`{"pageMode":"http","url":"http://127.0.0.1:8080/index.html"}`, "http", "http://127.0.0.1:8080/index.html"},
		{`{"pageMode":"local","localRoot":".","url":"/index.html?theme=dark#workspace"}`, "local", "/local/index.html?theme=dark#workspace"},
		{`{"pageMode":"local","url":"/"}`, "local", "/local/"},
	} {
		if err := os.WriteFile(configPath, []byte(sample.config), 0600); err != nil {
			t.Fatal(err)
		}
		config, err := New().Parse()
		if err != nil {
			t.Fatalf("%s: %v", sample.config, err)
		}
		if config.EffectivePageMode() != sample.mode || config.PageURL() != sample.target {
			t.Fatalf("%s: mode=%s target=%s", sample.config, config.EffectivePageMode(), config.PageURL())
		}
	}
	for _, raw := range []string{
		`{"pageMode":"unknown"}`, `{"pageMode":"http","url":"/index.html"}`,
		`{"pageMode":"builtin","url":"https://app.example.com/"}`,
		`{"pageMode":"local","url":"https://app.example.com/"}`,
		`{"pageMode":"local","url":"/../index.html"}`, `{"pageMode":"local","url":"/%2e%2e/index.html"}`,
		`{"pageMode":"local","url":"/folder//index.html"}`, `{"pageMode":"local","url":"/settings.json"}`,
		`{"pageMode":"local","localRoot":"https://example.com","url":"/index.html"}`,
		`{"pageMode":"http","url":"https://example.com/","localRoot":"web"}`,
	} {
		if err := os.WriteFile(configPath, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := New().Parse(); err == nil {
			t.Fatalf("invalid mode configuration accepted: %s", raw)
		}
	}
}
