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
	"hybrid-infra/go/utils"
)

var confirmNum atomic.Uint64

type UiService struct{}

func NewUiService() *UiService { return &UiService{} }

type ConfirmReq struct {
	Title          string `json:"title"`
	Body           string `json:"body"`
	OkText         string `json:"okText"`
	CancelText     string `json:"cancelText"`
	Place          string `json:"place"`
	TimeoutSeconds int    `json:"timeoutSeconds"` // <= 0 uses the 300 second default.
}
type ShowReq struct {
	Body   string `json:"body"` // Trusted HTML only; may contain event handlers.
	Place  string `json:"place"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type dialogContent struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	OK      string `json:"ok"`
	Cancel  string `json:"cancel"`
	Event   string `json:"event"`
	Destroy int    `json:"destroy"`
	HTML    bool   `json:"html"`
}

// JSON is passed as data, never interpolated into a JavaScript string literal.
// Wait for DOM readiness as the Wails runtime can initialise before the body.
func dialogScript(content dialogContent) string {
	data, _ := json.Marshal(content)
	return `(() => {
const data = ` + string(data) + `;
const apply = () => {
 const text = (id, value) => { const el = document.getElementById(id); if (el) el.textContent = value; };
 text('hybrid-ac-title', data.title);
 text('hybrid-ac-body', data.body);
 text('hybrid-confirm-ac-title', data.title);
 text('hybrid-confirm-ac-body', data.body);
 text('confirm-confirm', data.ok);
 text('confirm-cancel', data.cancel);
 text('confirm-id', data.event);
 const container = document.getElementById('hybrid-show-container');
 if (container && data.html) container.innerHTML = data.body;
 if (data.destroy > 0) {
  const bar = document.querySelector('.bar');
  if (bar) { bar.style.transition = 'transform ' + data.destroy + 's linear'; requestAnimationFrame(() => { bar.style.transform = 'translateX(-100%)'; }); }
  setTimeout(() => window.wails.Window.Close(), data.destroy * 1000);
 }
};
if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', apply, { once: true });
else apply();
})();`
}

func dialogOptions(url, place string, width, height int) utils.NewWindowOptions {
	return utils.NewWindowOptions{
		Width: width, Height: height, Url: url,
		Place:     utils.StrWithDefault(place, utils.PlaceCenter),
		FrameLess: true, AlwaysOnTop: true, DisableResize: true, HiddenOnTaskbar: true,
	}
}

// Alert displays plain text. destroy is in seconds; zero keeps the dialog open.
func (*UiService) Alert(title, body string, destroy int, place string) {
	w := utils.CreateUrlWindow(dialogOptions("/Alert.html", place, 300, 150))
	w.ExecJS(dialogScript(dialogContent{Title: title, Body: body, Destroy: destroy}))
	w.Show()
}

// Show displays trusted application HTML. Do not pass untrusted/user HTML here.
func (*UiService) Show(req ShowReq) {
	w := utils.CreateUrlWindow(dialogOptions("/Show.html", req.Place, req.Width, req.Height))
	w.ExecJS(dialogScript(dialogContent{Body: req.Body, HTML: true}))
	w.Show()
}

// confirmResult accepts only the first valid answer, including window closure.
type confirmResult struct {
	once sync.Once
	done chan string
}

func newConfirmResult() *confirmResult { return &confirmResult{done: make(chan string, 1)} }
func (r *confirmResult) complete(value any) {
	answer, ok := value.(string)
	if !ok || (answer != "0" && answer != "1") {
		return
	}
	r.once.Do(func() { r.done <- answer })
}

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

// Confirm returns "1" for OK, "0" for cancellation, closure or timeout.
// The context is supplied by Wails and supports JS CancellablePromise.cancel().
func (*UiService) Confirm(ctx context.Context, req ConfirmReq) string {
	app := application.Get()
	eventName := "hybrid-infra-confirm-result-" + strconv.FormatUint(confirmNum.Add(1), 10)
	options := dialogOptions("/Confirm.html", req.Place, 300, 150)
	options.Name = eventName
	w := utils.CreateUrlWindow(options)
	result := newConfirmResult()
	app.Event.On(eventName, func(e *application.CustomEvent) {
		if e.Sender == w.Name() {
			result.complete(e.Data)
		}
	})
	unclose := w.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) { result.complete("0") })
	defer func() {
		// This event name is private to the dialog. Off also removes the map
		// entry; Wails' unsubscribe callback leaves an empty entry behind.
		app.Event.Off(eventName)
		unclose()
		w.Close()
	}()
	w.ExecJS(dialogScript(dialogContent{
		Title: utils.StrWithDefault(req.Title, "确认框"), Body: utils.StrWithDefault(req.Body, "是否确认"),
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
	return result.wait(ctx, app.Context().Done(), timeoutCtx.Done())
}

// OpenWindowByUrl opens an internal route or a remote HTTP(S) page.
func (*UiService) OpenWindowByUrl(option utils.NewWindowOptions) {
	w := utils.CreateUrlWindow(option)
	w.Show()
}
