package host

import (
	"encoding/json"
	"errors"
)

// Error 是宿主对外使用的业务错误：Code 供程序判断，Message 提供易懂的中文原因。
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error 返回包含错误码和中文说明的文本，供 Go 日志及标准错误接口使用。
func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Fail 创建统一的宿主业务错误，让服务能以错误码和可读说明向前端报告失败原因。
func Fail(code, message string) error { return &Error{code, message} }

// MarshalError 把业务错误转换为前端可读取的 JSON；未知错误使用统一提示，避免把内部实现细节直接暴露给页面。
func MarshalError(err error) []byte {
	var e *Error
	if !errors.As(err, &e) {
		e = &Error{"E_INTERNAL", "宿主操作失败，请查看日志"}
	}
	data, _ := json.Marshal(e)
	return data
}
