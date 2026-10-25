//go:build !windows

package host

import "os/exec"

// hideWorker 在未开放自动安装的平台不设置进程参数。
func hideWorker(command *exec.Cmd) {}

// waitUpdateParent 拒绝尚未验证的平台，避免误执行二进制替换。
func waitUpdateParent(pid int, target, ready string) error {
	return Fail("E_UNSUPPORTED", "当前平台尚未支持自动安装")
}
