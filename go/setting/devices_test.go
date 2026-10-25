package setting

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDeviceEndpointValidation 验证网络白名单只接受字面 IP 和有效端口，不接受域名、任意 URL 或通配符。
func TestDeviceEndpointValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv("HYBRID_SETTINGS", path)
	for _, sample := range []struct {
		json  string
		valid bool
	}{{`{"deviceEndpoints":["127.0.0.1:9100","192.168.1.50:9100","[::1]:9100"]}`, true}, {`{"deviceEndpoints":["example.com:9100"]}`, false}, {`{"deviceEndpoints":["0.0.0.0:9100"]}`, false}, {`{"deviceEndpoints":["192.168.1.50:0"]}`, false}, {`{"deviceEndpoints":["http://127.0.0.1:80"]}`, false}} {
		if err := os.WriteFile(path, []byte(sample.json), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := New().Parse()
		if (err == nil) != sample.valid {
			t.Fatalf("config=%s error=%v", sample.json, err)
		}
	}
}
