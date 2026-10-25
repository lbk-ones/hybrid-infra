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
	"strings"
	"time"

	"hybrid-infra/go/setting"
)

// updateTicket 只由宿主写入，工作进程启动后重新验签；票据不通过 RPC 返回。
type updateTicket struct {
	ID         string
	Parent     int
	Target     string
	DataDir    string
	Settings   string
	OldVersion string
	NewVersion string
	Signed     SignedUpdate
	Config     setting.UpdateConfig
	Rollback   bool
	BackupID   string
	Hash       string
}

// rollbackRecord 记录上个已验证程序及对应数据备份，回退会同时还原当时的数据。
type rollbackRecord struct {
	Version  string
	Hash     string
	BackupID string
}

// updateDataLease 在启动新版本前释放锁，失败回退时重新取得锁，避免与其他启动中的实例竞争。
type updateDataLease struct {
	dir     string
	release func()
}

// lock 幂等取得当前更新事务的数据锁。
func (l *updateDataLease) lock() error {
	if l.release != nil {
		return nil
	}
	release, err := acquireDataLock(l.dir)
	if err == nil {
		l.release = release
	}
	return err
}

// unlock 幂等释放当前事务的数据锁，子进程随后可正常初始化。
func (l *updateDataLease) unlock() {
	if l.release != nil {
		l.release()
		l.release = nil
	}
}

// hashFile 计算本地文件 SHA256，用于安装前再次校验和验证回滚缓存。
func hashFile(path string) (string, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// copyUpdateFile 只复制本地普通文件，使用原子写入并保留可执行权限。
func copyUpdateFile(source, target string) error {
	info, e := os.Lstat(source)
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() || info.Size() > 256<<20 {
		return Fail("E_UPDATE_INVALID", "更新文件不是普通文件或超过限制")
	}
	b, e := os.ReadFile(source)
	if e != nil {
		return e
	}
	if e = atomicWrite(target, b); e != nil {
		return e
	}
	return os.Chmod(target, 0700)
}

// InstallUpdate 在无业务任务时启动独立工作进程；当前程序退出后才替换，失败则还原程序和数据。
func (m *Maintenance) InstallUpdate(rollback bool) error {
	m.op.Lock()
	defer m.op.Unlock()
	if runtime.GOOS != "windows" {
		return Fail("E_UNSUPPORTED", "自动更新仅支持 Windows 便携版")
	}
	if m.controller == nil {
		return Fail("E_UPDATE_DISABLED", "维护宿主未初始化")
	}
	undo, e := m.controller.reserveMaintenance()
	if e != nil {
		return e
	}
	ok := false
	defer func() {
		if !ok {
			undo()
		}
	}()
	target, e := os.Executable()
	if e != nil {
		return e
	}
	target, e = filepath.EvalSymlinks(target)
	if e != nil {
		return e
	}
	probe, e := os.CreateTemp(filepath.Dir(target), ".hybrid-write-test-*")
	if e != nil {
		return Fail("E_UPDATE_PERMISSION", "安装目录不可写；请使用系统安装器更新")
	}
	_ = probe.Close()
	_ = os.Remove(probe.Name())
	ticket := updateTicket{ID: maintenanceID(), Parent: os.Getpid(), Target: target, DataDir: m.dir, OldVersion: Version, Rollback: rollback}
	ticket.Settings, e = setting.ConfigPath()
	if e != nil {
		return e
	}
	if rollback {
		var record rollbackRecord
		b, err := os.ReadFile(filepath.Join(m.dir, "updates", "rollback.json"))
		if err != nil || json.Unmarshal(b, &record) != nil {
			return Fail("E_NO_ROLLBACK", "没有可回滚的版本")
		}
		ticket.NewVersion = record.Version
		ticket.BackupID = record.BackupID
		ticket.Hash = record.Hash
		if _, _, e = m.readBackup(record.BackupID); e != nil {
			return e
		}
		actual, err := hashFile(filepath.Join(m.dir, "updates", "previous.exe"))
		if err != nil || actual != record.Hash {
			return Fail("E_UPDATE_HASH", "回滚程序校验失败")
		}
	} else {
		m.mu.Lock()
		state := m.update
		m.mu.Unlock()
		if state.Stage != "ready" {
			return Fail("E_BUSY", "请先下载并校验更新")
		}
		ticket.Signed = state.signed
		ticket.Config = m.controller.config.Updates
		manifest, err := verifyUpdate(state.signed, ticket.Config)
		if err != nil {
			return err
		}
		ticket.NewVersion = manifest.Version
		ticket.Hash = manifest.SHA256
		actual, err := hashFile(filepath.Join(m.dir, "updates", "candidate.exe"))
		if err != nil || actual != manifest.SHA256 {
			return Fail("E_UPDATE_HASH", "安装前更新校验失败")
		}
	}
	directory := filepath.Join(m.dir, "updates", ticket.ID)
	if e = os.MkdirAll(directory, 0700); e != nil {
		return e
	}
	worker := filepath.Join(directory, "worker.exe")
	if e = copyUpdateFile(target, worker); e != nil {
		return e
	}
	path := filepath.Join(directory, "ticket.json")
	if e = jsonWrite(path, ticket); e != nil {
		return e
	}
	command := exec.Command(worker, "--hybrid-update-worker", path)
	hideWorker(command)
	if e = command.Start(); e != nil {
		return Fail("E_UPDATE_WORKER", "无法启动更新工作进程")
	}
	// 等工作进程持有父进程句柄再退出；避免启动较慢时原进程已消失，无法验证其身份。
	deadline := time.Now().Add(5 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(directory, "worker-ready.json")); err == nil {
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		_ = command.Process.Kill()
		_ = command.Wait()
		return Fail("E_UPDATE_WORKER", "更新工作进程尚未就绪，应用继续运行")
	}
	_ = command.Process.Release()
	ok = true
	m.record("UPDATE_RESTARTING")
	// 留出 RPC 响应时间再请求退出；工作进程等待父进程真正结束，包括 KV 的关闭保存。
	go func() { time.Sleep(400 * time.Millisecond); _ = m.controller.Quit() }()
	return nil
}

// RunUpdateWorker 识别专用启动参数；普通启动返回 false，工作进程无需启动 WebView。
func RunUpdateWorker(args []string) (bool, error) {
	if len(args) < 1 || args[0] != "--hybrid-update-worker" {
		return false, nil
	}
	if len(args) != 2 {
		return true, Fail("E_UPDATE_INVALID", "工作进程参数无效")
	}
	return true, runUpdateWorker(args[1])
}

// runUpdateWorker 固定票据位置与执行文件后，等待父进程退出并执行可回退替换；不调用 shell。
func runUpdateWorker(path string) error {
	executable, e := os.Executable()
	if e != nil {
		return e
	}
	path, e = filepath.Abs(path)
	if e != nil || filepath.Clean(filepath.Join(filepath.Dir(executable), "ticket.json")) != path {
		return Fail("E_UPDATE_INVALID", "票据不属于当前工作进程")
	}
	b, e := os.ReadFile(path)
	if e != nil || len(b) > 1<<20 {
		return Fail("E_UPDATE_INVALID", "票据读取失败")
	}
	var ticket updateTicket
	if json.Unmarshal(b, &ticket) != nil || ticket.ID != filepath.Base(filepath.Dir(path)) || filepath.Clean(filepath.Join(ticket.DataDir, "updates", ticket.ID, "ticket.json")) != path || !filepath.IsAbs(ticket.Target) || filepath.Ext(ticket.Target) != ".exe" || ticket.Parent <= 0 {
		return Fail("E_UPDATE_INVALID", "票据约束无效")
	}
	m := NewMaintenance(nil, ticket.DataDir)
	result := UpdateState{Stage: "failed", Version: ticket.NewVersion, Code: "E_UPDATE_INSTALL"}
	defer func() { _ = jsonWrite(filepath.Join(ticket.DataDir, "updates", "result.json"), result) }()
	source := filepath.Join(ticket.DataDir, "updates", "candidate.exe")
	if ticket.Rollback {
		source = filepath.Join(ticket.DataDir, "updates", "previous.exe")
		var record rollbackRecord
		b, e = os.ReadFile(filepath.Join(ticket.DataDir, "updates", "rollback.json"))
		if e != nil || json.Unmarshal(b, &record) != nil || record.Hash != ticket.Hash || record.BackupID != ticket.BackupID {
			return Fail("E_UPDATE_INVALID", "回滚票据无效")
		}
		if _, _, e = m.readBackup(ticket.BackupID); e != nil {
			return e
		}
	} else {
		manifest, err := verifyUpdate(ticket.Signed, ticket.Config)
		if err != nil || manifest.SHA256 != ticket.Hash || manifest.Version != ticket.NewVersion {
			return Fail("E_UPDATE_SIGNATURE", "工作进程验签失败")
		}
	}
	hash, e := hashFile(source)
	if e != nil || hash != ticket.Hash {
		return Fail("E_UPDATE_HASH", "工作进程文件校验失败")
	}
	if e = validateExecutable(source); e != nil {
		return e
	}
	if e = waitUpdateParent(ticket.Parent, ticket.Target, filepath.Join(filepath.Dir(path), "worker-ready.json")); e != nil {
		return e
	}
	lease := &updateDataLease{dir: ticket.DataDir}
	if e = lease.lock(); e != nil {
		return e
	}
	// 数据替换期间持有独占锁；启动子进程前由事务释放，否则新进程会被自己的维护锁阻止。
	defer lease.unlock()
	return executeUpdate(ticket, m, source, path, &result, func(t updateTicket, h, token string) (*exec.Cmd, error) {
		lease.unlock()
		return startUpdatedApp(t, h, token)
	}, waitUpdateHealth, lease)
}

// executeUpdate 在原进程退出后执行替换事务；进程启动和健康探测可注入，便于用临时目录验证回滚。
func executeUpdate(ticket updateTicket, m *Maintenance, source, path string, result *UpdateState, launch func(updateTicket, string, string) (*exec.Cmd, error), healthy func(*exec.Cmd, string, string, string, time.Duration) bool, leases ...*updateDataLease) error {
	// 进程已退出，KV 关闭保存也已完成，此时备份才代表替换前最终数据。
	before, e := m.createBackup()
	if e != nil {
		return e
	}
	old := filepath.Join(filepath.Dir(path), "before.exe")
	if e = copyUpdateFile(ticket.Target, old); e != nil {
		return e
	}
	oldHash, e := hashFile(old)
	if e != nil {
		return e
	}
	health := filepath.Join(filepath.Dir(path), "health.json")
	_ = os.Remove(health)
	// 每次失败均恢复同一组程序与数据；失败的子进程终止后才写回数据。
	recoverOld := func() error {
		if len(leases) > 0 {
			if err := leases[0].lock(); err != nil {
				return err
			}
		}
		if err := copyUpdateFile(old, ticket.Target); err != nil {
			return err
		}
		_, data, err := m.readBackup(before.ID)
		if err != nil {
			return err
		}
		if err = m.replaceData(data); err != nil {
			return err
		}
		_, err = launch(ticket, "", "")
		return err
	}
	// 回退本身失败时必须保留独立错误码，提示人工使用仍保留的 before.exe 和数据快照。
	fail := func(cause error) error {
		if err := recoverOld(); err != nil {
			result.Code = "E_ROLLBACK_FAILED"
			return err
		}
		return cause
	}
	if ticket.Rollback {
		_, data, err := m.readBackup(ticket.BackupID)
		if err != nil {
			return err
		}
		if err = m.replaceData(data); err != nil {
			return fail(err)
		}
	}
	if e = copyUpdateFile(source, ticket.Target); e != nil {
		return fail(e)
	}
	token := maintenanceID()
	child, e := launch(ticket, health, token)
	if e != nil {
		return fail(e)
	}
	if !healthy(child, health, token, ticket.NewVersion, 40*time.Second) {
		if e = recoverOld(); e != nil {
			result.Code = "E_ROLLBACK_FAILED"
			return e
		}
		result.Stage = "rolled_back"
		result.Version = ticket.OldVersion
		result.Code = "E_UPDATE_HEALTH"
		return nil
	}
	if e = copyUpdateFile(old, filepath.Join(ticket.DataDir, "updates", "previous.exe")); e != nil {
		result.Stage = "installed"
		result.Code = "E_ROLLBACK_CACHE"
		return e
	}
	if e = jsonWrite(filepath.Join(ticket.DataDir, "updates", "rollback.json"), rollbackRecord{ticket.OldVersion, oldHash, before.ID}); e != nil {
		return e
	}
	result.Stage = "installed"
	result.Code = ""
	return nil
}

// startUpdatedApp 固定启动目标程序并保留数据目录和设置文件；健康令牌只通过子进程环境传递。
func startUpdatedApp(ticket updateTicket, health, token string) (*exec.Cmd, error) {
	command := exec.Command(ticket.Target)
	command.Dir = filepath.Dir(ticket.Target)
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(strings.ToUpper(key), "HYBRID_") {
			continue
		}
		command.Env = append(command.Env, item)
	}
	command.Env = append(command.Env, "HYBRID_DATA_DIR="+ticket.DataDir)
	if ticket.Settings != "" {
		command.Env = append(command.Env, "HYBRID_SETTINGS="+ticket.Settings)
	}
	if health != "" {
		command.Env = append(command.Env, "HYBRID_UPDATE_HEALTH="+health, "HYBRID_UPDATE_TOKEN="+token)
	}
	err := command.Start()
	return command, err
}

// waitUpdateHealth 同时检查进程存活和主窗口 runtime 就绪；超时先终止并等待子进程，防止回滚与其写数据竞争。
func waitUpdateHealth(child *exec.Cmd, path, token, version string, timeout time.Duration) bool {
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	var ready time.Time
	for {
		select {
		case <-done:
			return false
		case <-deadline.C:
			_ = child.Process.Kill()
			<-done
			return false
		case <-ticker.C:
			b, e := os.ReadFile(path)
			var health struct {
				Token   string
				Version string
				PID     int
			}
			if e == nil && json.Unmarshal(b, &health) == nil && health.Token == token && health.Version == version && health.PID == child.Process.Pid {
				if ready.IsZero() {
					ready = time.Now()
				}
				if time.Since(ready) > 2*time.Second {
					return true
				}
			}
		}
	}
}

// MarkUpdateHealthy 只允许写入当前数据目录下的随机工作票据目录；普通启动不创建标记。
func (m *Maintenance) MarkUpdateHealthy() {
	path, token := os.Getenv("HYBRID_UPDATE_HEALTH"), os.Getenv("HYBRID_UPDATE_TOKEN")
	if path == "" || token == "" {
		return
	}
	relative, e := filepath.Rel(filepath.Join(m.dir, "updates"), path)
	if e != nil || strings.HasPrefix(relative, "..") || filepath.Base(path) != "health.json" || len(strings.Split(relative, string(filepath.Separator))) != 2 {
		return
	}
	_ = jsonWrite(path, map[string]any{"Token": token, "Version": Version, "PID": os.Getpid()})
}

// StartMaintenance 按启动配置执行可选检查与下载；仅下载，不自动重启或安装。
func (m *Maintenance) StartMaintenance() {
	if m.controller == nil || !m.controller.config.Updates.Enabled || !m.controller.config.Updates.CheckOnStartup {
		return
	}
	ctx := contextForMaintenance()
	go func() {
		state, e := m.CheckUpdates(ctx)
		if e != nil {
			m.RecordError(e)
			return
		}
		if state.Stage == "available" && m.controller.config.Updates.AutoDownload {
			if e = m.DownloadUpdate(ctx); e != nil {
				m.RecordError(e)
			}
		}
	}()
}

// contextForMaintenance 为宿主内部更新任务提供固定主窗口归属，不授予页面额外权限。
func contextForMaintenance() context.Context {
	return context.WithValue(context.Background(), principalKey{}, Principal{Origin: "http://wails.localhost", WindowName: "main-window"})
}
