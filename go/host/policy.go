package host

import (
	"bytes"
	"context"
	"encoding/json"
	"hash/fnv"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"hybrid-infra/go/setting"
)

// Capabilities 列出当前外壳开放的能力名称；它不是任意页面默认拥有的权限清单。
var Capabilities = []string{"host", "windows", "ui", "ui.html", "store", "log", "lifecycle", "events", "files", "downloads", "clipboard", "notifications", "shortcuts", "settings", "credentials", "protocol", "shell", "shell.script", "cache", "cache.global", "events.global", "screenshot"}

// defaultCapabilities 保留第一阶段的默认授权；涉及磁盘和系统集成的新能力必须显式开启。
var defaultCapabilities = []string{"host", "windows", "ui", "ui.html", "store", "log", "lifecycle", "events"}

type principalKey struct{}

// Principal 保存本次 RPC 经过校验的来源、有效能力和真实调用窗口身份。
type Principal struct {
	Origin       string
	Capabilities []string
	WindowName   string
}

// Policy 维护来源授权、服务方法所需能力及窗口继承上限，在 HTTP RPC 入口统一执行检查。
type Policy struct {
	config      *setting.PkgInfo
	methods     map[uint32]string
	names       map[string]string
	windowMu    sync.RWMutex
	windowCaps  map[string][]string
	windowNames map[string]string
}

// NewPolicy 根据应用配置创建权限策略；方法权限在启动时登记，窗口权限在创建和关闭时更新。
func NewPolicy(config *setting.PkgInfo) *Policy {
	return &Policy{config: config, methods: map[uint32]string{}, names: map[string]string{}, windowCaps: map[string][]string{}, windowNames: map[string]string{}}
}

// SetWindowCapabilities 按原生窗口 ID 保存能力上限，并可同时登记真实窗口名；复制能力列表，避免外部修改影响授权。
func (p *Policy) SetWindowCapabilities(id uint, capabilities []string, names ...string) {
	p.windowMu.Lock()
	defer p.windowMu.Unlock()
	p.windowCaps[strconv.FormatUint(uint64(id), 10)] = slices.Clone(capabilities)
	if len(names) > 0 {
		p.windowNames[strconv.FormatUint(uint64(id), 10)] = names[0]
	}
}

// ForgetWindow 撤销已关闭窗口的能力和名称登记，防止后续请求继续使用过期窗口身份。
func (p *Policy) ForgetWindow(id uint) {
	p.windowMu.Lock()
	defer p.windowMu.Unlock()
	delete(p.windowCaps, strconv.FormatUint(uint64(id), 10))
	delete(p.windowNames, strconv.FormatUint(uint64(id), 10))
}

// WindowGrants 计算页面来源权限与窗口继承上限的交集。
// 没有窗口 ID 时只按来源授权；提供了未登记的窗口 ID 时拒绝全部能力。
func (p *Policy) WindowGrants(id string, origin string) []string {
	grants := p.Grants(origin)
	if id == "" {
		return grants
	}
	p.windowMu.RLock()
	ceiling, known := p.windowCaps[id]
	p.windowMu.RUnlock()
	if !known {
		return nil
	}
	return slices.DeleteFunc(grants, func(capability string) bool { return !slices.Contains(ceiling, capability) })
}

// Origin 从 HTTP、HTTPS 或 Wails 地址中提取协议和主机组成的来源，并规范主机名大小写；非法地址返回空字符串。
func Origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "wails") {
		return ""
	}
	return u.Scheme + "://" + strings.ToLower(u.Host)
}

// localOrigin 判断地址是否属于 Wails 内置页面来源，只接受指定协议下的 wails.localhost，不把相似域名当成本地页面。
func localOrigin(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "wails") && u.Hostname() == "wails.localhost" && u.User == nil
}

// Grants 读取指定来源的能力列表；显式配置优先，包括空数组表示全部拒绝，未配置的可信来源使用默认能力。
func (p *Policy) Grants(origin string) []string {
	if grants, ok := p.config.Permissions[origin]; ok {
		return slices.Clone(grants)
	}
	if localOrigin(origin) {
		return append(slices.Clone(defaultCapabilities), p.config.DesktopCapabilities...)
	}
	if slices.Contains(p.config.TrustedOrigins(), origin) {
		return slices.Clone(defaultCapabilities)
	}
	return nil
}

// Require 校验当前授权上下文是否包含指定能力；无授权上下文或能力不足时返回 E_PERMISSION_DENIED。
func Require(ctx context.Context, capability string) error {
	principal, ok := ctx.Value(principalKey{}).(Principal)
	if !ok || !slices.Contains(principal.Capabilities, capability) {
		return Fail("E_PERMISSION_DENIED", "当前页面没有 "+capability+" 权限")
	}
	return nil
}

// CallerCapabilities 复制并返回调用者已经通过检查的能力列表，供窗口继承和 SDK 握手使用。
func CallerCapabilities(ctx context.Context) []string {
	principal, _ := ctx.Value(principalKey{}).(Principal)
	return slices.Clone(principal.Capabilities)
}

// CallerIdentity 返回中间件核验的来源和真实窗口名，供临时 UI 资源校验归属；不信任页面传入的目标窗口名。
func CallerIdentity(ctx context.Context) (origin, window string) {
	p, _ := ctx.Value(principalKey{}).(Principal)
	return p.Origin, p.WindowName
}

// Register 按当前固定 Wails 版本的规则，为服务方法登记名称、方法 ID 和所需能力。
// overrides 可覆盖个别方法的能力；未登记的方法不会自动获得权限。
func (p *Policy) Register(instance any, capability string, overrides map[string]string) {
	t := reflect.TypeOf(instance)
	for i := 0; i < t.NumMethod(); i++ {
		name := t.Method(i).Name
		if slices.Contains([]string{"ServiceName", "ServiceStartup", "ServiceShutdown", "ServeHTTP"}, name) {
			continue
		}
		fqn := t.Elem().PkgPath() + "." + t.Elem().Name() + "." + name
		cap := capability
		if value, ok := overrides[name]; ok {
			cap = value
		}
		h := fnv.New32a()
		_, _ = h.Write([]byte(fqn))
		p.methods[h.Sum32()] = cap
		p.names[fqn] = cap
	}
}

// Navigation 校验页面地址并判断是否允许在应用内打开；合法但不在允许范围内的 HTTP(S) 地址返回 false，交给外链策略处理。
func (p *Policy) Navigation(raw string) (bool, error) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || strings.ContainsAny(raw, "\r\n\\") {
		return false, Fail("E_INVALID_ARGUMENT", "无效的页面地址")
	}
	if u.Scheme == "" && u.Host == "" && strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		return true, nil
	}
	if !localOrigin(raw) && ((u.Scheme != "http" && u.Scheme != "https") || u.Host == "") {
		return false, Fail("E_INVALID_ARGUMENT", "只允许本地路径或 HTTP(S) 地址")
	}
	origin := Origin(raw)
	allowed := localOrigin(origin) || origin == Origin(p.config.Url) || slices.Contains(p.config.NavigationOrigins, origin) || slices.Contains(p.config.TrustedOrigins(), origin)
	return allowed, nil
}

// writeError 把宿主错误写成带错误码的 JSON HTTP 响应，让前端能区分参数错误、权限拒绝和请求过大等情况。
func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(MarshalError(err))
}

// rpcRequest 只解析授权判断需要的 RPC 字段；完整参数仍由 Wails 运行时处理。
type rpcRequest struct {
	Object int `json:"object"`
	Method int `json:"method"`
	Args   struct {
		MethodID   uint32 `json:"methodID"`
		MethodName string `json:"methodName"`
	} `json:"args"`
}

// requestCapability 根据 RPC 子系统或服务方法查找所需能力；未登记的方法、未开放的子系统及被禁用的开发工具返回空字符串。
func (p *Policy) requestCapability(req rpcRequest) string {
	switch req.Object {
	case 0:
		if req.Args.MethodName != "" {
			return p.names[req.Args.MethodName]
		}
		return p.methods[req.Args.MethodID]
	case 3:
		return "events"
	case 6:
		if req.Method == application.WindowOpenDevTools && !p.config.Debug {
			return ""
		}
		return "windows"
	case 7, 8:
		return "host"
	case 10:
		return "host"
	default:
		return "" // 暂不开放文件对话框、剪贴板、任意浏览器命令和绕过任务检查的原始退出接口。
	}
}

// Middleware 在宿主端校验 RPC 来源、请求大小和方法能力，再把真实调用身份写入上下文。
// 直接使用生成的 bindings 也必须经过检查；缺少有效来源或使用分块请求时明确拒绝。
func (p *Policy) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wails/runtime" {
			next.ServeHTTP(w, r)
			return
		}
		rawOrigin := r.Header.Get("Origin")
		origin := Origin(rawOrigin)
		if origin == "" || origin != rawOrigin {
			writeError(w, 403, Fail("E_PERMISSION_DENIED", "缺少有效的调用来源"))
			return
		}
		grants := p.WindowGrants(r.Header.Get("X-Wails-Window-ID"), origin)
		// 分块内容无法在这里完整检查方法权限，因此第一阶段明确拒绝，并将单次 RPC 限制为 1 MiB。
		if r.Header.Get("X-Wails-Chunk-ID") != "" {
			writeError(w, 413, Fail("E_PAYLOAD_TOO_LARGE", "第一阶段 RPC 不支持分块请求，单次调用请小于 1 MiB"))
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024*1024))
		if err != nil {
			writeError(w, 413, Fail("E_PAYLOAD_TOO_LARGE", "调用内容超过 1 MiB"))
			return
		}
		var req rpcRequest
		if len(body) == 0 {
			// 兼容 WebKitGTK 可能把自定义协议调用参数放进 URL 查询字符串的情况。
			q := r.URL.Query()
			body, _ = json.Marshal(map[string]any{"object": json.RawMessage(q.Get("object")), "method": json.RawMessage(q.Get("method")), "args": json.RawMessage(q.Get("args"))})
		}
		if json.Unmarshal(body, &req) != nil {
			writeError(w, 400, Fail("E_INVALID_ARGUMENT", "无效的调用数据"))
			return
		}
		capability := p.requestCapability(req)
		if capability == "" || !slices.Contains(grants, capability) {
			writeError(w, 403, Fail("E_PERMISSION_DENIED", "当前来源没有调用该能力的权限"))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		p.windowMu.RLock()
		windowName := p.windowNames[r.Header.Get("X-Wails-Window-ID")]
		p.windowMu.RUnlock()
		r = r.WithContext(context.WithValue(r.Context(), principalKey{}, Principal{Origin: origin, Capabilities: grants, WindowName: windowName}))
		next.ServeHTTP(w, r)
	})
}
