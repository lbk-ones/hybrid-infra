//go:build !windows

package host

// revealPath 在尚未适配的平台返回明确错误，不以执行任意命令作为替代。
func revealPath(path string) error { return Fail("E_UNSUPPORTED", "此平台尚未适配文件定位") }

// clipboardImage 在尚未适配的平台返回明确错误；文本剪贴板仍通过 Wails 可用。
func clipboardImage(data []byte, write bool, owner uintptr) ([]byte, error) {
	return nil, Fail("E_UNSUPPORTED", "此平台尚未适配图像剪贴板")
}

// nativeCredential 拒绝在未适配系统上退化为明文存储。
func nativeCredential(target string, value []byte, operation string) ([]byte, error) {
	return nil, Fail("E_UNSUPPORTED", "此平台尚未适配系统凭据库")
}

// registerProtocol 提示非 Windows 平台需由安装包声明协议入口。
func registerProtocol(appID, scheme string, enable bool) error {
	return Fail("E_UNSUPPORTED", "此平台请通过安装包声明协议入口")
}
