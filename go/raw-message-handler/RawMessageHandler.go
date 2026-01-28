package raw_message_handler

import (
	"fmt"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type RawMessageHandler struct {
}

func NewRawMessageHandler() *RawMessageHandler {
	return &RawMessageHandler{}
}

func (handler *RawMessageHandler) HandleMessage(window application.Window, message string, originInfo *application.OriginInfo) {
	println(fmt.Sprintf("Raw message received from Window %s with message: %s, origin %s, topOrigin %s, isMainFrame %t", window.Name(), message, originInfo.Origin, originInfo.TopOrigin, originInfo.IsMainFrame))
}
