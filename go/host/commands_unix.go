//go:build !windows

package host

import (
	"os/exec"
	"syscall"
)

// scriptCommand 使用固定系统 /bin/sh 执行已显式授权的脚本，不从业务工作目录查找解释器。
func scriptCommand(script string) *exec.Cmd { return exec.Command("/bin/sh", "-c", script) }

// prepareCommand 为命令创建独立 Unix 进程组，取消时向整个组发送 SIGKILL；自行脱离进程组的程序不属于此管理边界。
func prepareCommand(cmd *exec.Cmd) (func() error, func() error, func(), error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	kill := func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return kill, func() error { return nil }, func() { _ = kill() }, nil
}
