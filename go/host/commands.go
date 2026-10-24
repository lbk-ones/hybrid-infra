package host

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CommandOptions 区分直接程序和完整脚本；程序只允许启动时解析的白名单项，脚本额外要求 shell.script 权限。
type CommandOptions struct {
	Mode      string   `json:"mode"`
	Program   string   `json:"program"`
	Args      []string `json:"args"`
	Script    string   `json:"script"`
	Directory string   `json:"directory"`
	TimeoutMs int      `json:"timeoutMs"`
}

// CommandInfo 是命令任务状态，不包含命令参数或秘密环境变量；exitCode 在任务结束后有效。
type CommandInfo struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	ExitCode  int    `json:"exitCode"`
	Truncated bool   `json:"truncated"`
	Error     *Error `json:"error,omitempty"`
}

// CommandOutput 通过字节游标返回新增输出，[]byte 由绑定转换为 Base64，前端可按 UTF-8、GBK 等实际编码解码。
type CommandOutput struct {
	Job          CommandInfo `json:"job"`
	Stdout       []byte      `json:"stdout"`
	Stderr       []byte      `json:"stderr"`
	StdoutOffset int         `json:"stdoutOffset"`
	StderrOffset int         `json:"stderrOffset"`
}
type commandJob struct {
	info           CommandInfo
	owner          string
	window         string
	stdout, stderr []byte
	cancel         context.CancelFunc
	done           chan struct{}
}

// CommandManager 管理有限数量的后台进程和输出，绑定窗口退出保护；它提供操作系统命令能力，不是进程安全沙箱。
type CommandManager struct {
	mu         sync.Mutex
	controller *Controller
	programs   map[string]string
	jobs       map[string]*commandJob
	closed     bool
}

// NewCommandManager 在启动时固定白名单程序的绝对路径；未安装的程序不启用，不随业务请求或工作目录重新解析 PATH。
func NewCommandManager(c *Controller) *CommandManager {
	m := &CommandManager{controller: c, programs: map[string]string{}, jobs: map[string]*commandJob{}}
	for _, program := range c.config.ShellPrograms {
		if path, err := exec.LookPath(program); err == nil {
			if absolute, err := filepath.Abs(path); err == nil {
				m.programs[program] = absolute
			}
		}
	}
	return m
}

// command 校验参数并生成命令；不会把程序模式的参数拼接成脚本，工作目录必须是实际存在的绝对目录。
func (m *CommandManager) command(ctx context.Context, o CommandOptions) (*exec.Cmd, error) {
	if err := Require(ctx, "shell"); err != nil {
		return nil, err
	}
	var cmd *exec.Cmd
	switch o.Mode {
	case "", "program":
		program, ok := m.programs[o.Program]
		if !ok {
			return nil, Fail("E_PROGRAM_NOT_ALLOWED", "程序未在 shellPrograms 白名单中启用或尚未安装")
		}
		if len(o.Args) > 128 {
			return nil, Fail("E_INVALID_ARGUMENT", "命令参数过多")
		}
		for _, arg := range o.Args {
			if len(arg) > 8192 || strings.ContainsRune(arg, 0) {
				return nil, Fail("E_INVALID_ARGUMENT", "命令参数过长或包含空字符")
			}
		}
		cmd = exec.Command(program, o.Args...)
	case "script":
		if err := Require(ctx, "shell.script"); err != nil {
			return nil, err
		}
		if len(o.Script) == 0 || len(o.Script) > 65536 || strings.ContainsRune(o.Script, 0) {
			return nil, Fail("E_INVALID_ARGUMENT", "脚本必须是 1–65536 字节且不能包含空字符")
		}
		cmd = scriptCommand(o.Script)
	default:
		return nil, Fail("E_INVALID_ARGUMENT", "命令模式必须是 program 或 script")
	}
	if o.Directory != "" {
		if !filepath.IsAbs(o.Directory) {
			return nil, Fail("E_INVALID_ARGUMENT", "工作目录必须是绝对路径")
		}
		info, err := os.Stat(o.Directory)
		if err != nil || !info.IsDir() {
			return nil, Fail("E_INVALID_ARGUMENT", "工作目录不存在或不是目录")
		}
		cmd.Dir = o.Directory
	}
	cmd.WaitDelay = 2 * time.Second
	return cmd, nil
}

// commandWriter 消费完整输出流，仅保留 stdout/stderr 合计最多 256 KiB，超过部分仍持续读取，避免子进程因管道填满而阻塞。
type commandWriter struct {
	manager *CommandManager
	job     *commandJob
	stderr  bool
}

// Write 记录输出字节并返回完整消费长度，截断状态通过任务快照告知业务。
func (w *commandWriter) Write(p []byte) (int, error) {
	w.manager.mu.Lock()
	defer w.manager.mu.Unlock()
	remaining := 256*1024 - len(w.job.stdout) - len(w.job.stderr)
	take := len(p)
	if take > remaining {
		take = remaining
		w.job.info.Truncated = true
	}
	if w.stderr {
		w.job.stderr = append(w.job.stderr, p[:take]...)
	} else {
		w.job.stdout = append(w.job.stdout, p[:take]...)
	}
	return len(p), nil
}

// Start 启动独立于短期 RPC 的命令任务，最多并发 4 个；超时、取消、窗口关闭或退出会终止受管理进程树。
func (m *CommandManager) Start(ctx context.Context, o CommandOptions) (CommandInfo, error) {
	if o.TimeoutMs == 0 {
		o.TimeoutMs = 30000
	}
	if o.TimeoutMs < 1 || o.TimeoutMs > 600000 {
		return CommandInfo{}, Fail("E_INVALID_ARGUMENT", "命令超时必须是 1–600000 毫秒")
	}
	cmd, err := m.command(ctx, o)
	if err != nil {
		return CommandInfo{}, err
	}
	kill, started, cleanup, err := prepareCommand(cmd)
	if err != nil {
		return CommandInfo{}, Fail("E_PROCESS", "无法创建进程管理资源")
	}
	m.mu.Lock()
	running := 0
	for _, j := range m.jobs {
		if j.info.State == "running" {
			running++
		}
	}
	if m.closed || running >= 4 || len(m.jobs) >= 100 {
		m.mu.Unlock()
		cleanup()
		return CommandInfo{}, Fail("E_BUSY", "命令并发或历史数量已达上限，或应用正在退出")
	}
	id, err := randomID()
	if err != nil {
		m.mu.Unlock()
		cleanup()
		return CommandInfo{}, err
	}
	task, err := m.controller.BeginTask(ctx, "执行桌面命令")
	if err != nil {
		m.mu.Unlock()
		cleanup()
		return CommandInfo{}, err
	}
	background, cancel := context.WithTimeout(context.Background(), time.Duration(o.TimeoutMs)*time.Millisecond)
	job := &commandJob{info: CommandInfo{ID: id, State: "running", ExitCode: -1}, owner: resourceOwner(ctx), window: caller(ctx), cancel: cancel, done: make(chan struct{})}
	cmd.Stdout = &commandWriter{manager: m, job: job}
	cmd.Stderr = &commandWriter{manager: m, job: job, stderr: true}
	// 启动期间若 RPC 已取消也撤销进程；返回任务编号后由显式 Cancel 和窗口生命周期负责取消。
	stop := context.AfterFunc(ctx, cancel)
	err = cmd.Start()
	if err == nil {
		err = started()
	}
	if err != nil {
		_ = kill()
		cancel()
		stop()
		m.mu.Unlock()
		if cmd.Process != nil {
			_ = cmd.Wait()
		}
		cleanup()
		_ = m.controller.EndTask(ctx, task.ID)
		return CommandInfo{}, Fail("E_PROCESS", "启动或接管命令进程失败")
	}
	m.jobs[id] = job
	initial := job.info
	m.mu.Unlock()
	stop()
	// 取消监视与 Wait 分开运行，避免把进程退出误判为取消；系统句柄只由收尾协程释放。
	go func() {
		select {
		case <-background.Done():
			_ = kill()
		case <-job.done:
		}
	}()
	go func() {
		err := cmd.Wait()
		close(job.done)
		cleanup()
		_ = m.controller.EndTask(ctx, task.ID)
		m.mu.Lock()
		defer m.mu.Unlock()
		if cmd.ProcessState != nil {
			job.info.ExitCode = cmd.ProcessState.ExitCode()
		}
		if errors.Is(background.Err(), context.DeadlineExceeded) {
			job.info.State = "timed_out"
			job.info.Error = &Error{Code: "E_TIMEOUT", Message: "命令执行超时"}
		} else if background.Err() != nil {
			job.info.State = "cancelled"
			job.info.Error = &Error{Code: "E_CANCELLED", Message: "命令已取消"}
		} else if err != nil {
			job.info.State = "failed"
			job.info.Error = &Error{Code: "E_COMMAND_FAILED", Message: "命令以非零状态退出或输出管道收尾失败"}
		} else {
			job.info.State = "completed"
		}
		cancel()
	}()
	return initial, nil
}

// Read 按字节游标读取当前窗口任务的新输出；任务结束后仍可读取，直到显式 Remove 或窗口销毁。
func (m *CommandManager) Read(ctx context.Context, id string, stdoutOffset, stderrOffset int) (CommandOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.jobs[id]
	if job == nil || job.owner != resourceOwner(ctx) {
		return CommandOutput{}, Fail("E_PERMISSION_DENIED", "命令任务不存在或不属于当前页面")
	}
	if stdoutOffset < 0 || stderrOffset < 0 || stdoutOffset > len(job.stdout) || stderrOffset > len(job.stderr) {
		return CommandOutput{}, Fail("E_INVALID_ARGUMENT", "输出游标无效")
	}
	return CommandOutput{Job: job.info, Stdout: append([]byte(nil), job.stdout[stdoutOffset:]...), Stderr: append([]byte(nil), job.stderr[stderrOffset:]...), StdoutOffset: len(job.stdout), StderrOffset: len(job.stderr)}, nil
}

// Cancel 请求终止当前页面的任务，重复取消已经结束的任务安全返回；终态可通过 Read 查询。
func (m *CommandManager) Cancel(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.jobs[id]
	if job == nil || job.owner != resourceOwner(ctx) {
		return Fail("E_PERMISSION_DENIED", "命令任务不存在或不属于当前页面")
	}
	if job.info.State == "running" {
		job.cancel()
	}
	return nil
}

// Remove 清理已经结束的任务及输出，不删除命令在磁盘上创建的业务文件。
func (m *CommandManager) Remove(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.jobs[id]
	if job == nil {
		return nil
	}
	if job.owner != resourceOwner(ctx) {
		return Fail("E_PERMISSION_DENIED", "不能清理其他页面的命令")
	}
	if job.info.State == "running" {
		return Fail("E_BUSY", "请先取消并等待命令结束")
	}
	delete(m.jobs, id)
	return nil
}

// CloseWindow 取消并移除关闭窗口的任务，防止同名窗口重新打开后读取之前的命令输出。
func (m *CommandManager) CloseWindow(window string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, job := range m.jobs {
		if job.window == window {
			job.cancel()
			delete(m.jobs, id)
		}
	}
}

// Shutdown 阻止新命令并取消所有运行进程，由各自 Wait 协程关闭系统句柄和释放退出保护。
func (m *CommandManager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for _, job := range m.jobs {
		job.cancel()
	}
}
