package host

import (
	"encoding/json"
	"hybrid-infra/go/setting"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// restartTicket 只由宿主生成，固定程序、配置、身份和工作目录；不允许网页传入可执行文件或重启命令。
type restartTicket struct {
	ID       string
	Parent   int
	Target   string
	DataDir  string
	Settings string
	AppID    string
	WorkDir  string
	Token    string
}

// Restart 先检查退出保护和启动配置，再启动等待原进程退出的独立助手；助手未就绪时保留当前应用运行。
func (c *Controller) Restart() error {
	if c.app == nil {
		return Fail("E_UNSUPPORTED", "重启需要桌面应用")
	}
	undo, err := c.reserveMaintenance()
	if err != nil {
		return err
	}
	started := false
	defer func() {
		if !started {
			undo()
		}
	}()
	target, err := os.Executable()
	if err != nil {
		return Fail("E_RESTART", "无法取得当前程序")
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	settings, _ := c.config.ConfigLocation()
	if settings != "" {
		if _, err := setting.ReadConfig(settings, c.config.Identity()); err != nil {
			return Fail("E_RESTART_CONFIG", "启动配置不存在或校验失败，请先保存有效配置")
		}
	}
	workDir, err := os.Getwd()
	if err != nil {
		return err
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	token, err := randomID()
	if err != nil {
		return err
	}
	ticket := restartTicket{id, os.Getpid(), target, c.dataDir, settings, c.config.Identity(), workDir, token}
	dir := filepath.Join(c.dataDir, "restarts")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return diskError(err)
	}
	path := filepath.Join(dir, id+".json")
	ready := path + ".ready"
	if err = jsonWrite(path, ticket); err != nil {
		return diskError(err)
	}
	command := exec.Command(target, "--hybrid-restart-worker", path)
	command.Dir = workDir
	command.Env = append(os.Environ(), "HYBRID_RESTART_TOKEN="+token)
	configureRestartWorker(command)
	if err = command.Start(); err != nil {
		_ = os.Remove(path)
		return Fail("E_RESTART", "无法启动重启助手，应用继续运行")
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		data, e := os.ReadFile(ready)
		if e == nil && string(data) == token {
			started = true
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if !started {
		_ = command.Process.Kill()
		_ = command.Wait()
		_ = os.Remove(path)
		_ = os.Remove(ready)
		return Fail("E_RESTART", "重启助手尚未就绪，应用继续运行")
	}
	_ = command.Process.Release()
	// 让 RPC 先返回；退出流程会保存 KV、关闭设备与数据库，助手等待进程真正退出再启动，避免撞上单实例锁。
	go func() { time.Sleep(400 * time.Millisecond); _ = c.Quit() }()
	return nil
}

// RunRestartWorker 在初始化 WebView 前处理私有重启参数，校验票据与当前程序，等待原进程结束后只启动同一程序。
func RunRestartWorker(args []string) (bool, error) {
	if len(args) == 0 || args[0] != "--hybrid-restart-worker" {
		return false, nil
	}
	if len(args) != 2 {
		return true, Fail("E_RESTART", "重启助手参数无效")
	}
	path, err := filepath.Abs(args[1])
	if err != nil {
		return true, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<10 {
		return true, Fail("E_RESTART", "重启票据无效")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return true, err
	}
	var ticket restartTicket
	own, err := os.Executable()
	if err != nil {
		return true, err
	}
	if json.Unmarshal(data, &ticket) != nil || !validID.MatchString(ticket.ID) || ticket.Parent <= 0 || !filepath.IsAbs(ticket.DataDir) || !filepath.IsAbs(ticket.WorkDir) || !sameExecutable(own, ticket.Target) || path != filepath.Join(ticket.DataDir, "restarts", ticket.ID+".json") || ticket.Token == "" || ticket.Token != os.Getenv("HYBRID_RESTART_TOKEN") {
		return true, Fail("E_RESTART", "重启票据校验失败")
	}
	ready := path + ".ready"
	defer os.Remove(path)
	defer os.Remove(ready)
	if err = waitRestartParent(ticket.Parent, ticket.Target, ready, ticket.Token); err != nil {
		return true, err
	}
	command := restartCommand(ticket)
	if err = command.Start(); err != nil {
		_ = jsonWrite(filepath.Join(ticket.DataDir, "restarts", "result.json"), map[string]string{"code": "E_RESTART_LAUNCH", "time": time.Now().UTC().Format(time.RFC3339)})
		return true, Fail("E_RESTART_LAUNCH", "原程序已退出，但重新启动失败")
	}
	_ = command.Process.Release()
	_ = os.Remove(filepath.Join(ticket.DataDir, "restarts", "result.json"))
	return true, nil
}

// restartCommand 重新读取已保存配置并保留当前身份、数据目录和工作目录，不重放协议参数、更新健康令牌或任意命令行。
func restartCommand(ticket restartTicket) *exec.Cmd {
	command := exec.Command(ticket.Target)
	command.Dir = ticket.WorkDir
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		key = strings.ToUpper(key)
		if key == "HYBRID_SETTINGS" || key == "HYBRID_APP_ID" || key == "HYBRID_DATA_DIR" || key == "HYBRID_RESTART_TOKEN" || strings.HasPrefix(key, "HYBRID_UPDATE_") {
			continue
		}
		command.Env = append(command.Env, item)
	}
	command.Env = append(command.Env, "HYBRID_APP_ID="+ticket.AppID, "HYBRID_DATA_DIR="+ticket.DataDir)
	if ticket.Settings != "" {
		command.Env = append(command.Env, "HYBRID_SETTINGS="+ticket.Settings)
	}
	return command
}
