package services

import (
	"hybrid-infra/go/utils"
	"strconv"
	"strings"
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
type ShowInfo struct {
	Title  string `json:"title"`  // 标题
	Body   string `json:"body"`   // 内容体
	Footer string `json:"footer"` // Footer
}

// Alert    提示
// title    标题
// body     body体
// destroy  关闭时间 0代表不会关闭
func (*UiService) Alert(title string, body string, destroy int) {
	var builder strings.Builder
	builder.WriteString("document.getElementById('hybrid-ac-title').innerHTML = '" + title + "';")
	builder.WriteString("document.getElementById('hybrid-ac-body').innerHTML = '" + body + "';")
	if destroy > 0 {
		deseconds := destroy * 1000
		builder.WriteString("const bar = document.querySelector('.bar');bar.style.transform = 'translateX(-100%)';bar.style.transition = 'transform " + strconv.Itoa(destroy) + "s linear';")
		builder.WriteString("window.setTimeout(()=>{window.wails.Window.Close()}," + strconv.Itoa(deseconds) + ")")
	}
	modalWindow := utils.CreateUrlWindow(300, 150, "/Alert.html")
	modalWindow.ExecJS(builder.String())
	modalWindow.Show()
}

func (*UiService) Show(str string) {

}

func (*UiService) Confirm(str ShowInfo) {

}
