package setting

import (
	"encoding/json"
	"hybrid-infra/go/fg"
	"os"
	"path/filepath"
)

type Setting struct {
}

type PkgInfo struct {
	Title     string `json:"title"`
	Url       string `json:"url"`       // 项目名称
	Debug     bool   `json:"debug"`     // 项目名称
	Frameless bool   `json:"frameless"` // 项目名称
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	LogLevel  int    `json:"logLevel"` // 日志级别 默认INFO
}

// New 实例化
func New() *Setting {
	return &Setting{}
}

// Parse 解析当前运行路径下的setting.json
func (*Setting) Parse() *PkgInfo {
	pkgJson := &PkgInfo{
		Title:     "App",
		Frameless: false,
		Url:       "/",
		LogLevel:  0,
	}
	dir, err := os.Getwd()
	fg.GetLogger().Info("current dir:", dir)
	if err != nil {
		panic(err)
	}
	filePath := filepath.Join(dir, "settings.json")
	file, err := os.ReadFile(filePath)
	if err != nil {
		fg.GetLogger().Errorln("current dir:", err)
	} else {
		err = json.Unmarshal(file, pkgJson)
		if err != nil {
			fg.GetLogger().Error("settings.json parse error", err)
		}
	}
	return pkgJson
}
