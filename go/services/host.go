package services

import (
	"context"
	"hybrid-infra/go/host"
	"hybrid-infra/go/utils"
)

// HostService 是前端查询宿主状态、登记任务和请求退出的服务入口，先检查权限再调用管理器。
type HostService struct{ controller *host.Controller }

// OpenDeveloperDocs 检查 host 能力和原生窗口归属后打开固定开发文档与示例窗口，不接受目标 URL 或任意窗口选项。
func (s *HostService) OpenDeveloperDocs(ctx context.Context) (host.WindowInfo, error) {
	if err := host.Require(ctx, "host"); err != nil {
		return host.WindowInfo{}, err
	}
	_, window := host.CallerIdentity(ctx)
	if window == "" {
		return host.WindowInfo{}, host.Fail("E_PERMISSION_DENIED", "请从原生桌面窗口打开开发文档")
	}
	return s.controller.OpenDeveloperDocs()
}

// GetStartupState 检查 host 能力后读取启动状态，管理器还会验证调用者确为内置启动屏。
func (s *HostService) GetStartupState(ctx context.Context) (host.StartupState, error) {
	if err := host.Require(ctx, "host"); err != nil {
		return host.StartupState{}, err
	}
	return s.controller.GetStartupState(ctx)
}

// DismissSplash 检查 host 能力后跳过启动屏，不能通过参数指定或操控其他窗口。
func (s *HostService) DismissSplash(ctx context.Context) error {
	if err := host.Require(ctx, "host"); err != nil {
		return err
	}
	return s.controller.DismissSplash(ctx)
}

// GetEnvironment 通过真实调用窗口判断桌面外壳环境；普通浏览器即使连接到宿主也不会被误判为原生窗口。
func (s *HostService) GetEnvironment(ctx context.Context) (host.EnvironmentInfo, error) {
	if err := host.Require(ctx, "host"); err != nil {
		return host.EnvironmentInfo{}, err
	}
	return s.controller.Environment(ctx), nil
}

// GetInfo 检查 host 权限后返回宿主版本、平台和能力，供 SDK 建立连接时确认兼容性。
func (s *HostService) GetInfo(ctx context.Context) (map[string]any, error) {
	if err := host.Require(ctx, "host"); err != nil {
		return nil, err
	}
	return s.controller.HostInfo(ctx), nil
}

// GetLoadState 检查 host 权限后查询当前调用窗口的加载状态，前端不能用此接口指定其他窗口。
func (s *HostService) GetLoadState(ctx context.Context) (host.WindowInfo, error) {
	if err := host.Require(ctx, "host"); err != nil {
		return host.WindowInfo{}, err
	}
	return s.controller.LoadState(ctx)
}

// Retry 检查 host 权限后重试当前窗口；home 为 true 时返回内置首页，仍会执行未完成任务检查。
func (s *HostService) Retry(ctx context.Context, home bool) error {
	if err := host.Require(ctx, "host"); err != nil {
		return err
	}
	return s.controller.RetryCaller(ctx, home)
}

// Ready 检查 host 权限后通知宿主当前页面已经就绪，用于停止加载超时计时器。
func (s *HostService) Ready(ctx context.Context) error {
	if err := host.Require(ctx, "host"); err != nil {
		return err
	}
	return s.controller.PageReady(ctx)
}

// BeginTask 检查 lifecycle 权限后为当前窗口登记任务，登记期间该任务会参与退出保护。
func (s *HostService) BeginTask(ctx context.Context, label string) (host.Task, error) {
	if err := host.Require(ctx, "lifecycle"); err != nil {
		return host.Task{}, err
	}
	return s.controller.BeginTask(ctx, label)
}

// EndTask 检查 lifecycle 权限后释放当前窗口的任务；任务归属由宿主管理器再次核对。
func (s *HostService) EndTask(ctx context.Context, id string) error {
	if err := host.Require(ctx, "lifecycle"); err != nil {
		return err
	}
	return s.controller.EndTask(ctx, id)
}

// GetPendingTasks 检查 lifecycle 权限后返回未完成任务列表，供管理页面说明哪些任务仍在阻止退出。
func (s *HostService) GetPendingTasks(ctx context.Context) ([]host.Task, error) {
	if err := host.Require(ctx, "lifecycle"); err != nil {
		return nil, err
	}
	return s.controller.Tasks(), nil
}

// Quit 检查 lifecycle 权限后请求退出应用；存在未完成任务时返回错误，不直接绕过退出保护。
func (s *HostService) Quit(ctx context.Context) error {
	if err := host.Require(ctx, "lifecycle"); err != nil {
		return err
	}
	return s.controller.Quit()
}

// WindowService 是前端管理具名窗口的服务入口，不在这里重复实现窗口恢复和导航策略。
type WindowService struct{ controller *host.Controller }

// Open 检查 windows 权限后创建或复用具名窗口，新窗口继承调用者的权限上限。
func (s *WindowService) Open(ctx context.Context, options utils.NewWindowOptions) (host.WindowInfo, error) {
	if err := host.Require(ctx, "windows"); err != nil {
		return host.WindowInfo{}, err
	}
	return s.controller.OpenFor(ctx, options)
}

// List 检查 windows 权限后返回当前窗口列表，包括加载状态、位置及可见性等信息。
func (s *WindowService) List(ctx context.Context) ([]host.WindowInfo, error) {
	if err := host.Require(ctx, "windows"); err != nil {
		return nil, err
	}
	return s.controller.List(), nil
}

// Get 检查 windows 权限后查询指定 ID 的窗口；不存在时返回明确错误。
func (s *WindowService) Get(ctx context.Context, id string) (host.WindowInfo, error) {
	if err := host.Require(ctx, "windows"); err != nil {
		return host.WindowInfo{}, err
	}
	return s.controller.Info(id)
}

// Focus 检查 windows 权限后显示并聚焦指定窗口，已最小化的窗口会先恢复。
func (s *WindowService) Focus(ctx context.Context, id string) error {
	if err := host.Require(ctx, "windows"); err != nil {
		return err
	}
	return s.controller.Focus(id)
}

// Close 检查 windows 权限后请求关闭指定窗口；有未完成任务时保留窗口并返回错误。
func (s *WindowService) Close(ctx context.Context, id string) error {
	if err := host.Require(ctx, "windows"); err != nil {
		return err
	}
	return s.controller.Close(id)
}
