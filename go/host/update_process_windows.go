//go:build windows

package host

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/windows"
)

// acquireDataLock 防止多实例在同一数据目录恢复或改写 KV；锁随句柄关闭或进程退出自动释放。
func acquireDataLock(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, ".data.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	var overlapped windows.Overlapped
	if err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped); err != nil {
		_ = file.Close()
		return nil, Fail("E_DATA_IN_USE", "此数据目录已由其他进程使用，请关闭原进程或指定不同 HYBRID_DATA_DIR")
	}
	var once sync.Once
	return func() { once.Do(func() { _ = file.Close() }) }, nil
}

// hideWorker 仅隐藏维护工作进程；更新后的主程序仍按正常窗口方式显示。
func hideWorker(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}

// waitUpdateParent 验证父进程映像后等待其退出，避免 PID 复用误判并限制等待时间。
func waitUpdateParent(pid int, target, ready string) error {
	handle, e := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if e != nil {
		return Fail("E_UPDATE_PARENT", "无法验证更新父进程")
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if e = windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); e != nil || !strings.EqualFold(filepath.Clean(windows.UTF16ToString(buffer[:size])), filepath.Clean(target)) {
		return Fail("E_UPDATE_PARENT", "父进程路径不匹配")
	}
	if e = jsonWrite(ready, map[string]int{"pid": pid}); e != nil {
		return e
	}
	status, e := windows.WaitForSingleObject(handle, 45000)
	if e != nil || status != windows.WAIT_OBJECT_0 {
		return Fail("E_UPDATE_PARENT", "等待原程序退出超时")
	}
	return nil
}
