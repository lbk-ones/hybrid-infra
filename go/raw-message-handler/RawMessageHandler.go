package raw_message_handler

import (
	"fmt"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type RawMessageHandler struct {
}

// NewRawMessageHandler 创建历史原始消息日志处理器；当前 main.go 的宿主导航消息由 host.Controller.NativeMessage 接管。
func NewRawMessageHandler() *RawMessageHandler {
	return &RawMessageHandler{}
}

// HandleMessage 输出收到的原始消息和原生来源元数据，供旧代码排查消息通道；该方法只记录消息，不执行权限授权。
func (handler *RawMessageHandler) HandleMessage(window application.Window, message string, originInfo *application.OriginInfo) {
	println(fmt.Sprintf("Raw message received from Window %s with message: %s, origin %s, topOrigin %s, isMainFrame %t", window.Name(), message, originInfo.Origin, originInfo.TopOrigin, originInfo.IsMainFrame))
}
