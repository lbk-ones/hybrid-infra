package host

import (
	"encoding/json"
	"github.com/wailsapp/wails/v3/pkg/application"
	"os"
	"path/filepath"
	"sync"
)

// WindowState 是需要写入磁盘的最小窗口状态，不保存页面内容、任务或访问权限。
type WindowState struct {
	Bounds     application.Rect `json:"bounds"`
	Maximised  bool             `json:"maximised"`
	Fullscreen bool             `json:"fullscreen,omitempty"`
}

// windowRestorePlan 将普通矩形和显示模式分开恢复；显式启动模式优先于记忆，旧记录缺少全屏字段时按普通/最大化兼容。
func windowRestorePlan(mode string, width, height, minWidth, minHeight int, saved WindowState, remember bool, areas []application.Rect) (WindowState, application.WindowState) {
	start := application.WindowStateNormal
	if width <= 0 || height <= 0 {
		width, height = 1280, 800
		start = application.WindowStateMaximised
	}
	normal := application.Rect{Width: max(width, minWidth), Height: max(height, minHeight)}
	if remember {
		if saved.Bounds.Width > 0 && saved.Bounds.Height > 0 {
			normal = saved.Bounds
		}
		if saved.Fullscreen {
			start = application.WindowStateFullscreen
		} else if saved.Maximised {
			start = application.WindowStateMaximised
		} else {
			start = application.WindowStateNormal
		}
	}
	switch mode {
	case "normal":
		start = application.WindowStateNormal
	case "maximized":
		start = application.WindowStateMaximised
	case "fullscreen":
		start = application.WindowStateFullscreen
	}
	normal = FitBounds(normal, areas, minWidth, minHeight)
	return WindowState{Bounds: normal, Maximised: start == application.WindowStateMaximised, Fullscreen: start == application.WindowStateFullscreen}, start
}

// stateStore 管理窗口状态的内存副本及 JSON 文件，使用互斥锁避免并发保存相互覆盖。
type stateStore struct {
	mu     sync.Mutex
	path   string
	states map[string]WindowState
}

// newStateStore 在用户数据目录中读取窗口状态；文件缺失、格式损坏或内容为空时使用空状态表。
func newStateStore(dir string) *stateStore {
	s := &stateStore{path: filepath.Join(dir, "window-state.json"), states: map[string]WindowState{}}
	if data, err := os.ReadFile(s.path); err == nil {
		var states map[string]WindowState
		if json.Unmarshal(data, &states) == nil {
			s.states = states
		}
	}
	if s.states == nil {
		s.states = map[string]WindowState{}
	}
	return s
}

// get 按窗口 ID 读取内存中的状态副本，同时返回该窗口是否有保存记录。
func (s *stateStore) get(id string) (WindowState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.states[id]
	return v, ok
}

// put 更新内存中的窗口状态，限制新登记数量以免无限增长；已有窗口和主窗口仍可继续更新。
func (s *stateStore) put(id string, state WindowState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.states) < 100 || id == "main-window" {
		s.states[id] = state
	} else if _, ok := s.states[id]; ok {
		s.states[id] = state
	}
}

// save 先把状态写入同目录临时文件并同步到磁盘，再替换正式文件，尽量避免中断写入造成旧状态损坏。
func (s *stateStore) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(s.states, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".window-state-*")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temp, s.path)
}

// FitBounds 将记住的窗口矩形调整到现有显示器工作区内，支持负坐标。
// 优先选择与原窗口重叠最多的工作区；完全不可见时居中回退，工作区小于最小尺寸时优先保证窗口可见。
func FitBounds(bounds application.Rect, areas []application.Rect, minWidth, minHeight int) application.Rect {
	if len(areas) == 0 {
		return bounds
	}
	area := areas[0]
	best := 0
	for _, candidate := range areas {
		overlap := bounds.Intersect(candidate)
		score := overlap.Width * overlap.Height
		if score > best {
			best = score
			area = candidate
		}
	}
	bounds.Width = min(max(bounds.Width, minWidth), area.Width)
	bounds.Height = min(max(bounds.Height, minHeight), area.Height)
	if best == 0 {
		bounds.X = area.X + (area.Width-bounds.Width)/2
		bounds.Y = area.Y + (area.Height-bounds.Height)/2
	}
	bounds.X = max(area.X, min(bounds.X, area.X+area.Width-bounds.Width))
	bounds.Y = max(area.Y, min(bounds.Y, area.Y+area.Height-bounds.Height))
	return bounds
}
