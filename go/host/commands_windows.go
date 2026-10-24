//go:build windows

package host

import (
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"
)

// scriptCommand 使用系统 Windows PowerShell 执行显式授权的完整脚本，关闭交互和用户 profile，并统一标准输出为 UTF-8。
func scriptCommand(script string) *exec.Cmd {
	return exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false);\n"+script)
}

// prepareCommand 创建 Windows Job Object，进程启动后纳入作业；取消和句柄关闭都会终止作业内后代进程。
// 作业附加发生在 Start 之后，不能作为针对恶意程序主动逃逸的安全沙箱；输出等待也设有独立上限。
func prepareCommand(cmd *exec.Cmd) (func() error, func() error, func(), error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return nil, nil, nil, err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	var mu sync.Mutex
	closed := false
	cleanup := func() {
		mu.Lock()
		defer mu.Unlock()
		if !closed {
			closed = true
			_ = windows.CloseHandle(job)
		}
	}
	kill := func() error {
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return nil
		}
		err := windows.TerminateJobObject(job, 1)
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return err
	}
	started := func() error {
		process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
		if err != nil {
			return err
		}
		defer windows.CloseHandle(process)
		return windows.AssignProcessToJobObject(job, process)
	}
	return kill, started, cleanup, nil
}
