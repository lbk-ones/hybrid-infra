package setting

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Setting struct{}

// IconConfig 为运行时窗口和托盘指定 PNG，本地相对路径以配置文件目录为基准；留空使用内置品牌图标。
type IconConfig struct {
	Application string `json:"application"`
	Tray        string `json:"tray"`
}

// Validate 检查图标地址语法；实际大小和 PNG 内容由宿主加载时校验，不访问网络或读取文件。
func (c IconConfig) Validate() error {
	for _, value := range []string{c.Application, c.Tray} {
		if value == "" {
			continue
		}
		if len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") || strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, "//") {
			return errors.New("icons only supports local PNG paths or HTTPS URLs")
		}
		if filepath.IsAbs(value) {
			continue
		}
		u, err := url.Parse(value)
		if err != nil {
			return errors.New("invalid icon address")
		}
		if u.Scheme == "" && u.Host == "" {
			continue
		}
		if u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) {
			return errors.New("icon URL requires HTTPS without credentials or fragment")
		}
	}
	return nil
}

// UpdateConfig 固定更新信任根和频道；默认关闭，公钥由发布者离线生成，前端不能临时指定下载地址或签名密钥。
type UpdateConfig struct {
	Enabled        bool   `json:"enabled"`
	ManifestURL    string `json:"manifestUrl"`
	PublicKey      string `json:"publicKey"`
	Channel        string `json:"channel"`
	CheckOnStartup bool   `json:"checkOnStartup"`
	AutoDownload   bool   `json:"autoDownload"`
}
type PkgInfo struct {
	AppID               string `json:"appId"` // 发布时确定的应用身份；默认 hybrid-infra 保留旧数据目录，自定义 ID 使用独立数据目录与协议。
	sourcePath          string
	sourceKind          string
	SplashEnabled       bool                `json:"splashEnabled"` // 是否显示独立启动屏；默认开启，关闭后静默引导并直接显示主窗口。
	Icons               IconConfig          `json:"icons"`
	Updates             UpdateConfig        `json:"updates"`
	ShellPrograms       []string            `json:"shellPrograms"` // 允许程序模式执行的程序；启动时解析为固定绝对路径，默认空列表。
	Title               string              `json:"title"`
	Url                 string              `json:"url"`
	PageMode            string              `json:"pageMode"`  // builtin 使用内置资源，http 加载网址，local 读取 EXE 旁目录；留空按旧 url 规则推断。
	LocalRoot           string              `json:"localRoot"` // local 模式的网页目录，相对路径以 EXE 所在目录为基准，留空使用 web。
	Debug               bool                `json:"debug"`
	Frameless           bool                `json:"frameless"`
	Width               int                 `json:"width"`
	Height              int                 `json:"height"`
	LogLevel            int                 `json:"logLevel"`
	AllowedOrigins      []string            `json:"allowedOrigins"` // 额外允许访问本地资源和 runtime 的前端来源。
	SingleInstance      bool                `json:"singleInstance"`
	TrayEnabled         bool                `json:"trayEnabled"`   // 可独立于关闭行为开启托盘；hide 模式仍强制提供托盘入口。
	CloseBehavior       string              `json:"closeBehavior"` // exit 表示关闭主窗口时退出，hide 表示隐藏到托盘。
	RememberWindows     bool                `json:"rememberWindows"`
	MinWidth            int                 `json:"minWidth"`
	MinHeight           int                 `json:"minHeight"`
	LoadTimeoutSeconds  int                 `json:"loadTimeoutSeconds"`
	NavigationOrigins   []string            `json:"navigationOrigins"`
	ExternalLinks       string              `json:"externalLinks"`       // browser 表示交给系统浏览器打开外链，block 表示拒绝。
	Permissions         map[string][]string `json:"permissions"`         // 来源对应的显式能力列表，覆盖默认授权；空列表表示全部拒绝。
	DesktopCapabilities []string            `json:"desktopCapabilities"` // 仅为内置页面开启的第二阶段能力；远程页面仍需显式 permissions。
}

// New 创建配置读取器；实际读取、默认值填充和校验由 Parse 执行。
func New() *Setting { return &Setting{} }

// DataDir 返回指定应用的独立数据目录；不传身份时从启动配置解析，HYBRID_DATA_DIR 是明确的目录覆盖，不自动附加身份。
func DataDir(ids ...string) (string, error) {
	if dir := os.Getenv("HYBRID_DATA_DIR"); dir != "" {
		return filepath.Abs(dir)
	}
	id := DefaultAppID
	var err error
	if len(ids) > 0 {
		id = ids[0]
	} else {
		id, err = ApplicationID()
	}
	if err != nil {
		return "", err
	}
	return appDataDir(id)
}

// ConfigPath 按环境变量、用户数据目录、程序目录和开发仓库的顺序查找配置文件；未显式指定且均未找到时返回空路径。
func ConfigPath() (string, error) {
	path, _, err := configLocation()
	return path, err
}

// Parse 填充默认配置后读取选中的 JSON 文件并校验；没有配置文件时使用默认值，显式配置错误时返回错误。
func (*Setting) Parse() (*PkgInfo, error) {
	id, err := ApplicationID()
	if err != nil {
		return nil, err
	}
	path, source, err := configLocation()
	if err != nil {
		return nil, err
	}
	config, err := ReadConfig(path, id)
	if err != nil {
		return nil, err
	}
	config.sourcePath, config.sourceKind = path, source
	return config, nil
}

// validate 检查窗口尺寸、关闭方式、加载超时、导航地址和能力名称等配置，拒绝无法识别或不安全的取值。
func (p *PkgInfo) validate() error {
	if p.AppID == "" {
		p.AppID = DefaultAppID
	}
	if !appIDPattern.MatchString(p.AppID) {
		return errors.New("appId must be 1-48 lowercase letters, digits or hyphens, starting with a letter")
	}
	if err := p.Icons.Validate(); err != nil {
		return err
	}
	if p.Updates.Channel == "" {
		p.Updates.Channel = "stable"
	}
	if p.Updates.Channel != "stable" && p.Updates.Channel != "beta" {
		return errors.New("updates.channel must be stable or beta")
	}
	if p.Updates.Enabled {
		u, e := url.Parse(p.Updates.ManifestURL)
		if e != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) {
			return errors.New("updates.manifestUrl requires HTTPS (loopback HTTP allowed for local tests)")
		}
		key, e := base64.StdEncoding.DecodeString(p.Updates.PublicKey)
		if e != nil || len(key) != ed25519.PublicKeySize {
			return errors.New("updates.publicKey must be a base64 Ed25519 public key")
		}
	}
	if len(p.ShellPrograms) > 64 {
		return errors.New("shellPrograms supports at most 64 programs")
	}
	for _, program := range p.ShellPrograms {
		if len(program) == 0 || len(program) > 512 || strings.ContainsAny(program, "\x00\r\n") {
			return errors.New("shellPrograms contains an invalid program")
		}
	}
	if p.CloseBehavior != "exit" && p.CloseBehavior != "hide" {
		return errors.New("closeBehavior must be exit or hide")
	}
	if p.ExternalLinks != "browser" && p.ExternalLinks != "block" {
		return errors.New("externalLinks must be browser or block")
	}
	if p.MinWidth < 320 || p.MinHeight < 240 {
		return errors.New("minWidth/minHeight must be at least 320/240")
	}
	if p.LoadTimeoutSeconds < 1 || p.LoadTimeoutSeconds > 300 {
		return errors.New("loadTimeoutSeconds must be between 1 and 300")
	}
	if p.Width < 0 || p.Height < 0 {
		return errors.New("width and height must be non-negative")
	}
	if p.Url == "" {
		p.Url = "/"
	}
	parsed, err := url.Parse(p.Url)
	if err != nil {
		return err
	}
	if !((parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil) &&
		!(parsed.Scheme == "" && parsed.Host == "" && len(p.Url) > 0 && p.Url[0] == '/') {
		return errors.New("url must be an absolute HTTP(S) URL or a local path starting with /")
	}
	if err := p.validatePage(); err != nil {
		return err
	}
	origins := append(append([]string{}, p.AllowedOrigins...), p.NavigationOrigins...)
	permissionSets := map[string][]string{}
	for origin, capabilities := range p.Permissions {
		origins = append(origins, origin)
		permissionSets[origin] = capabilities
	}
	permissionSets["desktopCapabilities"] = p.DesktopCapabilities
	for _, capabilities := range permissionSets {
		for _, capability := range capabilities {
			switch capability {
			case "host", "windows", "ui", "ui.html", "store", "log", "lifecycle", "events", "files", "downloads", "clipboard", "notifications", "shortcuts", "settings", "credentials", "protocol", "shell", "shell.script", "cache", "cache.global", "events.global", "screenshot", "diagnostics", "updates", "backups":
			default:
				return fmt.Errorf("unknown capability %q", capability)
			}
		}
	}
	for _, origin := range origins {
		u, err := url.Parse(origin)
		if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("invalid allowed origin %q: use scheme://host[:port] without a trailing slash", origin)
		}
	}
	return nil
}

// Validate 供设置服务在保存前复用启动校验；任何无效配置都不会写入磁盘。
func (p *PkgInfo) Validate() error { return p.validate() }

// TrustedOrigins 汇总可信页面来源，具体能力仍由来源权限策略计算。
func (p *PkgInfo) TrustedOrigins() []string {
	origins := append([]string{}, p.AllowedOrigins...)
	if u, err := url.Parse(p.PageURL()); err == nil && u.Host != "" {
		origins = append(origins, u.Scheme+"://"+u.Host)
	}
	keys := make([]string, 0, len(p.Permissions))
	for origin := range p.Permissions {
		keys = append(keys, origin)
	}
	sort.Strings(keys)
	origins = append(origins, keys...)
	return origins
}
