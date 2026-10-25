package host

import (
	"context"
	"encoding/json"
	"hybrid-infra/go/utils"
	"os/exec"
	"testing"
)

// TestDeveloperDocsUsesReservedWindow 验证文档窗口名称不允许网页抢占，重复打开只聚焦固定窗口。
func TestDeveloperDocsUsesReservedWindow(t *testing.T) {
	controller := New(testConfig(), t.TempDir())
	ctx := context.WithValue(context.Background(), principalKey{}, Principal{WindowName: "business", Capabilities: []string{"host", "windows"}})
	if _, err := controller.OpenFor(ctx, utils.NewWindowOptions{Name: DeveloperDocsWindow, Url: DeveloperDocsPage}); err == nil {
		t.Fatal("business window could claim documentation ID")
	}
	window := &fakeWindow{name: DeveloperDocsWindow}
	controller.windows[DeveloperDocsWindow] = &entry{window: window, options: utils.NewWindowOptions{Name: DeveloperDocsWindow, Url: DeveloperDocsPage}, status: "ready"}
	for range 2 {
		info, err := controller.OpenDeveloperDocs()
		if err != nil || info.ID != DeveloperDocsWindow || info.URL != DeveloperDocsPage {
			t.Fatalf("docs reuse: %+v %v", info, err)
		}
	}
	if window.focused.Load() != 2 {
		t.Fatal("document window not reused")
	}
}

// TestDeveloperDocsConsoleCommand 用独立 JS 上下文验证控制台命令只发送固定意图，不执行系统命令或接受任意 URL。
func TestDeveloperDocsConsoleCommand(t *testing.T) {
	encoded, _ := json.Marshal(guardScript)
	source := `const vm=require('node:vm');const messages=[];const context={window:{chrome:{webview:{postMessage(value){messages.push(JSON.parse(value.slice(7)))}}}},document:{documentURI:'http://wails.localhost/',addEventListener(){}},location:{pathname:'/'}};vm.runInNewContext(` + string(encoded) + `,context);if(typeof context.window.hybridDocs!=='function')throw Error('missing command');context.window.hybridDocs();const last=messages.at(-1);if(last.type!=='open-docs'||Object.keys(last).length!==1)throw Error('unexpected intent');`
	command := exec.Command("node", "-e", source)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("console bridge: %v %s", err, output)
	}
}
