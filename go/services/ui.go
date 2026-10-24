package services

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"hybrid-infra/go/host"
	"hybrid-infra/go/utils"
)

var confirmNum atomic.Uint64

type UiService struct {
	controller   *host.Controller
	noticeMu     sync.Mutex
	noticeLayout sync.Mutex
	notices      []*noticeEntry
}

// NewUiService 创建弹框服务并关联统一宿主管理器，使弹框也遵循窗口登记和权限继承规则。
func NewUiService(controller *host.Controller) *UiService {
	s := &UiService{controller: controller}
	controller.OnResourceWindowClosed(s.closeOwnerNotifications)
	return s
}

type ConfirmReq struct {
	Draggable      *bool  `json:"draggable,omitempty"` // 未指定时允许拖拽；false 禁用网页拖拽区域。
	Title          string `json:"title"`
	Body           string `json:"body"`
	OkText         string `json:"okText"`
	CancelText     string `json:"cancelText"`
	Place          string `json:"place"`
	TimeoutSeconds int    `json:"timeoutSeconds"` // 等待秒数；非正数时使用默认的 300 秒。
}
type ShowReq struct {
	Draggable *bool  `json:"draggable,omitempty"`
	Body      string `json:"body"` // 仅接受可信 HTML，内容可以包含应用自己的事件处理器。
	Place     string `json:"place"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

// AlertReq 为消息弹框提供对象参数，旧四参数 Alert 保留兼容；Draggable 未指定时默认为 true。
type AlertReq struct {
	Title     string `json:"title"`
	Body      string `json:"body"`
	Destroy   int    `json:"destroy"`
	Place     string `json:"place"`
	Draggable *bool  `json:"draggable,omitempty"`
}

type dialogContent struct {
	Title           string  `json:"title"`
	Body            string  `json:"body"`
	OK              string  `json:"ok"`
	Cancel          string  `json:"cancel"`
	Event           string  `json:"event"`
	Destroy         int     `json:"destroy"`
	HTML            bool    `json:"html"`
	Draggable       bool    `json:"draggable"`
	Notification    bool    `json:"notification"`
	ID              string  `json:"id"`
	DurationSeconds float64 `json:"durationSeconds"`
}

// dialogScript 把弹框内容先序列化为 JSON，再生成等待 DOM 就绪后填入页面的脚本，避免直接拼接文本造成脚本注入。
func dialogScript(content dialogContent) string {
	data, _ := json.Marshal(content)
	return `(() => {
const data = ` + string(data) + `;
const apply = () => {
 document.documentElement.dataset.uiDraggable = String(data.draggable);
 const text = (id, value) => { const el = document.getElementById(id); if (el) el.textContent = value; };
 text('hybrid-ac-title', data.title);
 text('hybrid-ac-body', data.body);
 text('hybrid-confirm-ac-title', data.title);
 text('hybrid-confirm-ac-body', data.body);
 text('confirm-confirm', data.ok);
 text('confirm-cancel', data.cancel);
 text('confirm-id', data.event);
 text('hybrid-notify-text', data.body);
 const container = document.getElementById('hybrid-show-container');
 if (container && data.html) container.innerHTML = data.body;
 const duration = data.notification ? data.durationSeconds : data.destroy;
 if (duration > 0) {
  const bar = document.querySelector('.bar');
  if (bar) bar.style.animation = 'hybrid-countdown ' + duration + 's linear forwards';
  if (!data.notification) setTimeout(() => window.wails.Window.Close(), duration * 1000);
 }
 window.__HYBRID_DIALOG_DATA__ = data;
 window.dispatchEvent(new Event('hybrid:dialog-config'));
};
if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', apply, { once: true });
else apply();
})();`
}

// dialogOptions 生成原生弹框的公共窗口配置，包括位置、尺寸、无边框、置顶和不在任务栏显示等选项。
func dialogOptions(url, place string, width, height int) utils.NewWindowOptions {
	return utils.NewWindowOptions{
		Width: width, Height: height, Url: url,
		Place:     utils.StrWithDefault(place, utils.PlaceCenter),
		FrameLess: true, AlwaysOnTop: true, DisableResize: true, HiddenOnTaskbar: true,
	}
}

// Alert 检查 ui 权限后显示纯文本消息；destroy 的单位为秒，非正数表示不自动关闭。
func (s *UiService) Alert(ctx context.Context, title, body string, destroy int, place string) error {
	return s.AlertWithOptions(ctx, AlertReq{Title: title, Body: body, Destroy: destroy, Place: place})
}

// draggableDefault 区分未传入和显式 false，所有 UI 弹框默认都可拖拽。
func draggableDefault(value *bool) bool { return value == nil || *value }

// AlertWithOptions 显示纯文本消息并支持 draggable 属性，destroy 单位为秒，非正数保留旧版不自动关闭的语义。
func (s *UiService) AlertWithOptions(ctx context.Context, req AlertReq) error {
	if err := host.Require(ctx, "ui"); err != nil {
		return err
	}
	w := s.controller.CreateDialog(ctx, dialogOptions("/Alert.html", req.Place, 300, 150))
	w.ExecJS(dialogScript(dialogContent{Title: req.Title, Body: req.Body, Destroy: req.Destroy, Draggable: draggableDefault(req.Draggable)}))
	w.Show()
	return nil
}

// Show 检查 ui.html 权限后展示自定义 HTML 浮窗；内容不做 HTML 清洗，只能传入受信任的应用代码。
func (s *UiService) Show(ctx context.Context, req ShowReq) error {
	if err := host.Require(ctx, "ui.html"); err != nil {
		return err
	}
	w := s.controller.CreateDialog(ctx, dialogOptions("/Show.html", req.Place, req.Width, req.Height))
	w.ExecJS(dialogScript(dialogContent{Body: req.Body, HTML: true, Draggable: draggableDefault(req.Draggable)}))
	w.Show()
	return nil
}

// confirmResult 保存确认结果，只接收第一次有效回复；关闭窗口也会提交取消结果。
type confirmResult struct {
	once sync.Once
	done chan string
}

// newConfirmResult 创建只接收一次有效结果的确认状态，使用带缓冲通道避免事件回调阻塞。
func newConfirmResult() *confirmResult { return &confirmResult{done: make(chan string, 1)} }

// complete 仅接收字符串 1 或 0，并通过 sync.Once 保证重复点击、关闭与并发回包只提交一次结果。
func (r *confirmResult) complete(value any) {
	answer, ok := value.(string)
	if !ok || (answer != "0" && answer != "1") {
		return
	}
	r.once.Do(func() { r.done <- answer })
}

// wait 等待确认结果、调用取消、应用关闭或超时；未取得正常确认结果时按取消处理并返回字符串 0。
func (r *confirmResult) wait(ctx context.Context, shutdown, expired <-chan struct{}) string {
	select {
	case answer := <-r.done:
		return answer
	case <-ctx.Done():
		return "0"
	case <-shutdown:
		return "0"
	case <-expired:
		return "0"
	}
}

// Confirm 检查 ui 权限后显示确认框，并等待结果或取消；返回字符串 1 表示确认，0 表示取消、关闭或超时。
// 退出等待时统一清理事件监听、计时上下文及弹框，避免遗留后台等待。
func (s *UiService) Confirm(ctx context.Context, req ConfirmReq) (string, error) {
	if err := host.Require(ctx, "ui"); err != nil {
		return "", err
	}
	app := application.Get()
	eventName := "hybrid-infra-confirm-result-" + strconv.FormatUint(confirmNum.Add(1), 10)
	options := dialogOptions("/Confirm.html", req.Place, 300, 150)
	options.Name = eventName
	w := s.controller.CreateDialog(ctx, options)
	result := newConfirmResult()
	app.Event.On(eventName, func(e *application.CustomEvent) {
		if e.Sender == w.Name() {
			result.complete(e.Data)
		}
	})
	unclose := w.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) { result.complete("0") })
	defer func() {
		// 事件名只属于当前弹框。Off 会同时移除登记项，避免仅调用取消订阅后留下空列表。
		app.Event.Off(eventName)
		unclose()
		w.Close()
	}()
	w.ExecJS(dialogScript(dialogContent{
		Draggable: draggableDefault(req.Draggable),
		Title:     utils.StrWithDefault(req.Title, "确认框"), Body: utils.StrWithDefault(req.Body, "是否确认"),
		OK: utils.StrWithDefault(req.OkText, "确认"), Cancel: utils.StrWithDefault(req.CancelText, "取消"), Event: eventName,
	}))
	w.Show()
	seconds := req.TimeoutSeconds
	if seconds <= 0 {
		seconds = 300
	}
	if seconds > 86400 {
		seconds = 86400
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	return result.wait(ctx, app.Context().Done(), timeoutCtx.Done()), nil
}

// OpenWindowByUrl 保留旧版窗口打开入口，检查 windows 权限后交给统一宿主管理器执行复用、导航策略和权限继承。
func (s *UiService) OpenWindowByUrl(ctx context.Context, option utils.NewWindowOptions) (host.WindowInfo, error) {
	if err := host.Require(ctx, "windows"); err != nil {
		return host.WindowInfo{}, err
	}
	return s.controller.OpenFor(ctx, option)
}
