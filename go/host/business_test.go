package host

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// openTestDatabase 使用独立临时目录打开真实 SQLite，确保测试不会接触用户数据。
func openTestDatabase(t *testing.T) *BusinessDatabase {
	t.Helper()
	db, err := OpenBusinessDatabase(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestBusinessDatabaseTransactions 验证权限和来源隔离、版本冲突、批处理回滚、标量过滤及稳定分页。
func TestBusinessDatabaseTransactions(t *testing.T) {
	db := openTestDatabase(t)
	ctx := bridgeContext("https://a.test", "a", "database")
	other := bridgeContext("https://b.test", "b", "database")
	change := DocumentChange{Operation: "put", Collection: "orders", ID: "one", Data: json.RawMessage(`{"status":"draft","paid":true,"amount":2}`)}
	if _, err := db.Batch(context.Background(), []DocumentChange{change}); err == nil {
		t.Fatal("missing permission accepted")
	}
	result, err := db.Batch(ctx, []DocumentChange{change})
	if err != nil || result[0].Revision != 1 {
		t.Fatalf("create: %v %+v", err, result)
	}
	if _, err = db.Get(other, "orders", "one"); err == nil {
		t.Fatal("cross-origin leak")
	}
	if _, err = db.Get(bridgeContext("https://a.test", "second", "database"), "orders", "one"); err != nil {
		t.Fatal(err)
	}
	change.Data = json.RawMessage(`{"status":"updated"}`)
	change.Revision = 99
	first := DocumentChange{Operation: "put", Collection: "orders", ID: "two", Data: json.RawMessage(`{"status":"draft"}`)}
	if _, err = db.Batch(ctx, []DocumentChange{first, change}); err == nil {
		t.Fatal("conflict accepted")
	}
	if _, err = db.Get(ctx, "orders", "two"); err == nil {
		t.Fatal("batch partially committed")
	}
	change.Revision = 1
	result, err = db.Batch(ctx, []DocumentChange{change, first})
	if err != nil || result[0].Revision != 2 {
		t.Fatal(err, result)
	}
	page, err := db.Query(ctx, DocumentQuery{Collection: "orders", Where: map[string]json.RawMessage{"status": json.RawMessage(`"draft"`)}, Limit: 1})
	if err != nil || page.Total != 1 || page.Items[0].ID != "two" {
		t.Fatal(err, page)
	}
	page, err = db.Query(ctx, DocumentQuery{Collection: "orders", SortBy: "id", Limit: 1})
	if err != nil || !page.HasMore || page.NextOffset != 1 || page.Total != 2 {
		t.Fatal(err, page)
	}
	page, err = db.Query(ctx, DocumentQuery{Collection: "orders", SortBy: "id", Offset: page.NextOffset, Limit: 1})
	if err != nil || page.HasMore || page.Items[0].ID != "two" {
		t.Fatal(err, page)
	}
	for _, q := range []DocumentQuery{{Collection: "orders", SortBy: "id; DROP TABLE documents"}, {Collection: "orders", Where: map[string]json.RawMessage{"x'": json.RawMessage(`1`)}}, {Collection: "orders", Where: map[string]json.RawMessage{"x": json.RawMessage(`[]`)}}} {
		if _, err = db.Query(ctx, q); err == nil {
			t.Fatal("invalid query accepted")
		}
	}
	change.Revision = -1
	change.Data = json.RawMessage(`{"name":"` + strings.Repeat("x", maxRecordBytes) + `"}`)
	if _, err = db.Batch(ctx, []DocumentChange{change}); err == nil {
		t.Fatal("oversize record accepted")
	}
}

// TestDatabaseSnapshotRestore 验证真实备份恢复业务记录、拒绝非法快照，并在旧备份恢复时明确清空新数据库。
func TestDatabaseSnapshotRestore(t *testing.T) {
	dir := t.TempDir()
	if err := InitializeData(dir); err != nil {
		t.Fatal(err)
	}
	db, err := OpenBusinessDatabase(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := bridgeContext("https://a.test", "a", "database")
	_, err = db.Batch(ctx, []DocumentChange{{Operation: "put", Collection: "demo", ID: "one", Data: json.RawMessage(`{"value":"before"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	m := NewMaintenance(nil, dir)
	m.AttachDatabase(db)
	backup, err := m.CreateBackup()
	if err != nil {
		t.Fatal(err)
	}
	_, data, err := m.readBackup(backup.ID)
	if err != nil || !databaseSnapshotValid(data[databaseSnapshotFile]) {
		t.Fatal(err)
	}
	var snapshot databaseSnapshot
	_ = json.Unmarshal(data[databaseSnapshotFile], &snapshot)
	snapshot.Documents = append(snapshot.Documents, snapshot.Documents[0])
	bad, _ := json.Marshal(snapshot)
	if databaseSnapshotValid(bad) {
		t.Fatal("duplicate snapshot accepted")
	}
	if err = db.importSnapshot(bad); err == nil {
		t.Fatal("bad import accepted")
	}
	if _, err = db.Get(ctx, "demo", "one"); err != nil {
		t.Fatal("bad import changed data", err)
	}
	_, _ = db.Batch(ctx, []DocumentChange{{Operation: "put", Collection: "demo", ID: "one", Data: json.RawMessage(`{"value":"after"}`), Revision: -1}})
	if err = m.StageRestore(backup.ID); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if err = InitializeData(dir); err != nil {
		t.Fatal(err)
	}
	db, err = OpenBusinessDatabase(dir)
	if err != nil {
		t.Fatal(err)
	}
	value, err := db.Get(ctx, "demo", "one")
	if err != nil || !strings.Contains(string(value.Data), "before") {
		t.Fatal(err, value)
	}
	_ = db.Close()
	// 旧版备份没有 SQLite 条目，恢复时写空快照，下一次注册服务再原子替换数据库。
	if err = m.replaceData(map[string][]byte{}); err != nil {
		t.Fatal(err)
	}
	db, err = OpenBusinessDatabase(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Get(ctx, "demo", "one"); err == nil {
		t.Fatal("legacy restore retained new records")
	}
}

// newTestCenter 组装真实数据库与任务中心，统一按队列、命令、数据库顺序关闭资源。
func newTestCenter(t *testing.T) (*TaskCenter, *Controller, context.Context) {
	t.Helper()
	db := openTestDatabase(t)
	c := New(testConfig(), t.TempDir())
	commands := NewCommandManager(c)
	desktop := NewDesktop(nil, c, t.TempDir())
	center, err := NewTaskCenter(c, db, commands, desktop)
	if err != nil {
		t.Fatal(err)
	}
	c.AttachTaskCenter(center)
	t.Cleanup(func() { center.Shutdown(); commands.Shutdown() })
	return center, c, bridgeContext("https://a.test", "tasks-window", "tasks", "database", "shell", "downloads", "files")
}

// awaitTask 等待有限时间内的真实任务状态，超时输出当前状态用于定位调度或清理故障。
func awaitTask(t *testing.T, m *TaskCenter, ctx context.Context, id, state string) ManagedTask {
	t.Helper()
	var value ManagedTask
	for deadline := time.Now().Add(7 * time.Second); time.Now().Before(deadline); {
		list, err := m.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range list {
			if task.ID == id {
				value = task
				if task.State == state {
					return task
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected %s: %+v", state, value)
	return value
}

// TestTaskQueueOwnershipAndRestart 验证两项并发、排队取消、管理员不能领取他人回调、租约失联及重启中断历史。
func TestTaskQueueOwnershipAndRestart(t *testing.T) {
	m, c, ctx := newTestCenter(t)
	a, _ := m.CreateBusiness(ctx, "first")
	b, _ := m.CreateBusiness(ctx, "second")
	d, _ := m.CreateBusiness(ctx, "third")
	m.dispatch()
	list, _ := m.List(ctx)
	slots := 0
	for _, v := range list {
		if v.State == "ready" {
			slots++
		}
	}
	if slots != 2 {
		t.Fatal("concurrency", list)
	}
	foreign := bridgeContext("https://b.test", "admin", "tasks", "tasks.manage")
	all, _ := m.List(foreign)
	if len(all) != 3 {
		t.Fatal(all)
	}
	if _, err := m.Poll(foreign, a.ID); err == nil {
		t.Fatal("admin claimed foreign callback")
	}
	own, _ := m.List(bridgeContext("https://a.test", "another", "tasks"))
	if len(own) != 0 {
		t.Fatal("foreign tasks visible")
	}
	if err := m.Cancel(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	ready := a
	if task, _ := m.Poll(ctx, a.ID); task.State != "running" {
		ready = b
		_, _ = m.Poll(ctx, b.ID)
	}
	if _, err := m.Report(ctx, ready.ID, 45, "真实进度"); err != nil {
		t.Fatal(err)
	}
	_ = m.Cancel(foreign, ready.ID)
	value, _ := m.Finish(ctx, ready.ID, "completed", "")
	if value.State != "cancelled" {
		t.Fatal("cancel lost", value)
	}
	lease, _ := m.CreateBusiness(ctx, "lost lease")
	m.mu.Lock()
	m.entries[lease.ID].lastBeat = time.Now().Add(-time.Minute)
	m.mu.Unlock()
	m.dispatch()
	awaitTask(t, m, ctx, lease.ID, "interrupted")
	pending, _ := m.CreateBusiness(ctx, "restart")
	m.Shutdown()
	if len(c.tasks) != 0 {
		t.Fatal("exit guards leaked")
	}
	reopened, err := NewTaskCenter(c, m.database, m.commands, m.desktop)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Shutdown()
	value = awaitTask(t, reopened, ctx, pending.ID, "interrupted")
	if value.CanRetry {
		t.Fatal("restart retained code")
	}
}

// TestTaskNativeQueue 使用测试子进程和本地 HTTP 验证命令、下载自动归档与队列执行，终态回收底层历史。
func TestTaskNativeQueue(t *testing.T) {
	t.Setenv("HYBRID_COMMAND_TEST", "1")
	m, c, ctx := newTestCenter(t)
	exe, _ := os.Executable()
	c.config.ShellPrograms = []string{exe}
	m.commands.programs[exe] = exe
	options := CommandOptions{Program: exe, Args: []string{"-test.run=^TestCommandHelperProcess$", "--", "failure"}, TimeoutMs: 3000}
	task, err := m.EnqueueShell(ctx, "failure", options)
	if err != nil {
		t.Fatal(err)
	}
	awaitTask(t, m, ctx, task.ID, "failed")
	retry, err := m.Retry(ctx, task.ID)
	if err != nil || retry.Attempt != 2 {
		t.Fatal(err, retry)
	}
	awaitTask(t, m, ctx, retry.ID, "failed")
	if _, err = m.Retry(bridgeContext("https://a.test", "tasks-window", "tasks"), task.ID); err == nil {
		t.Fatal("retry bypassed shell permission")
	}
	raw, err := m.commands.Start(ctx, CommandOptions{Program: exe, Args: []string{"-test.run=^TestCommandHelperProcess$", "--", "output"}, TimeoutMs: 3000})
	if err != nil {
		t.Fatal(err)
	}
	awaitCommand(t, m.commands, ctx, raw.ID)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("download-test")) }))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "result.txt")
	grant, err := m.desktop.grant(ctx, path, false, true)
	if err != nil {
		t.Fatal(err)
	}
	download, err := m.EnqueueDownload(ctx, "download", server.URL, grant.Token)
	if err != nil {
		t.Fatal(err)
	}
	awaitTask(t, m, ctx, download.ID, "completed")
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "download-test" {
		t.Fatal(err, string(data))
	}
	m.desktop.mu.Lock()
	count := len(m.desktop.downloads)
	m.desktop.mu.Unlock()
	if count != 0 {
		t.Fatal("queue download history leaked")
	}
}
