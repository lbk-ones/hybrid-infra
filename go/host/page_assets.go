package host

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"hybrid-infra/go/setting"
)

// PageAssets 仅在 local 模式挂载指定目录，其余请求仍由内置资源处理器处理，不覆盖外壳 UI 或运行时。
type PageAssets struct {
	root *os.Root
	next http.Handler
}

// NewPageAssets 以 EXE 目录解析本地网页根目录并检查入口，使用 os.Root 限制路径和符号链接；不依赖启动工作目录。
func NewPageAssets(config *setting.PkgInfo, embedded http.Handler, executableDir string) (*PageAssets, error) {
	assets := &PageAssets{next: embedded}
	if config.EffectivePageMode() != "local" {
		return assets, nil
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	directory := config.LocalRoot
	if directory == "" {
		directory = "web"
	}
	if !filepath.IsAbs(directory) {
		directory = filepath.Join(executableDir, directory)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("open local page directory: %w", err)
	}
	entry, err := root.Open(config.LocalEntry())
	if err != nil {
		root.Close()
		return nil, fmt.Errorf("open local HTML entry: %w", err)
	}
	info, err := entry.Stat()
	entry.Close()
	if err != nil || !info.Mode().IsRegular() {
		root.Close()
		return nil, fmt.Errorf("local HTML entry must be a regular file")
	}
	assets.root = root
	return assets, nil
}

// Close 释放本地根目录句柄；内置和 HTTP 模式没有额外句柄需要释放。
func (a *PageAssets) Close() error {
	if a.root != nil {
		return a.root.Close()
	}
	return nil
}

// ServeHTTP 在专用前缀内提供网页资源，不开放目录列表、隐藏文件或配置数据；缺失资源不回退成内置页面。
func (a *PageAssets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if a.root == nil || !strings.HasPrefix(r.URL.Path, setting.LocalPagePrefix) {
		a.next.ServeHTTP(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, setting.LocalPagePrefix)
	if name == "" || strings.HasSuffix(name, "/") {
		name += "index.html"
	}
	if strings.ContainsAny(name, "\\:\x00") {
		http.NotFound(w, r)
		return
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "" || strings.HasPrefix(segment, ".") {
			http.NotFound(w, r)
			return
		}
	}
	// 根目录可以配置为 EXE 同目录，因此仅公开网页类型，避免把 settings.json、日志、凭据或 EXE 当静态资源下载。
	extension := strings.ToLower(path.Ext(name))
	switch extension {
	case ".html", ".htm", ".js", ".mjs", ".css", ".svg", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".ico", ".woff", ".woff2", ".ttf", ".otf", ".wasm", ".mp3", ".mp4", ".webm", ".wav", ".ogg":
	default:
		http.NotFound(w, r)
		return
	}
	file, err := a.root.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if contentType := mime.TypeByExtension(extension); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	http.ServeContent(w, r, name, info.ModTime(), file)
}
