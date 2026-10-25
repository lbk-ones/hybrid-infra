//go:build !windows

package host

import (
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// configureRestartWorker 使用普通子进程，主进程退出后由系统收养，不传入 Shell 命令。
func configureRestartWorker(command *exec.Cmd) {}

// sameExecutable 比较规范绝对路径，重启只能执行当前应用本身。
func sameExecutable(a, b string) bool {
	return filepath.IsAbs(b) && filepath.Clean(a) == filepath.Clean(b)
}

// waitRestartParent 使用实际父子关系等待进程被系统重新收养，不根据可复用的 PID 猜测是否退出。
func waitRestartParent(pid int, target, ready, token string) error {
	if os.Getppid() != pid {
		return Fail("E_RESTART_PARENT", "父进程关系不匹配")
	}
	if err := atomicWrite(ready, []byte(token)); err != nil {
		return err
	}
	for deadline := time.Now().Add(45 * time.Second); time.Now().Before(deadline); {
		if os.Getppid() != pid {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return Fail("E_RESTART_PARENT", "等待原程序退出超时")
}
