package services

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// TestDialogScriptTreatsContentAsData 实际执行生成的弹框脚本，验证引号、换行及恶意片段按数据传入，不会打断脚本或触发额外执行。
func TestDialogScriptTreatsContentAsData(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required for dialog boundary tests")
	}
	payload := "引号\"'\n` ${globalThis.injected = true} </script><img onerror='globalThis.injected=true'>\u2028\\"
	for _, html := range []bool{false, true} {
		content := dialogContent{Title: payload, Body: payload, OK: payload, Cancel: payload, Event: "event-123", HTML: html}
		want, _ := json.Marshal(content)
		for _, loading := range []bool{false, true} {
			script := `const assert = require('node:assert/strict');
const expected = ` + string(want) + `;
const elements = new Map();
let ready;
globalThis.document = {
 documentElement: { dataset: {} },
 readyState: ` + map[bool]string{true: "'loading'", false: "'complete'"}[loading] + `,
 getElementById(id) { if (!elements.has(id)) elements.set(id, {}); return elements.get(id); },
 addEventListener(name, fn) { assert.equal(name, 'DOMContentLoaded'); ready = fn; }
};
globalThis.window = { dispatchEvent() {} };
` + dialogScript(content) + `
if (ready) ready();
assert.equal(elements.get('hybrid-ac-title').textContent, expected.title);
assert.equal(elements.get('hybrid-confirm-ac-body').textContent, expected.body);
assert.equal(elements.get('confirm-id').textContent, expected.event);
assert.equal(elements.get('hybrid-show-container').innerHTML, expected.html ? expected.body : undefined);
assert.equal(globalThis.injected, undefined);
`
			cmd := exec.Command("node", "-")
			cmd.Stdin = strings.NewReader(script)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("html=%v loading=%v: %v\n%s", html, loading, err, out)
			}
		}
	}
}

// TestConfirmFirstValidResultWins 验证无效结果被忽略，首次有效确认结果在并发重复回包时也不会被覆盖。
func TestConfirmFirstValidResultWins(t *testing.T) {
	r := newConfirmResult()
	for _, invalid := range []any{nil, 1, []string{"1"}, "yes", true} {
		r.complete(invalid)
	}
	select {
	case <-r.done:
		t.Fatal("invalid event accepted")
	default:
	}
	r.complete("1")
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() { defer wg.Done(); r.complete("0") }()
	}
	wg.Wait()
	if got := <-r.done; got != "1" {
		t.Fatalf("got %q", got)
	}
	select {
	case <-r.done:
		t.Fatal("duplicate result delivered")
	default:
	}
}

// TestConfirmCancellationPaths 分别验证调用取消、应用关闭和超时都会结束确认等待并返回取消结果。
func TestConfirmCancellationPaths(t *testing.T) {
	for _, source := range []string{"caller", "shutdown", "timeout"} {
		t.Run(source, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			shutdown, expired := make(chan struct{}), make(chan struct{})
			switch source {
			case "caller":
				cancel()
			case "shutdown":
				close(shutdown)
			case "timeout":
				close(expired)
			}
			if got := newConfirmResult().wait(ctx, shutdown, expired); got != "0" {
				t.Fatalf("got %q", got)
			}
		})
	}
}
