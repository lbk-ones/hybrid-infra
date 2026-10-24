package services

import (
	"context"
	"fmt"
	"math"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"hybrid-infra/go/host"
	"hybrid-infra/go/utils"
	"hybrid-infra/go/utils/cross"
)

var notificationSequence atomic.Uint64

// NotifyReq 是轻量通知参数；默认右上角、四秒关闭且可拖拽，text 始终按纯文本显示。
type NotifyReq struct {
	Text            string  `json:"text"`
	Place           string  `json:"place"`
	DurationSeconds float64 `json:"durationSeconds"`
	Draggable       *bool   `json:"draggable,omitempty"`
}
type noticeEntry struct {
	id, origin, owner, place string
	window                   application.Window
	area, expected           application.Rect
	duration                 time.Duration
	timer                    *time.Timer
	started, detached        bool
}

// normalizeNotify 检查通知参数并填充默认值；不允许无限期通知，展示时长最多一小时。
func normalizeNotify(req NotifyReq) (NotifyReq, error) {
	if len(req.Text) == 0 || len(req.Text) > 4096 {
		return req, host.Fail("E_INVALID_ARGUMENT", "通知文本应为 1–4096 字节")
	}
	if req.Place == "" {
		req.Place = utils.PlaceTopRight
	}
	switch req.Place {
	case utils.PlaceTopLeft, utils.PlaceTopRight, utils.PlaceBottomLeft, utils.PlaceBottomRight, utils.PlaceCenter, "topCenter", "bottomCenter":
	default:
		return req, host.Fail("E_INVALID_ARGUMENT", "通知位置不受支持")
	}
	if req.DurationSeconds == 0 {
		req.DurationSeconds = 4
	}
	if math.IsNaN(req.DurationSeconds) || math.IsInf(req.DurationSeconds, 0) || req.DurationSeconds < 0.1 || req.DurationSeconds > 3600 {
		return req, host.Fail("E_INVALID_ARGUMENT", "通知时长应为 0.1–3600 秒，省略或 0 时为四秒")
	}
	return req, nil
}

// notificationBounds 计算同位置通知的逻辑坐标；上方向下、下方向上，竖向满后横向另起一列，始终限制在工作区内。
func notificationBounds(area application.Rect, place string, count int) ([]application.Rect, error) {
	margin, gap := 16, 12
	if area.Width < 100 || area.Height < 60 {
		return nil, host.Fail("E_DISPLAY", "显示器工作区过小")
	}
	width, height := min(360, area.Width-2*margin), min(116, area.Height-2*margin)
	rows := max(1, (area.Height-2*margin+gap)/(height+gap))
	cols := max(1, (area.Width-2*margin+gap)/(width+gap))
	if count > rows*cols {
		return nil, host.Fail("E_LIMIT_EXCEEDED", "此位置的通知已占满工作区，请等待关闭后重试")
	}
	result := make([]application.Rect, count)
	usedCols := (count + rows - 1) / rows
	for i := range count {
		col, row := i/rows, i%rows
		x := area.X + margin + col*(width+gap)
		y := area.Y + margin + row*(height+gap)
		switch place {
		case utils.PlaceTopRight, utils.PlaceBottomRight:
			x = area.X + area.Width - margin - width - col*(width+gap)
		case "topCenter", "bottomCenter", utils.PlaceCenter:
			x = area.X + (area.Width-(usedCols*width+(usedCols-1)*gap))/2 + col*(width+gap)
		}
		switch place {
		case utils.PlaceBottomLeft, utils.PlaceBottomRight, "bottomCenter":
			y = area.Y + area.Height - margin - height - row*(height+gap)
		case utils.PlaceCenter:
			usedRows := min(rows, count-col*rows)
			y = area.Y + (area.Height-(usedRows*height+(usedRows-1)*gap))/2 + row*(height+gap)
		}
		result[i] = application.Rect{X: x, Y: y, Width: width, Height: height}
	}
	return result, nil
}

// Notify 创建原生通知并返回编号；同一工作区与位置的通知顺序堆叠，加载就绪后再开始展示时长计时。
func (s *UiService) Notify(ctx context.Context, req NotifyReq) (string, error) {
	if err := host.Require(ctx, "ui"); err != nil {
		return "", err
	}
	req, err := normalizeNotify(req)
	if err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", host.Fail("E_CANCELLED", "通知调用已取消")
	}
	box, err := cross.GetScreenSize()
	if err != nil {
		return "", host.Fail("E_DISPLAY", "无法获取通知显示工作区")
	}
	area := application.Rect{X: int(box.Left), Y: int(box.Top), Width: int(box.Width), Height: int(box.Height)}
	origin, owner := host.CallerIdentity(ctx)
	if owner == "" {
		return "", host.Fail("E_WINDOW_NOT_FOUND", "通知必须关联桌面窗口")
	}
	s.noticeLayout.Lock()
	defer s.noticeLayout.Unlock()
	s.noticeMu.Lock()
	count := 1
	if len(s.notices) >= 24 {
		s.noticeMu.Unlock()
		return "", host.Fail("E_LIMIT_EXCEEDED", "最多同时显示 24 条通知")
	}
	for _, e := range s.notices {
		if !e.detached && e.area == area && e.place == req.Place {
			count++
		}
	}
	bounds, err := notificationBounds(area, req.Place, count)
	if err != nil {
		s.noticeMu.Unlock()
		return "", err
	}
	id := fmt.Sprintf("hybrid-infra-notify-%d", notificationSequence.Add(1))
	e := &noticeEntry{id: id, origin: origin, owner: owner, place: req.Place, area: area, expected: bounds[count-1], duration: time.Duration(req.DurationSeconds * float64(time.Second))}
	s.notices = append(s.notices, e)
	s.noticeMu.Unlock()
	options := dialogOptions("/Notify.html", req.Place, e.expected.Width, e.expected.Height)
	options.Name = id
	options.Title = "应用通知"
	w := s.controller.CreateDialog(ctx, options)
	s.noticeMu.Lock()
	active := s.noticeExistsLocked(id)
	if active {
		e.window = w
	}
	s.noticeMu.Unlock()
	if !active {
		w.Close()
		return "", host.Fail("E_CANCELLED", "所属窗口已关闭")
	}
	// 关闭回调只改内存并异步重排，不能在原生 UI 线程上等待正在调用原生接口的布局锁。
	w.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) { s.removeNotice(id) })
	w.OnWindowEvent(events.Common.WindowDidMove, func(*application.WindowEvent) { s.noticeMoved(id, w.Bounds()) })
	s.layoutNoticesLocked()
	s.noticeMu.Lock()
	// 就绪消息缺失时仍关闭窗口，避免资源加载失败留下无法操作的透明通知。
	if s.noticeExistsLocked(id) {
		e.timer = time.AfterFunc(10*time.Second, func() { w.Close() })
	}
	s.noticeMu.Unlock()
	if ctx.Err() != nil {
		w.Close()
		return "", host.Fail("E_CANCELLED", "通知调用已取消")
	}
	w.ExecJS(dialogScript(dialogContent{Body: req.Text, Notification: true, ID: id, DurationSeconds: req.DurationSeconds, Draggable: draggableDefault(req.Draggable)}))
	w.Show()
	return id, nil
}

// noticeExistsLocked 判断通知是否仍在登记表中，调用者必须持有通知锁。
func (s *UiService) noticeExistsLocked(id string) bool {
	for _, e := range s.notices {
		if e.id == id {
			return true
		}
	}
	return false
}

// layoutNoticesLocked 在持有布局锁时重排未被用户拖离的通知；计算期间只持内存锁，原生窗口调用在解锁后执行。
func (s *UiService) layoutNoticesLocked() {
	type group struct {
		area  application.Rect
		place string
	}
	type move struct {
		window application.Window
		bounds application.Rect
	}
	s.noticeMu.Lock()
	groups := map[group][]*noticeEntry{}
	for _, e := range s.notices {
		if !e.detached {
			key := group{e.area, e.place}
			groups[key] = append(groups[key], e)
		}
	}
	moves := []move{}
	for key, entries := range groups {
		bounds, err := notificationBounds(key.area, key.place, len(entries))
		if err != nil {
			continue
		}
		for i, e := range entries {
			e.expected = bounds[i]
			if e.window != nil {
				moves = append(moves, move{e.window, bounds[i]})
			}
		}
	}
	s.noticeMu.Unlock()
	for _, m := range moves {
		m.window.SetBounds(m.bounds)
	}
}

// reflowNotices 串行重新排列通知，关闭通知后回收空位，避免并发创建与关闭造成位置互相覆盖。
func (s *UiService) reflowNotices() {
	s.noticeLayout.Lock()
	defer s.noticeLayout.Unlock()
	s.layoutNoticesLocked()
}

// removeNotice 撤销关闭通知的计时器和登记；未找到时幂等返回，随后异步收拢剩余通知。
func (s *UiService) removeNotice(id string) {
	s.noticeMu.Lock()
	for i, e := range s.notices {
		if e.id == id {
			if e.timer != nil {
				e.timer.Stop()
			}
			s.notices = append(s.notices[:i], s.notices[i+1:]...)
			break
		}
	}
	s.noticeMu.Unlock()
	go s.reflowNotices()
}

// noticeMoved 检测用户手动拖拽；拖离队列的通知保留剩余寿命，但不再被后续通知强制拉回。
func (s *UiService) noticeMoved(id string, bounds application.Rect) {
	moved := false
	s.noticeMu.Lock()
	for _, e := range s.notices {
		if e.id == id && e.started && !e.detached && (bounds.X != e.expected.X || bounds.Y != e.expected.Y) {
			e.detached = true
			moved = true
			break
		}
	}
	s.noticeMu.Unlock()
	if moved {
		go s.reflowNotices()
	}
}

// NotificationReady 只允许通知窗口自己报告就绪，停止加载兜底计时器并开始真实展示倒计时，重复报告不会延长寿命。
func (s *UiService) NotificationReady(ctx context.Context, id string) error {
	if err := host.Require(ctx, "ui"); err != nil {
		return err
	}
	_, window := host.CallerIdentity(ctx)
	if window != id {
		return host.Fail("E_PERMISSION_DENIED", "只有通知自身可以报告就绪")
	}
	s.noticeMu.Lock()
	defer s.noticeMu.Unlock()
	for _, e := range s.notices {
		if e.id == id {
			if !e.started {
				e.started = true
				if e.timer != nil {
					e.timer.Stop()
				}
				e.timer = time.AfterFunc(e.duration, func() { e.window.Close() })
			}
			return nil
		}
	}
	return host.Fail("E_NOT_FOUND", "通知已关闭")
}

// CloseNotification 允许通知自身或同来源的创建窗口提前关闭通知，不能关闭其他业务窗口的通知。
func (s *UiService) CloseNotification(ctx context.Context, id string) error {
	if err := host.Require(ctx, "ui"); err != nil {
		return err
	}
	origin, window := host.CallerIdentity(ctx)
	s.noticeMu.Lock()
	var w application.Window
	for _, e := range s.notices {
		if e.id == id {
			if window != id && (e.origin != origin || e.owner != window) {
				s.noticeMu.Unlock()
				return host.Fail("E_PERMISSION_DENIED", "不能关闭其他窗口的通知")
			}
			w = e.window
			break
		}
	}
	s.noticeMu.Unlock()
	if w != nil {
		w.Close()
	}
	return nil
}

// closeOwnerNotifications 在来源窗口销毁时取消其全部通知，关闭操作放在独立协程中，避免阻塞原生销毁回调。
func (s *UiService) closeOwnerNotifications(owner string) {
	s.noticeMu.Lock()
	windows := []application.Window{}
	for i := len(s.notices) - 1; i >= 0; i-- {
		e := s.notices[i]
		if e.owner == owner {
			if e.timer != nil {
				e.timer.Stop()
			}
			if e.window != nil {
				windows = append(windows, e.window)
			}
			s.notices = append(s.notices[:i], s.notices[i+1:]...)
		}
	}
	s.noticeMu.Unlock()
	go func() {
		for _, w := range windows {
			w.Close()
		}
		s.reflowNotices()
	}()
}
