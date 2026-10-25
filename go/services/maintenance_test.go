package services

import (
	"context"
	"errors"
	"hybrid-infra/go/host"
	"testing"
)

// TestMaintenanceRequiresPermission 确认维护入口在没有真实授权上下文时全部拒绝，不触及磁盘或管理器。
func TestMaintenanceRequiresPermission(t *testing.T) {
	s := &MaintenanceService{}
	ctx := context.Background()
	operations := []func() error{func() error { _, e := s.GetDiagnostics(ctx); return e }, func() error { return s.ExportDiagnostics(ctx, "fake") }, func() error { _, e := s.ListBackups(ctx); return e }, func() error { _, e := s.CreateBackup(ctx); return e }, func() error { return s.StageRestore(ctx, "fake") }, func() error { _, e := s.GetUpdateState(ctx); return e }, func() error { _, e := s.CheckUpdates(ctx); return e }, func() error { return s.DownloadUpdate(ctx) }, func() error { return s.CancelUpdate(ctx) }, func() error { return s.InstallUpdate(ctx) }, func() error { return s.RollbackUpdate(ctx) }}
	for _, operation := range operations {
		var value *host.Error
		if err := operation(); !errors.As(err, &value) || value.Code != "E_PERMISSION_DENIED" {
			t.Fatalf("permission bypass: %v", err)
		}
	}
}
