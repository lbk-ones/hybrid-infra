package services

import (
	"context"
	"encoding/json"

	"hybrid-infra/go/host"
)

// MaintenanceService 暴露诊断、备份和签名更新，三类能力分别授权。
type MaintenanceService struct{ manager *host.Maintenance }

// GetDiagnostics 返回不包含业务值、文件路径和页面 URL 的诊断摘要。
func (s *MaintenanceService) GetDiagnostics(ctx context.Context) (map[string]any, error) {
	if e := host.Require(ctx, "diagnostics"); e != nil {
		return nil, e
	}
	return s.manager.Diagnostics(), nil
}

// ExportDiagnostics 通过系统保存凭证导出诊断 JSON，需要同时拥有文件和诊断权限。
func (s *MaintenanceService) ExportDiagnostics(ctx context.Context, token string) error {
	if e := host.Require(ctx, "diagnostics"); e != nil {
		return e
	}
	if e := host.Require(ctx, "files"); e != nil {
		return e
	}
	b, e := json.MarshalIndent(s.manager.Diagnostics(), "", "  ")
	if e != nil {
		return e
	}
	return s.manager.ExportDiagnostics(ctx, token, b)
}

// ListBackups 返回校验通过的本地备份，不接受任意磁盘目录。
func (s *MaintenanceService) ListBackups(ctx context.Context) ([]host.BackupInfo, error) {
	if e := host.Require(ctx, "backups"); e != nil {
		return nil, e
	}
	return s.manager.ListBackups(), nil
}

// CreateBackup 保存业务 KV、偏好和窗口状态，不包含权限配置与系统凭据。
func (s *MaintenanceService) CreateBackup(ctx context.Context) (host.BackupInfo, error) {
	if e := host.Require(ctx, "backups"); e != nil {
		return host.BackupInfo{}, e
	}
	return s.manager.CreateBackup()
}

// StageRestore 验证指定备份并排队，在下次启动时恢复。
func (s *MaintenanceService) StageRestore(ctx context.Context, id string) error {
	if e := host.Require(ctx, "backups"); e != nil {
		return e
	}
	return s.manager.StageRestore(id)
}

// GetUpdateState 返回更新状态与下载进度。
func (s *MaintenanceService) GetUpdateState(ctx context.Context) (host.UpdateState, error) {
	if e := host.Require(ctx, "updates"); e != nil {
		return host.UpdateState{}, e
	}
	return s.manager.UpdateStatus(), nil
}

// CheckUpdates 获取固定信任源的签名清单并检查兼容性。
func (s *MaintenanceService) CheckUpdates(ctx context.Context) (host.UpdateState, error) {
	if e := host.Require(ctx, "updates"); e != nil {
		return host.UpdateState{}, e
	}
	return s.manager.CheckUpdates(ctx)
}

// DownloadUpdate 后台下载已验证版本，任务期间阻止退出。
func (s *MaintenanceService) DownloadUpdate(ctx context.Context) error {
	if e := host.Require(ctx, "updates"); e != nil {
		return e
	}
	return s.manager.DownloadUpdate(ctx)
}

// CancelUpdate 取消更新下载并释放任务保护。
func (s *MaintenanceService) CancelUpdate(ctx context.Context) error {
	if e := host.Require(ctx, "updates"); e != nil {
		return e
	}
	s.manager.CancelUpdate()
	return nil
}

// InstallUpdate 安装已验证版本并重启；有业务任务时拒绝执行。
func (s *MaintenanceService) InstallUpdate(ctx context.Context) error {
	if e := host.Require(ctx, "updates"); e != nil {
		return e
	}
	return s.manager.InstallUpdate(false)
}

// RollbackUpdate 回退上一个程序及其数据快照，重启后生效。
func (s *MaintenanceService) RollbackUpdate(ctx context.Context) error {
	if e := host.Require(ctx, "updates"); e != nil {
		return e
	}
	if e := host.Require(ctx, "backups"); e != nil {
		return e
	}
	return s.manager.InstallUpdate(true)
}
