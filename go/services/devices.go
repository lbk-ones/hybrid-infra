package services

import (
	"context"
	"hybrid-infra/go/host"
)

// DeviceService 将设备通信作为专用业务能力暴露，每次操作继续检查连接所属窗口与实际类型权限。
type DeviceService struct{ manager *host.DeviceManager }

// CancelRead 停止本窗口当前接收等待，不关闭设备或清空数据，避免重新监听时旧读取占用连接。
func (s *DeviceService) CancelRead(ctx context.Context, id string) error {
	return s.manager.CancelRead(ctx, id)
}

// ListPorts 枚举串口，不打开设备或主动发送指令。
func (s *DeviceService) ListPorts(ctx context.Context) ([]string, error) {
	return s.manager.ListPorts(ctx)
}

// OpenSerial 按指定波特率和校验位打开当前存在的串口。
func (s *DeviceService) OpenSerial(ctx context.Context, options host.SerialOptions) (host.DeviceConnection, error) {
	return s.manager.OpenSerial(ctx, options)
}

// OpenTCP 连接启动白名单中的 IP:端口，供网络标签打印机或其他 TCP 设备使用。
func (s *DeviceService) OpenTCP(ctx context.Context, endpoint string) (host.DeviceConnection, error) {
	return s.manager.OpenTCP(ctx, endpoint)
}

// ListConnections 查看本窗口的连接状态，不跨窗口共享句柄。
func (s *DeviceService) ListConnections(ctx context.Context) ([]host.DeviceConnection, error) {
	return s.manager.ListConnections(ctx)
}

// Read 等待原始字节与终态；接收回调由 SDK 轮询后在前端调用。
func (s *DeviceService) Read(ctx context.Context, id string, timeoutMs int) (host.DeviceRead, error) {
	return s.manager.Read(ctx, id, timeoutMs)
}

// Write 发送 Base64 二进制内容，超时或取消不能撤回已发往设备的数据。
func (s *DeviceService) Write(ctx context.Context, id string, data []byte) (int, error) {
	return s.manager.Write(ctx, id, data)
}

// Close 关闭设备，可同时移除历史记录，重复关闭保留的终态安全。
func (s *DeviceService) Close(ctx context.Context, id string, remove bool) error {
	return s.manager.Close(ctx, id, remove)
}

// ListPrinters 列出系统已经安装的打印机，不进行真实打印。
func (s *DeviceService) ListPrinters(ctx context.Context) ([]string, error) {
	return s.manager.ListPrinters(ctx)
}

// PrintRaw 提交 ZPL、TSPL、ESC/POS 等原始字节，返回后台作业 ID，不表示纸张已经打印完成。
func (s *DeviceService) PrintRaw(ctx context.Context, printer, title string, data []byte) (uint32, error) {
	return s.manager.PrintRaw(ctx, printer, title, data)
}
