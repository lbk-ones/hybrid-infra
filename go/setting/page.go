package setting

import (
	"errors"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

const LocalPagePrefix = "/local/"

// EffectivePageMode 返回实际页面模式，兼容未配置 pageMode 的旧配置，不把推断结果写回磁盘。
func (p *PkgInfo) EffectivePageMode() string {
	if p.PageMode != "" {
		return p.PageMode
	}
	u, _ := url.Parse(p.Url)
	if u != nil && (u.Scheme == "http" || u.Scheme == "https") {
		return "http"
	}
	return "builtin"
}

// PageURL 返回真正交给 WebView 的页面地址；local 在内部增加专用前缀，保留查询参数和 hash 路由。
func (p *PkgInfo) PageURL() string {
	if p.EffectivePageMode() != "local" {
		return p.Url
	}
	u, err := url.Parse(p.Url)
	if err != nil {
		return ""
	}
	u.Path = LocalPagePrefix + strings.TrimPrefix(u.Path, "/")
	u.RawPath = ""
	return u.String()
}

// LocalEntry 返回相对于本地网页根目录的入口文件名；目录入口读取其 index.html，不开放目录列表。
func (p *PkgInfo) LocalEntry() string {
	u, _ := url.Parse(p.Url)
	if u == nil {
		return ""
	}
	entry := strings.TrimPrefix(u.Path, "/")
	if strings.HasSuffix(u.Path, "/") || entry == "" {
		entry += "index.html"
	}
	return entry
}

// validatePage 校验三种模式与地址是否对应；本地入口只接受目录内的 HTML，拒绝跳转、隐藏文件和系统路径。
func (p *PkgInfo) validatePage() error {
	mode := p.EffectivePageMode()
	u, err := url.Parse(p.Url)
	if err != nil || strings.ContainsAny(p.Url, "\\\x00\r\n") {
		return errors.New("invalid page URL")
	}
	switch mode {
	case "http":
		if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("pageMode http requires an HTTP(S) url")
		}
	case "builtin", "local":
		if u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") {
			return errors.New("builtin/local pageMode requires a /-prefixed page path")
		}
	default:
		return errors.New("pageMode must be builtin, http or local")
	}
	if mode != "local" {
		if p.LocalRoot != "" {
			return errors.New("localRoot is only valid in local pageMode")
		}
		return nil
	}
	if strings.ContainsAny(p.LocalRoot, "\x00\r\n") || strings.HasPrefix(p.LocalRoot, `\\`) || strings.HasPrefix(p.LocalRoot, "//") {
		return errors.New("localRoot requires a local directory")
	}
	if !filepath.IsAbs(p.LocalRoot) {
		root, err := url.Parse(p.LocalRoot)
		if err != nil || root.Scheme != "" || root.Host != "" {
			return errors.New("localRoot must be a filesystem directory, not a URL")
		}
	}
	for _, segment := range strings.Split(p.LocalEntry(), "/") {
		if segment == "" || strings.HasPrefix(segment, ".") || strings.Contains(segment, ":") {
			return errors.New("local page entry must stay inside its directory")
		}
	}
	ext := strings.ToLower(path.Ext(p.LocalEntry()))
	if ext != ".html" && ext != ".htm" {
		return errors.New("local page entry must be an HTML file")
	}
	return nil
}
