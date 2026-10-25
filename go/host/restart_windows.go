//go:build windows

package host

import (
	"golang.org/x/sys/windows"
	"os/exec"
	"path/filepath"
	"strings"
)

// configureRestartWorker 隐藏并分离助手；无法脱离父进程作业对象时 Start 会失败，当前应用不会退出。
func configureRestartWorker(command *exec.Cmd) {
	hideWorker(command)
	command.SysProcAttr.CreationFlags |= windows.CREATE_BREAKAWAY_FROM_JOB | windows.CREATE_NEW_PROCESS_GROUP
}

// sameExecutable 按 Windows 路径大小写规则比较，不允许票据指定其他应用。
func sameExecutable(a, b string) bool {
	return filepath.IsAbs(b) && strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// waitRestartParent 持有真实父进程句柄，验证映像路径后等待退出，防止 PID 复用造成误判。
func waitRestartParent(pid int, target, ready, token string) error {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return Fail("E_RESTART_PARENT", "无法验证父进程")
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size) != nil || !sameExecutable(windows.UTF16ToString(buffer[:size]), target) {
		return Fail("E_RESTART_PARENT", "父进程路径不匹配")
	}
	if err = atomicWrite(ready, []byte(token)); err != nil {
		return err
	}
	status, err := windows.WaitForSingleObject(handle, 45000)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		return Fail("E_RESTART_PARENT", "等待原程序退出超时")
	}
	return nil
}
