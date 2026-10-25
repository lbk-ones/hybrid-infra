package host

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestRestartCommandPreservesIdentity 验证重启保留身份、数据、配置和工作目录，不重放健康令牌或协议参数。
func TestRestartCommandPreservesIdentity(t *testing.T) {
	t.Setenv("HYBRID_RESTART_TOKEN", "old")
	t.Setenv("HYBRID_UPDATE_TOKEN", "old")
	t.Setenv("HYBRID_SETTINGS", "wrong")
	dir := t.TempDir()
	command := restartCommand(restartTicket{Target: filepath.Join(dir, "app.exe"), DataDir: dir, WorkDir: dir, Settings: filepath.Join(dir, "settings.json"), AppID: "restart-test"})
	env := strings.Join(command.Env, "\n")
	if len(command.Args) != 1 || command.Dir != dir || !strings.Contains(env, "HYBRID_APP_ID=restart-test") || strings.Contains(env, "HYBRID_RESTART_TOKEN=") || strings.Contains(env, "HYBRID_UPDATE_TOKEN=") || strings.Contains(env, "HYBRID_SETTINGS=wrong") {
		t.Fatal("restart environment", env)
	}
	if handled, _ := RunRestartWorker(nil); handled {
		t.Fatal("normal start intercepted")
	}
	if handled, err := RunRestartWorker([]string{"--hybrid-restart-worker"}); !handled || err == nil {
		t.Fatal("invalid helper accepted")
	}
}

// TestRestartWorkerRejectsForgedTicket 验证没有宿主临时令牌或目标程序不一致时不会执行任何程序。
func TestRestartWorkerRejectsForgedTicket(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "restarts"), 0700)
	own, _ := os.Executable()
	ticket := restartTicket{ID: "test", Parent: os.Getpid(), Target: own, DataDir: dir, WorkDir: dir, Token: "required"}
	path := filepath.Join(dir, "restarts", "test.json")
	if err := jsonWrite(path, ticket); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HYBRID_RESTART_TOKEN", "wrong")
	if handled, err := RunRestartWorker([]string{"--hybrid-restart-worker", path}); !handled || err == nil {
		t.Fatal("forged helper accepted")
	}
}

// TestRealRestartWorker 用独立测试程序验证真实父进程退出后才重启，整个流程不操作用户正在运行的应用。
func TestRealRestartWorker(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess integration")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	target := filepath.Join(dir, "restart-fixture")
	if runtime.GOOS == "windows" {
		target += ".exe"
	}
	code := `package main
import("encoding/json";"os";"os/exec";"path/filepath";"time";"hybrid-infra/go/host")
// main 只在独立测试子进程中运行重启协议并写验证标记，不操作用户应用。
func main(){
 if handled,err:=host.RunRestartWorker(os.Args[1:]);handled{if err!=nil{os.Exit(8)};return}
 dir:=os.Getenv("RESTART_FIXTURE_DIR");if os.Getenv("RESTART_FIXTURE_PHASE")=="child"{cwd,_:=os.Getwd();data,_:=json.Marshal(map[string]string{"token":os.Getenv("HYBRID_RESTART_TOKEN"),"data":os.Getenv("HYBRID_DATA_DIR"),"app":os.Getenv("HYBRID_APP_ID"),"cwd":cwd});_ = os.WriteFile(filepath.Join(dir,"child.json"),data,0600);return}
 own,_:=os.Executable();_ = os.MkdirAll(filepath.Join(dir,"restarts"),0700);path:=filepath.Join(dir,"restarts","fixture.json");data,_:=json.Marshal(map[string]any{"ID":"fixture","Parent":os.Getpid(),"Target":own,"DataDir":dir,"WorkDir":dir,"AppID":"restart-fixture","Token":"fixture-token"});_ = os.WriteFile(path,data,0600)
 command:=exec.Command(own,"--hybrid-restart-worker",path);command.Env=append(os.Environ(),"HYBRID_RESTART_TOKEN=fixture-token","RESTART_FIXTURE_PHASE=child");if command.Start()!=nil{os.Exit(2)}
 for end:=time.Now().Add(5*time.Second);time.Now().Before(end);{if _,err:=os.Stat(path+".ready");err==nil{return};time.Sleep(10*time.Millisecond)};os.Exit(3)
}`
	if err := os.WriteFile(source, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", target, source)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	command := exec.CommandContext(ctx, target)
	command.Env = append(os.Environ(), "RESTART_FIXTURE_DIR="+dir)
	hideWorker(command)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	path := filepath.Join(dir, "child.json")
	var data []byte
	for end := time.Now().Add(8 * time.Second); time.Now().Before(end); {
		if value, err := os.ReadFile(path); err == nil {
			data = value
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	var record map[string]string
	if json.Unmarshal(data, &record) != nil || record["token"] != "" || record["data"] != dir || record["app"] != "restart-fixture" || record["cwd"] != dir {
		t.Fatalf("restart failed: %s", data)
	}
}
