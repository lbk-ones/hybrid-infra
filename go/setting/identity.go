package setting

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const DefaultAppID = "hybrid-infra"

var appIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

// Identity 返回当前应用身份，兼容未设置 appId 的旧配置和内部测试构造。
func (p *PkgInfo) Identity() string {
	if p.AppID == "" {
		return DefaultAppID
	}
	return p.AppID
}

// ProtocolScheme 为每个业务应用派生独立协议；默认身份保留旧 hybrid-infra:// 链接。
func (p *PkgInfo) ProtocolScheme() string {
	if p.Identity() == DefaultAppID {
		return DefaultAppID
	}
	return "hybrid-" + p.Identity()
}

// ConfigLocation 返回启动时选中的固定配置路径和来源，供设置中心展示；不随磁盘上其他配置变化而切换。
func (p *PkgInfo) ConfigLocation() (string, string) { return p.sourcePath, p.sourceKind }

// appDataDir 将自定义身份放入 apps 子目录，默认身份继续使用旧目录，不迁移或覆盖已有业务数据。
func appDataDir(id string) (string, error) {
	if !appIDPattern.MatchString(id) {
		return "", errors.New("invalid appId")
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	if id == DefaultAppID {
		return filepath.Join(base, DefaultAppID), nil
	}
	return filepath.Join(base, DefaultAppID, "apps", id), nil
}

// deploymentPaths 查找 EXE 旁及开发项目的部署配置；身份引导不读取用户配置，以免不同应用互相借用身份。
func deploymentPaths() ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	paths := []string{filepath.Join(filepath.Dir(exe), "settings.json")}
	if cwd, err := os.Getwd(); err == nil {
		if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
			paths = append(paths, filepath.Join(cwd, "settings.json"))
		}
	}
	return paths, nil
}

// ApplicationID 按环境身份、协议启动参数、显式配置或部署配置引导身份；配置缺失保留默认值，错误配置明确报错。
func ApplicationID() (string, error) {
	if id := os.Getenv("HYBRID_APP_ID"); id != "" {
		if !appIDPattern.MatchString(id) {
			return "", errors.New("invalid HYBRID_APP_ID")
		}
		return id, nil
	}
	if id, err := argumentIdentity(os.Args[1:]); err != nil || id != "" {
		return id, err
	}
	paths, err := deploymentPaths()
	if err != nil {
		return "", err
	}
	if explicit := os.Getenv("HYBRID_SETTINGS"); explicit != "" {
		paths = []string{explicit}
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) && os.Getenv("HYBRID_SETTINGS") == "" {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("read application identity: %w", err)
		}
		var identity struct {
			AppID string `json:"appId"`
		}
		if err := json.Unmarshal(data, &identity); err != nil {
			return "", fmt.Errorf("parse application identity: %w", err)
		}
		if identity.AppID == "" {
			return DefaultAppID, nil
		}
		if !appIDPattern.MatchString(identity.AppID) {
			return "", errors.New("invalid appId in deployment settings")
		}
		return identity.AppID, nil
	}
	return DefaultAppID, nil
}

// argumentIdentity 读取协议启动时的固定身份参数，只接受合法应用名，不接受路径或可执行命令。
func argumentIdentity(args []string) (string, error) {
	var id string
	for _, arg := range args {
		if !strings.HasPrefix(arg, "--hybrid-app-id=") {
			continue
		}
		candidate := strings.TrimPrefix(arg, "--hybrid-app-id=")
		if !appIDPattern.MatchString(candidate) || id != "" && id != candidate {
			return "", errors.New("invalid or conflicting --hybrid-app-id")
		}
		id = candidate
	}
	return id, nil
}

// configLocation 根据引导身份查找应用专属用户配置，再回退部署配置；显式路径始终优先，旧应用保留原顺序。
func configLocation() (string, string, error) {
	if path := os.Getenv("HYBRID_SETTINGS"); path != "" {
		absolute, err := filepath.Abs(path)
		return absolute, "environment", err
	}
	dir, err := DataDir()
	if err != nil {
		return "", "", err
	}
	deployments, err := deploymentPaths()
	if err != nil {
		return "", "", err
	}
	paths := append([]string{filepath.Join(dir, "settings.json")}, deployments...)
	sources := []string{"user", "executable", "development"}
	for index, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path, sources[index], nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", "", err
		}
	}
	return "", "defaults", nil
}

// ReadConfig 读取固定应用的配置；用户配置不能更换引导身份，避免保存后转向另一应用的数据目录。
func ReadConfig(path, id string) (*PkgInfo, error) {
	config := &PkgInfo{AppID: id, SplashEnabled: true, Title: "Hybrid App", Url: "/", SingleInstance: true, CloseBehavior: "exit", RememberWindows: true, MinWidth: 800, MinHeight: 600, LoadTimeoutSeconds: 30, ExternalLinks: "browser"}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read settings %s: %w", path, err)
		}
		if err := json.Unmarshal(data, config); err != nil {
			return nil, fmt.Errorf("parse settings %s: %w", path, err)
		}
	}
	if config.AppID == "" {
		config.AppID = id
	}
	if config.AppID != id {
		return nil, errors.New("配置 appId 与启动身份不一致；请修改部署配置或 HYBRID_APP_ID 后重新启动")
	}
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("settings %s: %w", path, err)
	}
	return config, nil
}
