package services

import (
	"context"
	"encoding/json"
	"hybrid-infra/go/host"
)

// DatabaseService 提供来源隔离的业务记录接口，网页不能传数据库路径、执行原始 SQL 或操作其他来源。
type DatabaseService struct{ database *host.BusinessDatabase }

// Get 读取当前来源的集合记录，不存在时返回 E_NOT_FOUND。
func (s *DatabaseService) Get(ctx context.Context, collection, id string) (host.Document, error) {
	return s.database.Get(ctx, collection, id)
}

// Query 按集合分页，支持顶层字段精确过滤和元数据排序，响应大小由宿主控制。
func (s *DatabaseService) Query(ctx context.Context, query host.DocumentQuery) (host.DocumentPage, error) {
	return s.database.Query(ctx, query)
}

// Put 保存 JSON 对象，revision=-1 无条件写入，0 仅创建，正数要求已有版本一致。
func (s *DatabaseService) Put(ctx context.Context, collection, id string, data json.RawMessage, revision int64) (host.Document, error) {
	values, err := s.database.Batch(ctx, []host.DocumentChange{{Operation: "put", Collection: collection, ID: id, Data: data, Revision: revision}})
	if err != nil {
		return host.Document{}, err
	}
	return values[0], nil
}

// Delete 删除一条业务记录，可用 revision 防止删除其他窗口刚修改的数据。
func (s *DatabaseService) Delete(ctx context.Context, collection, id string, revision int64) error {
	_, err := s.database.Batch(ctx, []host.DocumentChange{{Operation: "delete", Collection: collection, ID: id, Revision: revision}})
	return err
}

// Batch 将一组修改原子提交，任何错误或版本冲突都回滚整批。
func (s *DatabaseService) Batch(ctx context.Context, changes []host.DocumentChange) ([]host.Document, error) {
	return s.database.Batch(ctx, changes)
}

// TaskService 将队列、进度和历史暴露给网页，所有资源归属和能力检查仍在任务管理器执行。
type TaskService struct{ center *host.TaskCenter }

// List 查看本窗口任务，tasks.manage 允许查看本应用全部窗口的任务元数据。
func (s *TaskService) List(ctx context.Context) ([]host.ManagedTask, error) {
	return s.center.List(ctx)
}

// Create 登记由前端执行函数的排队任务；推荐使用 Tasks.run 自动领取、续租和收尾。
func (s *TaskService) Create(ctx context.Context, title string) (host.ManagedTask, error) {
	return s.center.CreateBusiness(ctx, title)
}

// Poll 领取就绪业务任务并维持前端租约，不能替其他窗口领取或续租。
func (s *TaskService) Poll(ctx context.Context, id string) (host.ManagedTask, error) {
	return s.center.Poll(ctx, id)
}

// Report 更新业务任务的真实进度和短消息，不执行任务代码。
func (s *TaskService) Report(ctx context.Context, id string, progress int, message string) (host.ManagedTask, error) {
	return s.center.Report(ctx, id, progress, message)
}

// Finish 提交业务函数的最终结果状态，取消请求优先于迟到的成功结果。
func (s *TaskService) Finish(ctx context.Context, id, state, message string) (host.ManagedTask, error) {
	return s.center.Finish(ctx, id, state, message)
}

// EnqueueShell 将经过授权的白名单命令排队，原有 Shell 接口创建的任务也会自动记录历史。
func (s *TaskService) EnqueueShell(ctx context.Context, title string, options host.CommandOptions) (host.ManagedTask, error) {
	return s.center.EnqueueShell(ctx, title, options)
}

// EnqueueDownload 使用本窗口保存凭证排队下载，凭证在任务结束前应保持有效。
func (s *TaskService) EnqueueDownload(ctx context.Context, title, url, token string) (host.ManagedTask, error) {
	return s.center.EnqueueDownload(ctx, title, url, token)
}

// Cancel 取消排队或请求中断运行，前端函数需配合 AbortSignal 停止自己的实际工作。
func (s *TaskService) Cancel(ctx context.Context, id string) error { return s.center.Cancel(ctx, id) }

// Retry 重试本窗口仍有内存执行参数的原生任务，重启后不会自动重放旧命令或下载。
func (s *TaskService) Retry(ctx context.Context, id string) (host.ManagedTask, error) {
	return s.center.Retry(ctx, id)
}

// Remove 删除终态历史，不能删除活动任务，也不删除下载或命令产生的文件。
func (s *TaskService) Remove(ctx context.Context, id string) error { return s.center.Remove(ctx, id) }
