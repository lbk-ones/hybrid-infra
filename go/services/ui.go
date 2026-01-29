package services

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"hybrid-infra/go/utils"
	"strconv"
	"strings"
	"sync/atomic"
)

var (
	confirmNum atomic.Int64
)

type UiService struct {
}

func NewUiService() *UiService {

	return &UiService{}
}

type ButtonText struct {
	Ok     string `json:"ok"`
	Cancel string `json:"cancel"`
}
type ConfirmReq struct {
	Title      string `json:"title"` // 标题
	Body       string `json:"body"`  // 内容体
	OkText     string `json:"okText"`
	CancelText string `json:"cancelText"`
	Place      string `json:"place"` // 位置 center、bottomLeft、bottomRight、topRight、topLeft
}
type ShowReq struct {
	Body   string `json:"body"`  // 内容体
	Place  string `json:"place"` // 位置 center、bottomLeft、bottomRight、topRight、topLeft
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// Alert    提示
// title    标题
// body     body体
// destroy  关闭时间 0代表不会关闭
// place    位置 center、bottomLeft、bottomRight、topRight、topLeft
func (*UiService) Alert(title string, body string, destroy int, place string) {
	application.Get().Logger.Debug("Alert params -> " + title + body + strconv.Itoa(destroy))

	var builder strings.Builder
	builder.WriteString("document.getElementById('hybrid-ac-title').innerHTML = \"" + title + "\";")
	builder.WriteString("document.getElementById('hybrid-ac-body').innerHTML = \"" + body + "\";")
	if destroy > 0 {
		deseconds := destroy * 1000
		builder.WriteString("const bar = document.querySelector('.bar');bar.style.transform = \"translateX(-100%)\";bar.style.transition = \"transform " + strconv.Itoa(destroy) + "s linear\";")
		builder.WriteString("window.setTimeout(()=>{window.wails.Window.Close()}," + strconv.Itoa(deseconds) + ")")
	}
	option := utils.NewWindowOptions{
		Width:           300,
		Height:          150,
		Url:             "/Alert.html",
		Place:           utils.PlaceCenter,
		FrameLess:       true,
		AlwaysOnTop:     true,
		DisableResize:   true,
		IsFullscreen:    false,
		HiddenOnTaskbar: true,
	}
	modalWindow := utils.CreateUrlWindow(option)
	exeJs := builder.String()
	application.Get().Logger.Debug("Alert will exe js is -> " + exeJs)
	modalWindow.ExecJS(exeJs)
	modalWindow.Show()
}

// Show 展示html
func (*UiService) Show(req ShowReq) {
	application.Get().Logger.Debug("Show params -> " + utils.GetJsonStr(req))
	var builder strings.Builder
	tempStr := "document.getElementById('hybrid-show-container').innerHTML = `{html}`;"
	finalHtml := strings.ReplaceAll(tempStr, "{html}", req.Body)
	//finalHtml := strings.ReplaceAll(tempStr, "{html}", utils.FormatHTMLForInnerHTML(req.Body))
	builder.WriteString(finalHtml)
	option := utils.NewWindowOptions{
		Width:           req.Width,
		Height:          req.Height,
		Url:             "/Show.html",
		Place:           utils.StrWithDefault(req.Place, utils.PlaceCenter),
		FrameLess:       true,
		AlwaysOnTop:     true,
		DisableResize:   true,
		IsFullscreen:    false,
		HiddenOnTaskbar: true,
	}
	modalWindow := utils.CreateUrlWindow(option)
	exeJs := builder.String()
	application.Get().Logger.Debug("Alert will exe js is -> " + exeJs)
	modalWindow.ExecJS(exeJs)
	modalWindow.Show()
}

// Confirm 确认框
// 返回1代表通过 0代表取消
func (*UiService) Confirm(req ConfirmReq) string {
	app := application.Get()
	evN := "hybrid-infra-confirm-result" + strconv.FormatInt(confirmNum.Load(), 10)
	confirmNum.Add(1)
	app.Logger.Debug("confirm req is: " + utils.GetJsonStr(req))
	var builder strings.Builder
	builder.WriteString("document.getElementById('hybrid-confirm-ac-title').innerHTML = \"" + utils.StrWithDefault(req.Title, "确认框") + "\";")
	builder.WriteString("document.getElementById('hybrid-confirm-ac-body').innerHTML = \"" + utils.StrWithDefault(req.Body, "是否确认") + "\";")
	builder.WriteString("document.getElementById('confirm-confirm').innerHTML = \"" + utils.StrWithDefault(req.OkText, "确认") + "\";")
	builder.WriteString("document.getElementById('confirm-cancel').innerHTML = \"" + utils.StrWithDefault(req.CancelText, "取消") + "\";")
	builder.WriteString("document.getElementById('confirm-id').innerHTML = \"" + evN + "\";")
	var res any
	option := utils.NewWindowOptions{
		Width:           300,
		Height:          150,
		Url:             "/Confirm.html",
		Place:           utils.StrWithDefault(req.Place, utils.PlaceCenter),
		FrameLess:       true,
		AlwaysOnTop:     true,
		DisableResize:   true,
		IsFullscreen:    false,
		HiddenOnTaskbar: true,
	}
	confirmWindow := utils.CreateUrlWindow(option)
	confirmWindow.ExecJS(builder.String())
	confirmWindow.Show()
	chanRes := make(chan any, 1)
	app.Event.On(evN, func(e *application.CustomEvent) {
		res = e.Data
		app.Logger.Debug("接受到事件" + res.(string))
		select {
		case chanRes <- res:
			confirmWindow.Close()
		default:
			app.Logger.Error("警告：通道缓冲区已满，本次事件数据未发送：%v\n", res)
		}
	})
	return (<-chanRes).(string)
}

// OpenWindowByUrl 通过地址连接和其他信息一起打开一个新窗口
func (*UiService) OpenWindowByUrl(option utils.NewWindowOptions) {
	confirmWindow := utils.CreateUrlWindow(option)
	confirmWindow.Show()
}
