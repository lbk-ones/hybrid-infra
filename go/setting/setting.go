package setting

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

type Setting struct{}
type PkgInfo struct {
	Title          string   `json:"title"`
	Url            string   `json:"url"`
	Debug          bool     `json:"debug"`
	Frameless      bool     `json:"frameless"`
	Width          int      `json:"width"`
	Height         int      `json:"height"`
	LogLevel       int      `json:"logLevel"`
	AllowedOrigins []string `json:"allowedOrigins"` // Additional frontends allowed to access local assets/runtime.
}

func New() *Setting { return &Setting{} }

// DataDir is independent of the process working directory.
func DataDir() (string, error) {
	if dir := os.Getenv("HYBRID_DATA_DIR"); dir != "" {
		return filepath.Abs(dir)
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hybrid-infra"), nil
}

// ConfigPath resolves an explicit override, user config, executable sibling,
// then a development checkout (identified by go.mod). Missing defaults are OK.
func ConfigPath() (string, error) {
	if path := os.Getenv("HYBRID_SETTINGS"); path != "" {
		return filepath.Abs(path)
	}
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	candidates := []string{filepath.Join(dir, "settings.json")}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	candidates = append(candidates, filepath.Join(filepath.Dir(exe), "settings.json"))
	if cwd, err := os.Getwd(); err == nil {
		if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
			candidates = append(candidates, filepath.Join(cwd, "settings.json"))
		}
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return "", nil
}

func (*Setting) Parse() (*PkgInfo, error) {
	config := &PkgInfo{Title: "App", Url: "/"}
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	if path == "" {
		return config, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read settings %s: %w", path, err)
	}
	if err := json.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("parse settings %s: %w", path, err)
	}
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("settings %s: %w", path, err)
	}
	return config, nil
}

func (p *PkgInfo) validate() error {
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
	for _, origin := range p.AllowedOrigins {
		u, err := url.Parse(origin)
		if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("invalid allowed origin %q: use scheme://host[:port] without a trailing slash", origin)
		}
	}
	return nil
}

// TrustedOrigins includes the configured main remote frontend automatically.
func (p *PkgInfo) TrustedOrigins() []string {
	origins := append([]string{}, p.AllowedOrigins...)
	if u, err := url.Parse(p.Url); err == nil && u.Host != "" {
		origins = append(origins, u.Scheme+"://"+u.Host)
	}
	return origins
}
