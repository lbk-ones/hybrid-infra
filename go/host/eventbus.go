package host

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"time"
)

var businessTopic = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,119}$`)

// BusMessage 是跨进程传递的纯 JSON 消息；listenerId 用于在前端找到本地函数，函数本身不会传入 Go。
type BusMessage struct {
	Kind       string          `json:"kind"`
	Topic      string          `json:"topic"`
	ListenerID string          `json:"listenerId"`
	RequestID  string          `json:"requestId,omitempty"`
	Data       json.RawMessage `json:"data"`
	Sender     string          `json:"sender"`
}
type busListener struct{ id, topic, scope, kind string }
type busSession struct {
	owner, origin, window string
	listeners             map[string]busListener
	queue                 []BusMessage
	wake                  chan struct{}
	lastSeen              time.Time
	polling               bool
	overflow              bool
}
type busAnswer struct {
	data json.RawMessage
	err  error
}
type busRequest struct {
	session, listener string
	requester         string
	answer            chan busAnswer
}

// EventBus 管理按来源隔离的业务事件，使用单会话长轮询传送回调消息，不依赖原生广播，也支持普通浏览器 HTTP 连接。
type EventBus struct {
	mu       sync.Mutex
	sessions map[string]*busSession
	requests map[string]busRequest
	closed   bool
}

// NewEventBus 创建空事件总线；会话、队列、请求均有数量上限，窗口关闭或应用退出时由生命周期钩子清理。
func NewEventBus() *EventBus {
	return &EventBus{sessions: map[string]*busSession{}, requests: map[string]busRequest{}}
}

// signal 唤醒长轮询，不在持锁期间阻塞等待页面读取消息。
func signal(session *busSession) {
	select {
	case session.wake <- struct{}{}:
	default:
	}
}

// closeSession 在持锁状态下移除会话并让等待它返回结果的请求及时失败，避免超时前一直占用资源。
func (b *EventBus) closeSession(id string) {
	s := b.sessions[id]
	if s == nil {
		return
	}
	delete(b.sessions, id)
	signal(s)
	for requestID, p := range b.requests {
		if p.session == id {
			select {
			case p.answer <- busAnswer{err: Fail("E_HANDLER_GONE", "响应页面已离开")}:
			default:
			}
			delete(b.requests, requestID)
		}
	}
}

// pruneSessions 回收两分钟没有轮询或操作的会话，处理页面刷新、断网或卸载请求未送达的情况。
func (b *EventBus) pruneSessions() {
	for id, s := range b.sessions {
		if time.Since(s.lastSeen) > 2*time.Minute {
			b.closeSession(id)
		}
	}
}

// session 验证会话只能由原来源和真实窗口操作；必须在持有总线锁时调用。
func (b *EventBus) session(ctx context.Context, id string) (*busSession, error) {
	s := b.sessions[id]
	if s == nil || s.owner != resourceOwner(ctx) {
		return nil, Fail("E_SESSION_EXPIRED", "事件会话已失效，请重新订阅")
	}
	s.lastSeen = time.Now()
	return s, nil
}

// Open 创建当前页面的事件会话；原生窗口的新会话替换旧会话，防止刷新后的旧处理器造成重复响应。
// 无原生窗口身份的普通浏览器同来源最多 8 个会话，应用总计最多 128 个，每会话只需一个长轮询连接。
func (b *EventBus) Open(ctx context.Context) (string, error) {
	if err := Require(ctx, "events"); err != nil {
		return "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneSessions()
	if b.closed {
		return "", Fail("E_SHUTTING_DOWN", "事件总线已关闭")
	}
	p, _ := ctx.Value(principalKey{}).(Principal)
	if p.WindowName != "" {
		for id, s := range b.sessions {
			if s.window == p.WindowName {
				b.closeSession(id)
			}
		}
	}
	count := 0
	for _, s := range b.sessions {
		if s.owner == resourceOwner(ctx) {
			count++
		}
	}
	if count >= 8 || len(b.sessions) >= 128 {
		return "", Fail("E_LIMIT_EXCEEDED", "事件会话数量已达上限")
	}
	id, err := randomID()
	if err != nil {
		return "", err
	}
	b.sessions[id] = &busSession{owner: resourceOwner(ctx), origin: p.Origin, window: p.WindowName, listeners: map[string]busListener{}, wake: make(chan struct{}, 1), lastSeen: time.Now()}
	return id, nil
}

// eventTopic 限制业务主题格式并保留原生内部名称，避免业务把内置事件当成自己的消息通道。
func eventTopic(topic string) error {
	if !businessTopic.MatchString(topic) || strings.HasPrefix(topic, "wails:") || strings.HasPrefix(topic, "hybrid:") {
		return Fail("E_INVALID_ARGUMENT", "事件名称无效，不能使用 wails: 或 hybrid: 前缀")
	}
	return nil
}

// Subscribe 登记事件或请求处理器；同一个会话不能为相同主题和空间重复登记请求处理器。
func (b *EventBus) Subscribe(ctx context.Context, session, topic, kind string, global bool) (string, error) {
	scope, err := sharedScope(ctx, "events", global)
	if err != nil {
		return "", err
	}
	if err = eventTopic(topic); err != nil {
		return "", err
	}
	if kind != "event" && kind != "handler" {
		return "", Fail("E_INVALID_ARGUMENT", "监听类型必须是 event 或 handler")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s, err := b.session(ctx, session)
	if err != nil {
		return "", err
	}
	if len(s.listeners) >= 64 {
		return "", Fail("E_LIMIT_EXCEEDED", "每个事件会话最多 64 个监听")
	}
	for _, l := range s.listeners {
		if kind == "handler" && l.kind == kind && l.topic == topic && l.scope == scope {
			return "", Fail("E_CONFLICT", "当前页面已注册此请求处理器")
		}
	}
	id, err := randomID()
	if err != nil {
		return "", err
	}
	s.listeners[id] = busListener{id: id, topic: topic, kind: kind, scope: scope}
	return id, nil
}

// Unsubscribe 仅移除当前会话的指定监听；同时删除尚未投递的回调，并结束正在等待此处理器响应的请求。
func (b *EventBus) Unsubscribe(ctx context.Context, session, listener string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, err := b.session(ctx, session)
	if err != nil {
		return err
	}
	delete(s.listeners, listener)
	filtered := s.queue[:0]
	for _, m := range s.queue {
		if m.ListenerID != listener {
			filtered = append(filtered, m)
		}
	}
	s.queue = filtered
	for id, p := range b.requests {
		if p.session == session && p.listener == listener {
			select {
			case p.answer <- busAnswer{err: Fail("E_HANDLER_GONE", "请求处理器已取消监听")}:
			default:
			}
			delete(b.requests, id)
		}
	}
	return nil
}

// enqueue 为回调添加有界消息；队列满时明确报错，不悄悄丢失消息后声称发送成功。
func enqueue(s *busSession, m BusMessage) error {
	if len(s.queue) >= 128 || queueBytes(s.queue)+queueBytes([]BusMessage{m}) > 256*1024 {
		s.overflow = true
		signal(s)
		return Fail("E_BACKPRESSURE", "目标页面的事件队列已满")
	}
	s.queue = append(s.queue, m)
	signal(s)
	return nil
}

// queueBytes 估算队列有效载荷及路由元数据大小，每个会话最多 256 KiB，避免大量长消息占满宿主内存。
func queueBytes(messages []BusMessage) int {
	total := 0
	for _, m := range messages {
		total += len(m.Data) + len(m.Topic) + len(m.ListenerID) + len(m.RequestID) + len(m.Sender) + 128
	}
	return total
}

// eventData 检查可序列化的消息体，单条最多 64 KiB；JSON 中无法传递函数、DOM 节点或循环引用。
func eventData(data json.RawMessage) error {
	if !json.Valid(data) || len(data) > 65536 {
		return Fail("E_INVALID_ARGUMENT", "事件数据必须是最多 64 KiB 的 JSON")
	}
	return nil
}

// Emit 在指定来源空间或显式全局空间投递事件；targetWindow 为空表示发送到该空间所有已订阅窗口。
func (b *EventBus) Emit(ctx context.Context, topic string, data json.RawMessage, targetWindow string, global bool) (int, error) {
	scope, err := sharedScope(ctx, "events", global)
	if err != nil {
		return 0, err
	}
	if err = eventTopic(topic); err != nil {
		return 0, err
	}
	if err = eventData(data); err != nil {
		return 0, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneSessions()
	p, _ := ctx.Value(principalKey{}).(Principal)
	count := 0
	payload := append(json.RawMessage(nil), data...)
	// 先核对全部目标容量，再统一入队，避免前几个窗口已收到而后几个窗口失败的部分广播。
	targets := map[*busSession][]BusMessage{}
	for _, s := range b.sessions {
		if targetWindow != "" && s.window != targetWindow {
			continue
		}
		for _, l := range s.listeners {
			if l.kind == "event" && l.topic == topic && l.scope == scope {
				targets[s] = append(targets[s], BusMessage{Kind: "event", Topic: topic, ListenerID: l.id, Data: payload, Sender: p.WindowName})
			}
		}
	}
	for s, messages := range targets {
		if len(s.queue)+len(messages) > 128 || queueBytes(s.queue)+queueBytes(messages) > 256*1024 {
			return 0, Fail("E_BACKPRESSURE", "目标页面的事件队列已满")
		}
	}
	for s, messages := range targets {
		for _, m := range messages {
			_ = enqueue(s, m)
			count++
		}
	}
	return count, nil
}

// Poll 等待当前会话消息或最多 15 秒，立即取出积累的回调；一个会话只允许一个并行轮询，避免消息被重复消费者抢走。
func (b *EventBus) Poll(ctx context.Context, id string) ([]BusMessage, error) {
	if err := Require(ctx, "events"); err != nil {
		return nil, err
	}
	b.mu.Lock()
	s, err := b.session(ctx, id)
	if err != nil {
		b.mu.Unlock()
		return nil, err
	}
	if s.polling {
		b.mu.Unlock()
		return nil, Fail("E_BUSY", "该会话已有轮询")
	}
	s.polling = true
	b.mu.Unlock()
	defer func() { b.mu.Lock(); s.polling = false; b.mu.Unlock() }()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	for {
		b.mu.Lock()
		current, err := b.session(ctx, id)
		if err != nil {
			b.mu.Unlock()
			return nil, err
		}
		if current.overflow {
			current.overflow = false
			b.mu.Unlock()
			return nil, Fail("E_BACKPRESSURE", "事件接收队列已满")
		}
		if len(current.queue) > 0 {
			messages := current.queue
			current.queue = nil
			// 取出时复制共享的只读负载，避免 Go 调用者改写已投递给其他会话的数据。
			for i := range messages {
				messages[i].Data = append(json.RawMessage(nil), messages[i].Data...)
			}
			b.mu.Unlock()
			return messages, nil
		}
		b.mu.Unlock()
		select {
		case <-s.wake:
		case <-timer.C:
			return []BusMessage{}, nil
		case <-ctx.Done():
			return nil, Fail("E_CANCELLED", "事件轮询已取消")
		}
	}
}

// Request 将请求发给唯一匹配的处理器并等待其响应；多个处理器时要求显式目标，超时、取消或页面离开都会释放等待资源。
func (b *EventBus) Request(ctx context.Context, topic string, data json.RawMessage, targetWindow string, global bool, timeoutMs int) (json.RawMessage, error) {
	scope, err := sharedScope(ctx, "events", global)
	if err != nil {
		return nil, err
	}
	if err = eventTopic(topic); err != nil {
		return nil, err
	}
	if err = eventData(data); err != nil {
		return nil, err
	}
	if timeoutMs < 1 || timeoutMs > 30000 {
		return nil, Fail("E_INVALID_ARGUMENT", "请求超时必须介于 1–30000 毫秒")
	}
	b.mu.Lock()
	b.pruneSessions()
	var target *busSession
	var sessionID string
	var listener busListener
	matches := 0
	for id, s := range b.sessions {
		if targetWindow != "" && s.window != targetWindow {
			continue
		}
		for _, l := range s.listeners {
			if l.kind == "handler" && l.topic == topic && l.scope == scope {
				target = s
				sessionID = id
				listener = l
				matches++
			}
		}
	}
	if matches != 1 {
		b.mu.Unlock()
		if matches == 0 {
			return nil, Fail("E_NO_HANDLER", "没有匹配的请求处理器")
		}
		return nil, Fail("E_CONFLICT", "请求存在多个处理器，请指定唯一目标窗口")
	}
	if len(b.requests) >= 256 {
		b.mu.Unlock()
		return nil, Fail("E_LIMIT_EXCEEDED", "待响应请求过多")
	}
	id, err := randomID()
	if err != nil {
		b.mu.Unlock()
		return nil, err
	}
	answer := make(chan busAnswer, 1)
	p, _ := ctx.Value(principalKey{}).(Principal)
	if err = enqueue(target, BusMessage{Kind: "request", Topic: topic, ListenerID: listener.id, RequestID: id, Data: append(json.RawMessage(nil), data...), Sender: p.WindowName}); err != nil {
		b.mu.Unlock()
		return nil, err
	}
	b.requests[id] = busRequest{session: sessionID, listener: listener.id, requester: p.WindowName, answer: answer}
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.requests, id)
		if s := b.sessions[sessionID]; s != nil {
			queue := s.queue[:0]
			for _, m := range s.queue {
				if m.RequestID != id {
					queue = append(queue, m)
				}
			}
			s.queue = queue
		}
		b.mu.Unlock()
	}()
	timer := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
	defer timer.Stop()
	select {
	case result := <-answer:
		return result.data, result.err
	case <-timer.C:
		return nil, Fail("E_TIMEOUT", "事件请求等待响应超时")
	case <-ctx.Done():
		return nil, Fail("E_CANCELLED", "事件请求已取消")
	}
}

// Respond 只接受被选中的会话和处理器提交结果，不能仅凭 requestId 冒充其他窗口；业务异常统一返回 E_HANDLER_ERROR。
func (b *EventBus) Respond(ctx context.Context, session, listener, request string, data json.RawMessage, failed bool) error {
	if err := eventData(data); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := b.session(ctx, session); err != nil {
		return err
	}
	p, ok := b.requests[request]
	if !ok {
		return Fail("E_NOT_FOUND", "请求已完成、取消或超时")
	}
	if p.session != session || p.listener != listener {
		return Fail("E_PERMISSION_DENIED", "不能响应其他处理器的请求")
	}
	result := busAnswer{data: append(json.RawMessage(nil), data...)}
	if failed {
		result.err = Fail("E_HANDLER_ERROR", "目标页面处理请求失败")
	}
	select {
	case p.answer <- result:
		delete(b.requests, request)
		return nil
	default:
		return Fail("E_CONFLICT", "请求已经响应")
	}
}

// Close 主动关闭本页面的会话；页面卸载时尽力调用，网络未送达时仍会按超时清理。
func (b *EventBus) Close(ctx context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sessions[id] == nil {
		return nil
	}
	if _, err := b.session(ctx, id); err != nil {
		return err
	}
	b.closeSession(id)
	return nil
}

// CloseWindow 撤销关闭窗口所属的全部事件会话，让等待此窗口处理结果的请求及时结束。
func (b *EventBus) CloseWindow(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, p := range b.requests {
		if p.requester == name {
			select {
			case p.answer <- busAnswer{err: Fail("E_CANCELLED", "请求窗口已关闭")}:
			default:
			}
			delete(b.requests, id)
		}
	}
	for id, s := range b.sessions {
		if s.window == name {
			b.closeSession(id)
		}
	}
}

// Shutdown 停止接收新会话并唤醒全部长轮询；不会把等待中的请求留到进程退出之后。
func (b *EventBus) Shutdown() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	for id := range b.sessions {
		b.closeSession(id)
	}
}
