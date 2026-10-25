package host

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"hybrid-infra/go/setting"
)

// TestMigrationAndRestore 验证旧数据迁移、恢复延迟生效及权限文件不进入备份。
func TestMigrationAndRestore(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "store.json"), []byte(`{"value":"before"}`), 0600)
	_ = os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"secret":"exclude"}`), 0600)
	if e := InitializeData(dir); e != nil {
		t.Fatal(e)
	}
	m := NewMaintenance(nil, dir)
	list := m.ListBackups()
	if len(list) != 1 || list[0].Schema != 0 {
		t.Fatalf("migration backup: %+v", list)
	}
	backup, e := m.CreateBackup()
	if e != nil {
		t.Fatal(e)
	}
	_, data, e := m.readBackup(backup.ID)
	if e != nil {
		t.Fatal(e)
	}
	if data["settings.json"] != nil {
		t.Fatal("settings leaked")
	}
	_ = os.WriteFile(filepath.Join(dir, "store.json"), []byte(`{"value":"after"}`), 0600)
	if e = m.StageRestore(backup.ID); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "store.json"))
	if !strings.Contains(string(b), "after") {
		t.Fatal("restore happened before restart")
	}
	if e = InitializeData(dir); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "store.json"))
	if !strings.Contains(string(b), "before") {
		t.Fatal("restore failed")
	}
	if len(m.ListBackups()) != 3 {
		t.Fatal("pre-restore snapshot missing")
	}
	if e = m.StageRestore("../../settings"); e == nil {
		t.Fatal("path accepted")
	}
	_ = os.WriteFile(filepath.Join(dir, "data-version.json"), []byte(`{"schema":99}`), 0600)
	if e = InitializeData(dir); e == nil {
		t.Fatal("future schema accepted")
	}
}

// TestBackupRejectsCorruption 验证 Zip 路径穿越、重复条目及数据篡改不会进入恢复阶段。
func TestBackupRejectsCorruption(t *testing.T) {
	for _, name := range []string{"../store.json", "store.json", "duplicate"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			m := NewMaintenance(nil, dir)
			_ = os.WriteFile(filepath.Join(dir, "store.json"), []byte(`{"x":1}`), 0600)
			backup, e := m.CreateBackup()
			if e != nil {
				t.Fatal(e)
			}
			var b bytes.Buffer
			w := zip.NewWriter(&b)
			r, e := zip.OpenReader(filepath.Join(dir, "backups", backup.ID+".zip"))
			if e != nil {
				t.Fatal(e)
			}
			for _, f := range r.File {
				source, _ := f.Open()
				var content bytes.Buffer
				_, _ = content.ReadFrom(source)
				_ = source.Close()
				entry, _ := w.Create(f.Name)
				_, _ = entry.Write(content.Bytes())
			}
			_ = r.Close()
			target := name
			if name == "duplicate" {
				target = "manifest.json"
			}
			entry, _ := w.Create(target)
			_, _ = entry.Write([]byte(`{"x":2}`))
			_ = w.Close()
			_ = os.WriteFile(filepath.Join(dir, "backups", backup.ID+".zip"), b.Bytes(), 0600)
			if e = m.StageRestore(backup.ID); e == nil {
				t.Fatal("corrupt archive accepted")
			}
			original, _ := os.ReadFile(filepath.Join(dir, "store.json"))
			if string(original) != `{"x":1}` {
				t.Fatal("data touched")
			}
		})
	}
}

// signedFixture 为本地 HTTP 测试生成临时 Ed25519 密钥和签名，不使用真实发布密钥。
func signedFixture(t *testing.T, manifest UpdateManifest) (SignedUpdate, setting.UpdateConfig) {
	t.Helper()
	public, private, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	payload, _ := json.Marshal(manifest)
	return SignedUpdate{base64.StdEncoding.EncodeToString(payload), base64.StdEncoding.EncodeToString(ed25519.Sign(private, payload))}, setting.UpdateConfig{Enabled: true, PublicKey: base64.StdEncoding.EncodeToString(public), Channel: "stable"}
}

// TestSignedUpdateVerification 验证签名、平台、数据版本、来源和版本比较的拒绝边界。
func TestSignedUpdateVerification(t *testing.T) {
	manifest := UpdateManifest{Schema: 1, Version: "0.2.0", Channel: "stable", Platform: runtime.GOOS, Arch: runtime.GOARCH, URL: "https://example.com/app.exe", SHA256: strings.Repeat("a", 64), Size: 100, MaxDataSchema: 1}
	signed, config := signedFixture(t, manifest)
	if _, e := verifyUpdate(signed, config); e != nil {
		t.Fatal(e)
	}
	bad := signed
	bad.Payload = base64.StdEncoding.EncodeToString([]byte(`{}`))
	if _, e := verifyUpdate(bad, config); e == nil {
		t.Fatal("tampered signature accepted")
	}
	for _, change := range []func(*UpdateManifest){func(m *UpdateManifest) { m.Arch = "wrong" }, func(m *UpdateManifest) { m.MaxDataSchema = 0 }, func(m *UpdateManifest) { m.URL = "http://example.com/update" }, func(m *UpdateManifest) { m.Size = 300 << 20 }} {
		copy := manifest
		change(&copy)
		s, c := signedFixture(t, copy)
		if _, e := verifyUpdate(s, c); e == nil {
			t.Fatal("incompatible update accepted")
		}
	}
	if !newerRelease("1.10.0", "1.9.9") || newerRelease("1.0.0", "1.0.0") || newerRelease("1.0.1-beta", "1.0.0") {
		t.Fatal("version comparison")
	}
}

// TestUpdateDownload 验证真实本机 HTTP 下载、哈希失败与取消，不访问用户桌面或安装目录。
func TestUpdateDownload(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows portable executable")
	}
	exe, _ := os.Executable()
	artifact, e := os.ReadFile(exe)
	if e != nil {
		t.Fatal(e)
	}
	hash := sha256.Sum256(artifact)
	manifest := UpdateManifest{Schema: 1, Version: "0.2.0", Channel: "stable", Platform: runtime.GOOS, Arch: runtime.GOARCH, Size: int64(len(artifact)), SHA256: hex.EncodeToString(hash[:]), MaxDataSchema: 1}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(artifact) }))
	defer server.Close()
	manifest.URL = server.URL
	m := NewMaintenance(nil, t.TempDir())
	if e = m.download(context.Background(), manifest); e != nil {
		t.Fatal(e)
	}
	manifest.SHA256 = strings.Repeat("a", 64)
	if e = m.download(context.Background(), manifest); e == nil {
		t.Fatal("bad hash accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e = m.download(ctx, manifest); e == nil {
		t.Fatal("cancel ignored")
	}
}

// TestUpdateTransaction 使用假启动器验证安装成功、崩溃和启动错误后的程序与数据回退，不运行真实更新。
func TestUpdateTransaction(t *testing.T) {
	for _, mode := range []string{"healthy", "crash", "launch-error"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			m := NewMaintenance(nil, dir)
			if e := InitializeData(dir); e != nil {
				t.Fatal(e)
			}
			target := filepath.Join(dir, "app.exe")
			source := filepath.Join(dir, "candidate.exe")
			_ = os.WriteFile(target, []byte("old-binary"), 0700)
			_ = os.WriteFile(source, []byte("new-binary"), 0700)
			_ = os.WriteFile(filepath.Join(dir, "store.json"), []byte(`{"value":"old"}`), 0600)
			work := filepath.Join(dir, "updates", maintenanceID())
			_ = os.MkdirAll(work, 0700)
			ticket := updateTicket{Target: target, DataDir: dir, OldVersion: "0.1.0", NewVersion: "0.2.0"}
			state := UpdateState{Stage: "failed"}
			launches := 0
			// 假启动器模拟新版本写入业务数据后崩溃；回退启动不产生额外写入。
			launch := func(ticket updateTicket, health, token string) (*exec.Cmd, error) {
				launches++
				if health != "" {
					_ = os.WriteFile(filepath.Join(dir, "store.json"), []byte(`{"value":"new"}`), 0600)
					if mode == "launch-error" {
						return nil, errors.New("fixture startup failure")
					}
				}
				return nil, nil
			}
			probe := func(*exec.Cmd, string, string, string, time.Duration) bool { return mode == "healthy" }
			e := executeUpdate(ticket, m, source, filepath.Join(work, "ticket.json"), &state, launch, probe)
			b, _ := os.ReadFile(target)
			data, _ := os.ReadFile(filepath.Join(dir, "store.json"))
			if mode == "healthy" {
				if e != nil || state.Stage != "installed" || string(b) != "new-binary" {
					t.Fatalf("install: %s %v", state.Stage, e)
				}
				if _, e = os.Stat(filepath.Join(dir, "updates", "rollback.json")); e != nil {
					t.Fatal(e)
				}
			} else {
				if string(b) != "old-binary" || !strings.Contains(string(data), "old") || launches != 2 {
					t.Fatalf("rollback failed: %s %s %d", b, data, launches)
				}
				if mode == "crash" && state.Stage != "rolled_back" {
					t.Fatal(state)
				}
			}
		})
	}
}

// TestDiagnosticsPrivacy 确认错误文本和业务秘密不会进入诊断快照或落盘记录。
func TestDiagnosticsPrivacy(t *testing.T) {
	m := NewMaintenance(nil, t.TempDir())
	m.RecordError(errors.New("secret-token https://private.example"))
	m.RecordError(Fail("E_TEST", "password=private"))
	b, _ := json.Marshal(m.Diagnostics())
	disk, _ := os.ReadFile(filepath.Join(m.dir, "diagnostics.json"))
	for _, content := range []string{string(b), string(disk)} {
		if strings.Contains(content, "secret") || strings.Contains(content, "password") || strings.Contains(content, "private") {
			t.Fatal("diagnostic leaked content")
		}
	}
}

// TestDataLeaseProtectsPendingRestore 验证运行中的数据目录不能被第二实例提前恢复，释放锁后才允许正常恢复。
func TestDataLeaseProtectsPendingRestore(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "store.json"), []byte(`{"x":1}`), 0600)
	m := NewMaintenance(nil, dir)
	release, err := m.PrepareData()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	snapshot, err := m.CreateBackup()
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "store.json"), []byte(`{"x":2}`), 0600)
	if err = m.StageRestore(snapshot.ID); err != nil {
		t.Fatal(err)
	}
	second := NewMaintenance(nil, dir)
	if unlock, e := second.PrepareData(); e == nil {
		unlock()
		t.Fatal("second instance touched live data")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "store.json"))
	if string(b) != `{"x":2}` {
		t.Fatal("live data changed")
	}
	release()
	unlock, err := second.PrepareData()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	b, _ = os.ReadFile(filepath.Join(dir, "store.json"))
	if string(b) != `{"x":1}` {
		t.Fatal("pending restore not applied")
	}
}
