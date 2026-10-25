//go:build !windows

package host

// devicePrinters 在未适配系统明确返回不支持，串口和 TCP 标签打印仍可使用。
func devicePrinters() ([]string, error) {
	return nil, Fail("E_UNSUPPORTED", "当前平台尚未接入系统 RAW 打印")
}

// devicePrintRaw 不在未适配系统上执行 Shell 打印命令或假装提交成功。
func devicePrintRaw(printer, title string, data []byte) (uint32, error) {
	return 0, Fail("E_UNSUPPORTED", "当前平台尚未接入系统 RAW 打印")
}
