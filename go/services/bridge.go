package services

import (
	"context"
	"encoding/json"
	"hybrid-infra/go/host"
)

// CacheService 暴露有 TTL 的进程共享缓存；宿主管理器校验 cache 与 cache.global 权限并隔离来源。
type CacheService struct{ cache *host.SharedCache }

// Get 返回命中状态、JSON 值、过期时间和修订号。
func (s *CacheService) Get(ctx context.Context, namespace, key string, global bool) (host.CacheValue, error) {
	return s.cache.Get(ctx, namespace, key, global)
}

// Set 保存 JSON 数据，ttlMs=0 表示进程内不过期。
func (s *CacheService) Set(ctx context.Context, namespace, key string, value json.RawMessage, ttlMs int64, global bool) (host.CacheValue, error) {
	return s.cache.Set(ctx, namespace, key, value, ttlMs, global, false, 0)
}

// CompareAndSet 仅在修订号与读取结果一致时更新，防止多个窗口互相覆盖；revision=0 要求键不存在。
func (s *CacheService) CompareAndSet(ctx context.Context, namespace, key string, value json.RawMessage, ttlMs int64, global bool, revision uint64) (host.CacheValue, error) {
	return s.cache.Set(ctx, namespace, key, value, ttlMs, global, true, revision)
}

// Delete 删除当前共享空间中的单个键。
func (s *CacheService) Delete(ctx context.Context, namespace, key string, global bool) error {
	return s.cache.Delete(ctx, namespace, key, global)
}

// Clear 只清理指定命名空间，不影响其他业务空间。
func (s *CacheService) Clear(ctx context.Context, namespace string, global bool) error {
	return s.cache.Clear(ctx, namespace, global)
}

// EventService 暴露可在 HTTP 上工作的事件会话，所有回调函数均由前端 SDK 保存，Go 只处理 JSON 消息。
type EventService struct{ bus *host.EventBus }

// Open 为当前来源和窗口创建事件会话。
func (s *EventService) Open(ctx context.Context) (string, error) { return s.bus.Open(ctx) }

// Subscribe 登记事件监听或请求处理器，global 需要额外权限。
func (s *EventService) Subscribe(ctx context.Context, session, topic, kind string, global bool) (string, error) {
	return s.bus.Subscribe(ctx, session, topic, kind, global)
}

// Poll 使用一条长轮询批量接收该会话的所有监听消息，最多等待 15 秒。
func (s *EventService) Poll(ctx context.Context, session string) ([]host.BusMessage, error) {
	return s.bus.Poll(ctx, session)
}

// Unsubscribe 移除指定监听，其他同主题监听不受影响。
func (s *EventService) Unsubscribe(ctx context.Context, session, listener string) error {
	if err := host.Require(ctx, "events"); err != nil {
		return err
	}
	return s.bus.Unsubscribe(ctx, session, listener)
}

// Emit 在同来源或显式全局空间中发送事件，返回匹配监听数，不等待回调完成。
func (s *EventService) Emit(ctx context.Context, topic string, data json.RawMessage, targetWindow string, global bool) (int, error) {
	return s.bus.Emit(ctx, topic, data, targetWindow, global)
}

// Request 等待唯一处理器的响应，超时或取消会释放宿主请求资源。
func (s *EventService) Request(ctx context.Context, topic string, data json.RawMessage, targetWindow string, global bool, timeoutMs int) (json.RawMessage, error) {
	return s.bus.Request(ctx, topic, data, targetWindow, global, timeoutMs)
}

// Respond 提交指定请求的结果，宿主验证处理器和会话身份以防伪造响应。
func (s *EventService) Respond(ctx context.Context, session, listener, request string, data json.RawMessage, failed bool) error {
	if err := host.Require(ctx, "events"); err != nil {
		return err
	}
	return s.bus.Respond(ctx, session, listener, request, data, failed)
}

// Close 关闭当前页面的事件会话，页面卸载时尽力释放。
func (s *EventService) Close(ctx context.Context, session string) error {
	if err := host.Require(ctx, "events"); err != nil {
		return err
	}
	return s.bus.Close(ctx, session)
}

// ShellService 是显式授权的命令入口，独立于普通窗口和文件能力。
type ShellService struct{ commands *host.CommandManager }

// Start 启动白名单程序或额外授权的完整脚本，返回后台任务编号。
func (s *ShellService) Start(ctx context.Context, options host.CommandOptions) (host.CommandInfo, error) {
	return s.commands.Start(ctx, options)
}

// Read 根据字节游标读取任务的新输出和退出状态。
func (s *ShellService) Read(ctx context.Context, id string, stdoutOffset, stderrOffset int) (host.CommandOutput, error) {
	if err := host.Require(ctx, "shell"); err != nil {
		return host.CommandOutput{}, err
	}
	return s.commands.Read(ctx, id, stdoutOffset, stderrOffset)
}

// Cancel 取消属于当前页面的命令及受管理后代进程。
func (s *ShellService) Cancel(ctx context.Context, id string) error {
	if err := host.Require(ctx, "shell"); err != nil {
		return err
	}
	return s.commands.Cancel(ctx, id)
}

// Remove 清理结束命令的输出记录，不删除业务文件。
func (s *ShellService) Remove(ctx context.Context, id string) error {
	if err := host.Require(ctx, "shell"); err != nil {
		return err
	}
	return s.commands.Remove(ctx, id)
}
