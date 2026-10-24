package host

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// bridgeContext 创建指定来源和窗口的已认证上下文，能力集合可按测试用例收窄。
func bridgeContext(origin, window string, caps ...string) context.Context {
	return context.WithValue(context.Background(), principalKey{}, Principal{Origin: origin, WindowName: window, Capabilities: caps})
}

// TestCacheIsolationTTLAndAtomicUpdate 验证窗口共享、来源隔离、全局授权、TTL 以及并发修订号更新不会丢失写入。
func TestCacheIsolationTTLAndAtomicUpdate(t *testing.T) {
	c := NewSharedCache()
	ctx := bridgeContext("https://a.test", "a", "cache", "cache.global")
	same := bridgeContext("https://a.test", "b", "cache")
	other := bridgeContext("https://b.test", "c", "cache")
	if _, err := c.Set(ctx, "demo", "counter", json.RawMessage(`0`), 0, false, false, 0); err != nil {
		t.Fatal(err)
	}
	value, err := c.Get(same, "demo", "counter", false)
	if err != nil || !value.Found {
		t.Fatal("same origin windows cannot share cache")
	}
	if value, err = c.Get(other, "demo", "counter", false); err != nil || value.Found {
		t.Fatal("cache leaked to another origin")
	}
	if _, err = c.Get(same, "demo", "counter", true); err == nil {
		t.Fatal("global cache allowed without extra permission")
	}
	var wg sync.WaitGroup
	// 使用修订号重试竞争更新，最终总数必须与成功写入数量一致。
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				old, err := c.Get(ctx, "demo", "counter", false)
				if err != nil {
					t.Error(err)
					return
				}
				var n int
				_ = json.Unmarshal(old.Value, &n)
				data, _ := json.Marshal(n + 1)
				if _, err = c.Set(ctx, "demo", "counter", data, 0, false, true, old.Revision); err == nil {
					return
				}
			}
		}()
	}
	wg.Wait()
	value, _ = c.Get(ctx, "demo", "counter", false)
	if string(value.Value) != "12" {
		t.Fatalf("lost concurrent update: %s", value.Value)
	}
	if _, err = c.Set(ctx, "demo", "short", json.RawMessage(`null`), 10, false, false, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	value, _ = c.Get(ctx, "demo", "short", false)
	if value.Found {
		t.Fatal("expired value was returned")
	}
	if err = c.Clear(ctx, "demo", false); err != nil {
		t.Fatal(err)
	}
	value, _ = c.Get(same, "demo", "counter", false)
	if value.Found {
		t.Fatal("namespace clear failed")
	}
}

// busSubscription 为隔离测试创建完整事件会话和单个订阅，测试失败时立即报告宿主错误。
func busSubscription(t *testing.T, b *EventBus, ctx context.Context, kind string, global bool) (string, string) {
	t.Helper()
	session, err := b.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := b.Subscribe(ctx, session, "demo:topic", kind, global)
	if err != nil {
		t.Fatal(err)
	}
	return session, listener
}

// TestEventRoutingAndRequestResponse 验证发送目标、跨来源隔离、异步响应、伪造响应拒绝和处理器关闭时的资源回收。
func TestEventRoutingAndRequestResponse(t *testing.T) {
	b := NewEventBus()
	defer b.Shutdown()
	a := bridgeContext("https://a.test", "a", "events")
	same := bridgeContext("https://a.test", "b", "events")
	other := bridgeContext("https://b.test", "c", "events")
	sa, _ := busSubscription(t, b, a, "event", false)
	sb, listener := busSubscription(t, b, same, "event", false)
	sc, _ := busSubscription(t, b, other, "event", false)
	count, err := b.Emit(a, "demo:topic", json.RawMessage(`{"value":42}`), "b", false)
	if err != nil || count != 1 {
		t.Fatalf("target count=%d %v", count, err)
	}
	messages, err := b.Poll(same, sb)
	if err != nil || len(messages) != 1 || messages[0].ListenerID != listener || messages[0].Sender != "a" {
		t.Fatalf("unexpected delivery: %+v %v", messages, err)
	}
	b.mu.Lock()
	leaked := len(b.sessions[sa].queue) + len(b.sessions[sc].queue)
	b.mu.Unlock()
	if leaked != 0 {
		t.Fatal("event leaked to a non-target subscription")
	}
	handler, handlerID := busSubscription(t, b, same, "handler", false)
	answer := make(chan error, 1)
	go func() {
		data, err := b.Request(a, "demo:topic", json.RawMessage(`{"request":true}`), "b", false, 1000)
		if err == nil && string(data) != `{"response":true}` {
			t.Error("response data changed")
		}
		answer <- err
	}()
	messages, err = b.Poll(same, handler)
	if err != nil || len(messages) != 1 || messages[0].Kind != "request" {
		t.Fatalf("request delivery: %+v %v", messages, err)
	}
	request := messages[0].RequestID
	if err = b.Respond(a, sa, handlerID, request, json.RawMessage(`null`), false); err == nil {
		t.Fatal("forged responder accepted")
	}
	if err = b.Respond(same, handler, handlerID, request, json.RawMessage(`{"response":true}`), false); err != nil {
		t.Fatal(err)
	}
	if err = <-answer; err != nil {
		t.Fatal(err)
	}
	if err = b.Respond(same, handler, handlerID, request, json.RawMessage(`null`), false); err == nil {
		t.Fatal("duplicate response accepted")
	}
	go func() { _, err := b.Request(a, "demo:topic", json.RawMessage(`null`), "b", false, 1000); answer <- err }()
	if _, err = b.Poll(same, handler); err != nil {
		t.Fatal(err)
	}
	b.CloseWindow("b")
	if err = <-answer; err == nil {
		t.Fatal("request survived handler window close")
	}
	b.mu.Lock()
	pending := len(b.requests)
	b.mu.Unlock()
	if pending != 0 {
		t.Fatal("pending request leaked")
	}
}

// TestEventsGlobalPermissionAndTimeout 验证全局事件需要双方授权、请求超时和长轮询取消都能清理资源。
func TestEventsGlobalPermissionAndTimeout(t *testing.T) {
	b := NewEventBus()
	defer b.Shutdown()
	a := bridgeContext("https://a.test", "a", "events", "events.global")
	other := bridgeContext("https://b.test", "b", "events", "events.global")
	limited := bridgeContext("https://c.test", "c", "events")
	session, _ := busSubscription(t, b, other, "event", true)
	if _, err := b.Emit(limited, "demo:topic", json.RawMessage(`null`), "", true); err == nil {
		t.Fatal("global emission allowed without permission")
	}
	if count, err := b.Emit(a, "demo:topic", json.RawMessage(`1`), "", true); err != nil || count != 1 {
		t.Fatalf("global emission failed: %d %v", count, err)
	}
	if _, err := b.Poll(other, session); err != nil {
		t.Fatal(err)
	}
	handler, _ := busSubscription(t, b, other, "handler", true)
	if _, err := b.Request(a, "demo:topic", json.RawMessage(`null`), "b", true, 10); err == nil {
		t.Fatal("request without response did not time out")
	}
	b.mu.Lock()
	pending := len(b.requests)
	queued := len(b.sessions[handler].queue)
	b.mu.Unlock()
	if pending != 0 || queued != 0 {
		t.Fatal("timed-out request leaked")
	}
	cancelCtx, cancel := context.WithCancel(other)
	cancel()
	if _, err := b.Poll(cancelCtx, handler); err == nil {
		t.Fatal("cancelled polling did not return")
	}
}

// TestEventSessionReplacementAndBackpressure 验证刷新替换旧会话，和大消息广播在容量不足时不会部分投递。
func TestEventSessionReplacementAndBackpressure(t *testing.T) {
	b := NewEventBus()
	defer b.Shutdown()
	a := bridgeContext("https://a.test", "a", "events")
	other := bridgeContext("https://a.test", "b", "events")
	old, _ := busSubscription(t, b, a, "handler", false)
	fresh, err := b.Open(a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Poll(a, old); err == nil {
		t.Fatal("old page session survived replacement")
	}
	if _, err = b.Subscribe(a, fresh, "demo:topic", "handler", false); err != nil {
		t.Fatal(err)
	}
	receiver, _ := busSubscription(t, b, other, "event", false)
	payload, _ := json.Marshal(strings.Repeat("x", 60000))
	for range 4 {
		if _, err = b.Emit(a, "demo:topic", payload, "b", false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = b.Emit(a, "demo:topic", payload, "b", false); err == nil {
		t.Fatal("oversized event queue accepted")
	}
	messages, err := b.Poll(other, receiver)
	if err != nil || len(messages) != 4 {
		t.Fatalf("queue changed after rejected emission: %d %v", len(messages), err)
	}
}

// TestScriptModeWithExplicitPermission 使用固定无副作用脚本验证额外授权后的完整脚本执行和 UTF-8 输出，不通过 UI 自动化执行命令。
func TestScriptModeWithExplicitPermission(t *testing.T) {
	c := New(testConfig(), t.TempDir())
	m := NewCommandManager(c)
	defer m.Shutdown()
	ctx := bridgeContext("https://a.test", "script-window", "shell", "shell.script")
	script := "printf '脚本-42'"
	if runtime.GOOS == "windows" {
		script = "Write-Output '脚本-42'"
	}
	job, err := m.Start(ctx, CommandOptions{Mode: "script", Script: script, TimeoutMs: 5000})
	if err != nil {
		t.Fatal(err)
	}
	result := awaitCommand(t, m, ctx, job.ID)
	if result.Job.State != "completed" || strings.TrimSpace(string(result.Stdout)) != "脚本-42" {
		t.Fatalf("script failed: %+v", result.Job)
	}
}

// TestCommandHelperProcess 在测试自身的子进程中生成可控输出、退出码或延迟，不执行用户文件和网络命令。
func TestCommandHelperProcess(t *testing.T) {
	if os.Getenv("HYBRID_COMMAND_TEST") != "1" {
		return
	}
	args := os.Args
	mode := args[len(args)-1]
	switch mode {
	case "output":
		_, _ = os.Stdout.WriteString("输出-42")
		_, _ = os.Stderr.WriteString("stderr")
		os.Exit(0)
	case "failure":
		os.Exit(7)
	case "slow":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	case "large":
		_, _ = os.Stdout.Write(make([]byte, 300*1024))
		os.Exit(0)
	}
	os.Exit(2)
}

// awaitCommand 等待任务终态，超时立即失败，所有输出只来自本次测试启动的子进程。
func awaitCommand(t *testing.T, m *CommandManager, ctx context.Context, id string) CommandOutput {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		value, err := m.Read(ctx, id, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if value.Job.State != "running" {
			return value
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("command did not finish")
	return CommandOutput{}
}

// TestCommandsPermissionsOutputCancelAndTimeout 验证白名单、脚本额外权限、输出字节、退出码、取消、超时和退出保护释放。
func TestCommandsPermissionsOutputCancelAndTimeout(t *testing.T) {
	t.Setenv("HYBRID_COMMAND_TEST", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		t.Fatal(err)
	}
	config := testConfig()
	config.ShellPrograms = []string{exe}
	c := New(config, t.TempDir())
	m := NewCommandManager(c)
	defer m.Shutdown()
	ctx := bridgeContext("https://a.test", "command-window", "shell")
	options := CommandOptions{Program: exe, Args: []string{"-test.run=^TestCommandHelperProcess$", "--", "output"}, TimeoutMs: 3000}
	job, err := m.Start(ctx, options)
	if err != nil {
		t.Fatalf("start on %s: %v", runtime.GOOS, err)
	}
	result := awaitCommand(t, m, ctx, job.ID)
	if result.Job.State != "completed" || string(result.Stdout) != "输出-42" || string(result.Stderr) != "stderr" {
		t.Fatalf("unexpected output: %+v", result.Job)
	}
	if _, err = m.Read(bridgeContext("https://b.test", "command-window", "shell"), job.ID, 0, 0); err == nil {
		t.Fatal("command output leaked")
	}
	if _, err = m.Start(ctx, CommandOptions{Program: "not-allowed"}); err == nil {
		t.Fatal("program outside allowlist ran")
	}
	if _, err = m.Start(ctx, CommandOptions{Mode: "script", Script: "echo test"}); err == nil {
		t.Fatal("script ran without shell.script permission")
	}
	options.Args[len(options.Args)-1] = "failure"
	job, err = m.Start(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	result = awaitCommand(t, m, ctx, job.ID)
	if result.Job.State != "failed" || result.Job.ExitCode != 7 {
		t.Fatal("exit code was lost")
	}
	options.Args[len(options.Args)-1] = "slow"
	job, err = m.Start(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Tasks()) == 0 {
		t.Fatal("command missing exit protection")
	}
	if err = m.Cancel(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	result = awaitCommand(t, m, ctx, job.ID)
	if result.Job.State != "cancelled" {
		t.Fatalf("cancel: %+v", result.Job)
	}
	options.TimeoutMs = 30
	job, err = m.Start(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	result = awaitCommand(t, m, ctx, job.ID)
	if result.Job.State != "timed_out" {
		t.Fatalf("timeout: %+v", result.Job)
	}
	options.TimeoutMs = 3000
	options.Args[len(options.Args)-1] = "large"
	job, err = m.Start(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	result = awaitCommand(t, m, ctx, job.ID)
	if !result.Job.Truncated || len(result.Stdout) != 256*1024 {
		t.Fatal("output limit was not enforced")
	}
	if len(c.Tasks()) != 0 {
		t.Fatal("command exit protection leaked")
	}
}
