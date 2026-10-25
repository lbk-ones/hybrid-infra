package host

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// init 仅在测试专用环境下充当小型应用，验证真实进程握手和锁释放；生产二进制不会包含此入口。
func init() {
	if os.Getenv("MAINTENANCE_TEST_FIXTURE") != "1" {
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--hybrid-update-worker" {
		_, err := RunUpdateWorker(os.Args[1:])
		if err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	if len(os.Args) > 2 && os.Args[1] == "--fixture-parent" {
		path := os.Args[2]
		var ticket updateTicket
		readyDeadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(readyDeadline) {
			if _, err := os.Stat(path); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		b, _ := os.ReadFile(path)
		_ = json.Unmarshal(b, &ticket)
		release, err := acquireDataLock(ticket.DataDir)
		if err != nil {
			os.Exit(3)
		}
		defer release()
		command := exec.Command(filepath.Join(filepath.Dir(path), "worker.exe"), "--hybrid-update-worker", path)
		hideWorker(command)
		if command.Start() != nil {
			os.Exit(4)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err = os.Stat(filepath.Join(filepath.Dir(path), "worker-ready.json")); err == nil {
				os.Exit(0)
			}
			time.Sleep(30 * time.Millisecond)
		}
		os.Exit(5)
	}
	dir := os.Getenv("HYBRID_DATA_DIR")
	if dir == "" {
		os.Exit(6)
	}
	release, err := acquireDataLock(dir)
	if err != nil {
		os.Exit(7)
	}
	defer release()
	if os.Getenv("HYBRID_UPDATE_HEALTH") != "" {
		Version = "0.2.0"
		_ = os.WriteFile(filepath.Join(dir, "store.json"), []byte(`{"value":"new"}`), 0600)
		if os.Getenv("MAINTENANCE_TEST_CRASH") == "1" {
			os.Exit(8)
		}
		NewMaintenance(nil, dir).MarkUpdateHealthy()
		time.Sleep(4 * time.Second)
	} else {
		_ = os.WriteFile(filepath.Join(dir, "old-started"), []byte("ok"), 0600)
	}
	os.Exit(0)
}

// TestRealUpdateWorker 在临时目录运行测试 EXE，验证父进程退出握手、Windows 文件替换、健康确认及崩溃回滚。
func TestRealUpdateWorker(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows update process integration")
	}
	for _, crash := range []bool{false, true} {
		name := "healthy"
		if crash {
			name = "crash"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := InitializeData(dir); err != nil {
				t.Fatal(err)
			}
			_ = os.WriteFile(filepath.Join(dir, "store.json"), []byte(`{"value":"old"}`), 0600)
			current, _ := os.Executable()
			target := filepath.Join(dir, "app.exe")
			candidate := filepath.Join(dir, "updates", "candidate.exe")
			id := maintenanceID()
			work := filepath.Join(dir, "updates", id)
			_ = os.MkdirAll(work, 0700)
			for _, path := range []string{target, candidate, filepath.Join(work, "worker.exe")} {
				if err := copyUpdateFile(current, path); err != nil {
					t.Fatal(err)
				}
			}
			b, _ := os.ReadFile(candidate)
			hash := sha256.Sum256(b)
			manifest := UpdateManifest{Schema: 1, Version: "0.2.0", Channel: "stable", Platform: runtime.GOOS, Arch: runtime.GOARCH, URL: "https://example.invalid/fixture.exe", Size: int64(len(b)), SHA256: hex.EncodeToString(hash[:]), MaxDataSchema: 1}
			signed, config := signedFixture(t, manifest)
			// 父进程 PID 只能由启动后的实际进程写入，使用票据预备模式避免猜测进程身份。
			ticket := updateTicket{ID: id, Target: target, DataDir: dir, OldVersion: "0.1.0", NewVersion: "0.2.0", Signed: signed, Config: config, Hash: manifest.SHA256}
			path := filepath.Join(work, "ticket.json")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			parent := exec.CommandContext(ctx, target, "--fixture-parent", path)
			parent.Env = append(os.Environ(), "MAINTENANCE_TEST_FIXTURE=1")
			if crash {
				parent.Env = append(parent.Env, "MAINTENANCE_TEST_CRASH=1")
			}
			hideWorker(parent)
			// 先让父进程等待完整票据，写入真实 PID 后它再创建维护工作进程。
			parent.Args = append(parent.Args, "--wait-ticket")
			if err := parent.Start(); err != nil {
				t.Fatal(err)
			}
			ticket.Parent = parent.Process.Pid
			if err := jsonWrite(path, ticket); err != nil {
				t.Fatal(err)
			}
			if err := parent.Wait(); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(12 * time.Second)
			var result UpdateState
			for time.Now().Before(deadline) {
				content, err := os.ReadFile(filepath.Join(dir, "updates", "result.json"))
				if err == nil && json.Unmarshal(content, &result) == nil {
					break
				}
				time.Sleep(60 * time.Millisecond)
			}
			expected := "installed"
			if crash {
				expected = "rolled_back"
			}
			if result.Stage != expected {
				t.Fatalf("worker result: %+v", result)
			}
			data, _ := os.ReadFile(filepath.Join(dir, "store.json"))
			if crash && string(data) != `{"value":"old"}` {
				t.Fatalf("data rollback: %s", data)
			}
			// 成功测试子进程会自行退出，等它释放 EXE 后再由 TempDir 清理，避免留下后台测试程序。
			time.Sleep(2200 * time.Millisecond)
		})
	}
}
