package services

import (
	"context"
	"hybrid-infra/go/host"
	"hybrid-infra/go/setting"
)

// DesktopService 是业务前端的桌面能力入口；每个方法独立校验能力，资源归属由宿主管理层继续检查。
type DesktopService struct{ desktop *host.Desktop }

// ListDirectory 读取已选目录的直接条目；不会递归扫描整块磁盘。
func (s *DesktopService) ListDirectory(ctx context.Context, token string) ([]host.DirectoryEntry, error) {
	if err := host.Require(ctx, "files"); err != nil {
		return nil, err
	}
	return s.desktop.ListDirectory(ctx, token)
}

// OpenChild 为已选目录中的直接子项创建只读凭证，拒绝目录跳转和符号链接。
func (s *DesktopService) OpenChild(ctx context.Context, token, name string) (host.FileGrant, error) {
	if err := host.Require(ctx, "files"); err != nil {
		return host.FileGrant{}, err
	}
	return s.desktop.OpenChild(ctx, token, name)
}

// SelectFile 打开原生文件、目录或保存对话框；返回仅对当前页面有效的文件凭证。
func (s *DesktopService) SelectFile(ctx context.Context, mode, name string) (host.FileGrant, error) {
	if err := host.Require(ctx, "files"); err != nil {
		return host.FileGrant{}, err
	}
	return s.desktop.SelectFile(ctx, mode, name)
}

// ReadFile 读取凭证指定的小文件，返回 Base64 编码的二进制内容。
func (s *DesktopService) ReadFile(ctx context.Context, token string) ([]byte, error) {
	if err := host.Require(ctx, "files"); err != nil {
		return nil, err
	}
	return s.desktop.ReadFile(ctx, token)
}

// WriteFile 向保存对话框授权的文件写入 Base64 编码数据，不接受任意磁盘路径。
func (s *DesktopService) WriteFile(ctx context.Context, token string, data []byte) error {
	if err := host.Require(ctx, "files"); err != nil {
		return err
	}
	return s.desktop.WriteFile(ctx, token, data)
}

// ReleaseFile 销毁当前页面不再使用的文件凭证。
func (s *DesktopService) ReleaseFile(ctx context.Context, token string) error {
	if err := host.Require(ctx, "files"); err != nil {
		return err
	}
	return s.desktop.ReleaseFile(ctx, token)
}

// RevealFile 在资源管理器定位选中的文件或打开选中的目录。
func (s *DesktopService) RevealFile(ctx context.Context, token string) error {
	if err := host.Require(ctx, "files"); err != nil {
		return err
	}
	return s.desktop.RevealFile(ctx, token)
}

// StartDownload 从 HTTP(S) 地址下载到已授权的保存目标，返回后台任务快照。
func (s *DesktopService) StartDownload(ctx context.Context, url, token string) (host.DownloadInfo, error) {
	if err := host.Require(ctx, "downloads"); err != nil {
		return host.DownloadInfo{}, err
	}
	return s.desktop.StartDownload(ctx, url, token)
}

// ListDownloads 读取当前页面下载进度和终态，页面可按一秒间隔轮询。
func (s *DesktopService) ListDownloads(ctx context.Context) ([]host.DownloadInfo, error) {
	if err := host.Require(ctx, "downloads"); err != nil {
		return nil, err
	}
	return s.desktop.ListDownloads(ctx), nil
}

// CancelDownload 取消当前页面的后台下载，已完成任务保持不变。
func (s *DesktopService) CancelDownload(ctx context.Context, id string) error {
	if err := host.Require(ctx, "downloads"); err != nil {
		return err
	}
	return s.desktop.CancelDownload(ctx, id)
}

// RetryDownload 为失败或已取消任务创建新的下载，不承诺断点续传。
func (s *DesktopService) RetryDownload(ctx context.Context, id string) (host.DownloadInfo, error) {
	if err := host.Require(ctx, "downloads"); err != nil {
		return host.DownloadInfo{}, err
	}
	return s.desktop.RetryDownload(ctx, id)
}

// OpenDownload 使用默认程序打开完成文件，reveal 为 true 时只定位文件。
func (s *DesktopService) OpenDownload(ctx context.Context, id string, reveal bool) error {
	if err := host.Require(ctx, "downloads"); err != nil {
		return err
	}
	return s.desktop.OpenDownload(ctx, id, reveal)
}

// RemoveDownload 移除本页面已经结束的任务记录，磁盘文件不会删除。
func (s *DesktopService) RemoveDownload(ctx context.Context, id string) error {
	if err := host.Require(ctx, "downloads"); err != nil {
		return err
	}
	return s.desktop.RemoveDownload(ctx, id)
}

// ClipboardText 读取、写入或清空系统文本剪贴板，失败时返回明确错误。
func (s *DesktopService) ClipboardText(ctx context.Context, operation, text string) (string, error) {
	if err := host.Require(ctx, "clipboard"); err != nil {
		return "", err
	}
	return s.desktop.ClipboardText(operation, text)
}

// ClipboardImage 读取或写入 PNG 剪贴板内容，write 为 false 时 data 可为空。
func (s *DesktopService) ClipboardImage(ctx context.Context, data []byte, write bool) ([]byte, error) {
	if err := host.Require(ctx, "clipboard"); err != nil {
		return nil, err
	}
	return s.desktop.ClipboardImage(ctx, data, write)
}

// Notify 发送系统通知，点击后唤醒主窗口并尝试进入指定站内路由。
func (s *DesktopService) Notify(ctx context.Context, title, body, route string) error {
	if err := host.Require(ctx, "notifications"); err != nil {
		return err
	}
	return s.desktop.Notify(title, body, route)
}

// SetShortcut 注册或注销应用内、全局快捷键；全局冲突返回 E_CONFLICT。
func (s *DesktopService) SetShortcut(ctx context.Context, key, route string, global, enable bool) error {
	if err := host.Require(ctx, "shortcuts"); err != nil {
		return err
	}
	return s.desktop.SetShortcut(ctx, key, route, global, enable)
}

// ConfigureProtocol 显式注册或移除固定 hybrid-infra 协议，不在应用启动时自动注册。
func (s *DesktopService) ConfigureProtocol(ctx context.Context, enable bool) error {
	if err := host.Require(ctx, "protocol"); err != nil {
		return err
	}
	return s.desktop.ConfigureProtocol(enable)
}

// Credential 使用系统凭据库读取、保存或删除当前来源的秘密，不经过普通 KV 存储。
func (s *DesktopService) Credential(ctx context.Context, key, value, operation string) (string, error) {
	if err := host.Require(ctx, "credentials"); err != nil {
		return "", err
	}
	return s.desktop.Credential(ctx, key, value, operation)
}

// GetSettings 返回当前配置、待重启配置与即时偏好，便于业务界面区分生效时机。
func (s *DesktopService) GetSettings(ctx context.Context) (host.SettingsSnapshot, error) {
	if err := host.Require(ctx, "settings"); err != nil {
		return host.SettingsSnapshot{}, err
	}
	return s.desktop.GetSettings()
}

// SaveSettings 校验并保存完整启动配置，权限、窗口等设置须在重启后生效。
func (s *DesktopService) SaveSettings(ctx context.Context, config setting.PkgInfo) (host.SettingsSnapshot, error) {
	if err := host.Require(ctx, "settings"); err != nil {
		return host.SettingsSnapshot{}, err
	}
	return s.desktop.SaveSettings(config)
}

// SavePreferences 保存主题、语言和通知偏好，无须重启。
func (s *DesktopService) SavePreferences(ctx context.Context, p host.Preferences) (host.Preferences, error) {
	if err := host.Require(ctx, "settings"); err != nil {
		return host.Preferences{}, err
	}
	return s.desktop.SavePreferences(p)
}

// ResetPreferences 将业务界面偏好恢复为默认值，不修改安全配置和系统凭据。
func (s *DesktopService) ResetPreferences(ctx context.Context) (host.Preferences, error) {
	if err := host.Require(ctx, "settings"); err != nil {
		return host.Preferences{}, err
	}
	return s.desktop.ResetPreferences()
}
