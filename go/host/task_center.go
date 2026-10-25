package host

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"
)

// ManagedTask 是统一任务中心的公开快照，不持久化命令参数、下载地址、文件 token 或函数代码。
type ManagedTask struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	State     string `json:"state"`
	Progress  int    `json:"progress"`
	Message   string `json:"message"`
	Error     *Error `json:"error,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	Attempt   int    `json:"attempt"`
	CanCancel bool   `json:"canCancel"`
	CanRetry  bool   `json:"canRetry"`
}

// storedTask 保存任务来源归属，供重启后读取历史；敏感执行参数只放在内存 taskEntry 中。
type storedTask struct {
	Origin string      `json:"origin"`
	Window string      `json:"window"`
	Info   ManagedTask `json:"info"`
}
type taskEntry struct {
	storedTask
	ctx                        context.Context
	guard                      Task
	run                        func(context.Context, *taskEntry) error
	cancel                     context.CancelFunc
	nativeCancel               func()
	command                    *CommandOptions
	downloadURL, downloadToken string
	lastBeat                   time.Time
}
type queueInvocationKey struct{}

// TaskCenter 管理最多两项并发和 100 项活动任务，500 条持久化历史；前端业务函数通过租约领取任务并协作取消。
type TaskCenter struct {
	mu         sync.Mutex
	controller *Controller
	database   *BusinessDatabase
	commands   *CommandManager
	desktop    *Desktop
	entries    map[string]*taskEntry
	stop       chan struct{}
	wg         sync.WaitGroup
	closed     bool
}

// taskTerminal 判断任务是否已经结束，排队、等待领取和正在取消都仍占用相应退出保护。
func taskTerminal(state string) bool {
	return state == "completed" || state == "failed" || state == "cancelled" || state == "interrupted"
}

// validateStoredTask 在恢复或读取历史时拒绝无效状态、超长文本和不合法归属，不把磁盘数据变成可执行指令。
func validateStoredTask(value storedTask) error {
	if value.Info.Attempt < 1 || value.Info.CreatedAt < 0 || value.Info.UpdatedAt < 0 || value.Info.Error != nil && (len(value.Info.Error.Code) > 128 || len(value.Info.Error.Message) > 2048) {
		return Fail("E_BACKUP_INVALID", "任务历史字段无效")
	}
	if !validID.MatchString(value.Info.ID) || Origin(value.Origin) != value.Origin || value.Origin == "" || !validID.MatchString(value.Window) || len(value.Info.Title) > 480 || len(value.Info.Message) > 2048 || value.Info.Progress < -1 || value.Info.Progress > 100 {
		return Fail("E_BACKUP_INVALID", "任务历史数据无效")
	}
	switch value.Info.Kind {
	case "business", "shell", "download":
	default:
		return Fail("E_BACKUP_INVALID", "任务历史类型无效")
	}
	switch value.Info.State {
	case "queued", "ready", "running", "cancelling", "completed", "failed", "cancelled", "interrupted":
	default:
		return Fail("E_BACKUP_INVALID", "任务历史状态无效")
	}
	return nil
}

// saveTaskHistory 原子写入一条任务快照，不保存可执行参数；数据库锁与队列锁的调用顺序始终为队列在外。
func (d *BusinessDatabase) saveTaskHistory(value storedTask) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	value.Info.CanCancel = false
	value.Info.CanRetry = false
	data, err := json.Marshal(value.Info)
	if err != nil {
		return err
	}
	_, err = d.db.Exec("INSERT INTO task_history(id,origin,window,info) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET info=excluded.info", value.Info.ID, value.Origin, value.Window, string(data))
	return databaseError(err)
}

// loadTaskHistoryLocked 在调用者持有数据库锁时加载有限历史，坏记录会使初始化明确失败而不是静默丢失数据。
func (d *BusinessDatabase) loadTaskHistoryLocked() ([]storedTask, error) {
	rows, err := d.db.Query("SELECT origin,window,info FROM task_history ORDER BY id LIMIT 1001")
	if err != nil {
		return nil, databaseError(err)
	}
	defer rows.Close()
	values := []storedTask{}
	for rows.Next() {
		var value storedTask
		var data string
		if err = rows.Scan(&value.Origin, &value.Window, &data); err != nil {
			return nil, databaseError(err)
		}
		if json.Unmarshal([]byte(data), &value.Info) != nil {
			return nil, Fail("E_DATABASE", "任务历史损坏")
		}
		if err = validateStoredTask(value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if len(values) > 1000 {
		return nil, Fail("E_DATABASE", "任务历史数量超限")
	}
	return values, databaseError(rows.Err())
}

// NewTaskCenter 恢复历史并将上次未完成的任务标成 interrupted，不在重启后自动重放命令、下载或 JS 函数。
func NewTaskCenter(c *Controller, db *BusinessDatabase, commands *CommandManager, desktop *Desktop) (*TaskCenter, error) {
	m := &TaskCenter{controller: c, database: db, commands: commands, desktop: desktop, entries: map[string]*taskEntry{}, stop: make(chan struct{})}
	db.mu.Lock()
	history, err := db.loadTaskHistoryLocked()
	db.mu.Unlock()
	if err != nil {
		return nil, err
	}
	for _, value := range history {
		if !taskTerminal(value.Info.State) {
			value.Info.State = "interrupted"
			value.Info.Error = &Error{Code: "E_INTERRUPTED", Message: "上次运行未完成，请重新提交"}
			value.Info.UpdatedAt = time.Now().UnixMilli()
			if err = db.saveTaskHistory(value); err != nil {
				return nil, err
			}
		}
		m.entries[value.Info.ID] = &taskEntry{storedTask: value}
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(150 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.dispatch()
			case <-m.stop:
				return
			}
		}
	}()
	return m, nil
}

// taskContext 复制已经校验的身份到后台上下文，不沿用返回即取消的 RPC 上下文；队列自身负责取消与窗口清理。
func taskContext(ctx context.Context) context.Context {
	p, _ := ctx.Value(principalKey{}).(Principal)
	return context.WithValue(context.Background(), principalKey{}, p)
}

// saveLocked 更新时间并持久化状态；写盘失败记为固定错误，不能向界面伪装保存成功。
func (m *TaskCenter) saveLocked(entry *taskEntry) error {
	entry.Info.UpdatedAt = time.Now().UnixMilli()
	return m.database.saveTaskHistory(entry.storedTask)
}

// finishLocked 将任务置为终态并释放退出登记，仅在队列锁内调用，重复完成不会重复释放。
func (m *TaskCenter) finishLocked(entry *taskEntry, state, message string, err error) {
	if taskTerminal(entry.Info.State) {
		return
	}
	if entry.Info.State == "cancelling" {
		state = "cancelled"
	}
	entry.Info.State = state
	entry.Info.Message = message
	if state == "completed" {
		entry.Info.Progress = 100
	}
	if err != nil {
		var value *Error
		if errors.As(err, &value) && value != nil {
			entry.Info.Error = &Error{Code: value.Code, Message: value.Message}
		} else {
			entry.Info.Error = &Error{Code: "E_TASK_FAILED", Message: "后台任务执行失败"}
		}
	}
	if entry.guard.ID != "" {
		_ = m.controller.EndTask(entry.ctx, entry.guard.ID)
		entry.guard = Task{}
	}
	if e := m.saveLocked(entry); e != nil {
		entry.Info.Error = &Error{Code: "E_DATABASE", Message: "任务状态保存失败"}
	}
}

// create 登记排队任务并先建立退出保护；历史达到 500 条时仅清理最旧终态，活动任务最多 100 项。
func (m *TaskCenter) create(ctx context.Context, kind, title string, observed ...bool) (*taskEntry, error) {
	if len(observed) == 0 {
		if err := Require(ctx, "tasks"); err != nil {
			return nil, err
		}
	}
	if caller(ctx) == "" {
		return nil, Fail("E_PERMISSION_DENIED", "任务必须来自原生窗口")
	}
	var guard Task
	var err error
	if len(observed) == 0 {
		guard, err = m.controller.BeginTask(ctx, title)
		if err != nil {
			return nil, err
		}
	}
	if guard.ID != "" {
		title = guard.Label
	} else {
		runes := []rune(title)
		if len(runes) > 120 {
			title = string(runes[:120])
		}
	}
	context := taskContext(ctx)
	origin, window := CallerIdentity(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		_ = m.controller.EndTask(context, guard.ID)
		return nil, Fail("E_SHUTTING_DOWN", "任务中心正在退出")
	}
	if len(m.entries) >= 500 {
		var oldest *taskEntry
		for _, entry := range m.entries {
			if taskTerminal(entry.Info.State) && (oldest == nil || entry.Info.CreatedAt < oldest.Info.CreatedAt) {
				oldest = entry
			}
		}
		if oldest == nil {
			_ = m.controller.EndTask(context, guard.ID)
			return nil, Fail("E_BUSY", "任务历史和活动数量已达上限")
		}
		m.database.mu.Lock()
		_, err = m.database.db.Exec("DELETE FROM task_history WHERE id=?", oldest.Info.ID)
		m.database.mu.Unlock()
		if err != nil {
			_ = m.controller.EndTask(context, guard.ID)
			return nil, databaseError(err)
		}
		delete(m.entries, oldest.Info.ID)
	}
	id, err := randomID()
	if err != nil {
		_ = m.controller.EndTask(context, guard.ID)
		return nil, err
	}
	now := time.Now().UnixMilli()
	entry := &taskEntry{storedTask: storedTask{Origin: origin, Window: window, Info: ManagedTask{ID: id, Kind: kind, Title: title, State: "queued", Progress: -1, CreatedAt: now, UpdatedAt: now, Attempt: 1}}, ctx: context, guard: guard, lastBeat: time.Now()}
	if err = m.database.saveTaskHistory(entry.storedTask); err != nil {
		_ = m.controller.EndTask(context, guard.ID)
		return nil, err
	}
	m.entries[id] = entry
	return entry, nil
}

// CreateBusiness 创建由前端 SDK 领取的业务任务，不把回调函数或业务代码发送给 Go 或写入数据库。
func (m *TaskCenter) CreateBusiness(ctx context.Context, title string) (ManagedTask, error) {
	entry, err := m.create(ctx, "business", title)
	if err != nil {
		return ManagedTask{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.infoLocked(ctx, entry), nil
}

// observeNative 自动记录旧 Shell/下载接口创建的任务；显式队列内部调用带标记，避免重复记录或增加退出登记。
func (m *TaskCenter) observeNative(ctx context.Context, kind, title string, command *CommandOptions, raw, token string, cancel func()) string {
	if ctx.Value(queueInvocationKey{}) == true {
		return ""
	}
	entry, err := m.create(ctx, kind, title, true)
	if err != nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry.Info.State = "running"
	entry.command = command
	entry.downloadURL = raw
	entry.downloadToken = token
	entry.nativeCancel = cancel
	_ = m.saveLocked(entry)
	return entry.Info.ID
}

// observeProgress 将原接口的真实状态同步到统一历史，不记录输出或 URL；队列关闭后不再写已关闭的数据库。
func (m *TaskCenter) observeProgress(id string, progress int, state string, err error) {
	if id == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.entries[id]
	if m.closed || entry == nil || taskTerminal(entry.Info.State) {
		return
	}
	if state != "running" {
		m.finishLocked(entry, state, "", err)
		return
	}
	if progress != entry.Info.Progress {
		entry.Info.Progress = progress
		_ = m.saveLocked(entry)
	}
}

// EnqueueShell 校验白名单和能力后将命令排队；执行时仍通过原有进程管理器，不直接运行拼接脚本。
func (m *TaskCenter) EnqueueShell(ctx context.Context, title string, options CommandOptions) (ManagedTask, error) {
	if _, err := m.commands.command(ctx, options); err != nil {
		return ManagedTask{}, err
	}
	entry, err := m.create(ctx, "shell", title)
	if err != nil {
		return ManagedTask{}, err
	}
	m.mu.Lock()
	entry.command = &options
	entry.run = m.runShell
	info := entry.Info
	m.mu.Unlock()
	return info, nil
}

// EnqueueDownload 校验当前窗口的保存凭证后排队；token 在任务完成前必须保留，未授权路径不会进入队列。
func (m *TaskCenter) EnqueueDownload(ctx context.Context, title, raw, token string) (ManagedTask, error) {
	if err := Require(ctx, "downloads"); err != nil {
		return ManagedTask{}, err
	}
	if err := Require(ctx, "files"); err != nil {
		return ManagedTask{}, err
	}
	if err := downloadURL(raw); err != nil {
		return ManagedTask{}, err
	}
	if _, err := m.desktop.file(ctx, token, true); err != nil {
		return ManagedTask{}, err
	}
	entry, err := m.create(ctx, "download", title)
	if err != nil {
		return ManagedTask{}, err
	}
	m.mu.Lock()
	entry.downloadURL, entry.downloadToken = raw, token
	entry.run = m.runDownload
	info := entry.Info
	m.mu.Unlock()
	return info, nil
}

// dispatch 按提交时间选择任务；业务任务先保留执行槽等待 SDK 领取，租约过期会中断，不占用队列无限等待。
func (m *TaskCenter) dispatch() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	running := 0
	queue := []*taskEntry{}
	for _, entry := range m.entries {
		if entry.Info.Kind == "business" && !taskTerminal(entry.Info.State) && time.Since(entry.lastBeat) > 30*time.Second {
			m.finishLocked(entry, "interrupted", "前端执行会话已失联", Fail("E_INTERRUPTED", "前端执行会话已失联"))
			continue
		}
		if entry.Info.State == "running" || entry.Info.State == "ready" || entry.Info.State == "cancelling" {
			running++
		}
		if entry.Info.State == "queued" && (entry.Info.Kind == "business" || entry.run != nil) {
			queue = append(queue, entry)
		}
	}
	sort.Slice(queue, func(i, j int) bool { return queue[i].Info.CreatedAt < queue[j].Info.CreatedAt })
	for _, entry := range queue {
		if running >= 2 {
			break
		}
		running++
		if entry.Info.Kind == "business" {
			entry.Info.State = "ready"
			_ = m.saveLocked(entry)
			continue
		}
		entry.Info.State = "running"
		ctx, cancel := context.WithCancel(context.WithValue(entry.ctx, queueInvocationKey{}, true))
		entry.cancel = cancel
		_ = m.saveLocked(entry)
		m.wg.Add(1)
		go m.execute(ctx, entry)
	}
}

// execute 在锁外执行原生任务；结束后统一记录结果，取消不撤回已经写入的外部文件或程序副作用。
func (m *TaskCenter) execute(ctx context.Context, entry *taskEntry) {
	defer m.wg.Done()
	err := entry.run(ctx, entry)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	state := "completed"
	if err != nil {
		state = "failed"
	}
	if ctx.Err() != nil {
		state = "cancelled"
	}
	m.finishLocked(entry, state, "", err)
}

// runShell 将原有命令任务接入队列，取消会传到进程管理器；输出继续通过原 Shell 读取接口获取，不写入任务历史。
func (m *TaskCenter) runShell(ctx context.Context, entry *taskEntry) error {
	m.mu.Lock()
	var options CommandOptions
	if entry.command != nil {
		options = *entry.command
	}
	m.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	job, err := m.commands.Start(ctx, options)
	if err != nil {
		return err
	}
	defer func() {
		// 进程停止需要等待 Wait 回收；先取消再等终态，避免过早 Remove 留下占用历史配额的记录。
		_ = m.commands.Cancel(entry.ctx, job.ID)
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
			if m.commands.Remove(entry.ctx, job.ID) == nil {
				return
			}
			time.Sleep(30 * time.Millisecond)
		}
	}()
	for {
		value, e := m.commands.Read(entry.ctx, job.ID, 0, 0)
		if e != nil {
			return e
		}
		if value.Job.State != "running" {
			if value.Job.State == "completed" {
				return nil
			}
			if value.Job.Error != nil {
				return value.Job.Error
			}
			return Fail("E_COMMAND_FAILED", "命令执行失败")
		}
		select {
		case <-ctx.Done():
			_ = m.commands.Cancel(entry.ctx, job.ID)
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// runDownload 复用现有临时文件和原子提交流程，定期同步真实字节进度；取消后请求下载器停止。
func (m *TaskCenter) runDownload(ctx context.Context, entry *taskEntry) error {
	m.mu.Lock()
	raw, token := entry.downloadURL, entry.downloadToken
	m.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	job, err := m.desktop.StartDownload(ctx, raw, token)
	if err != nil {
		return err
	}
	defer func() {
		_ = m.desktop.CancelDownload(entry.ctx, job.ID)
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
			if m.desktop.RemoveDownload(entry.ctx, job.ID) == nil {
				return
			}
			time.Sleep(30 * time.Millisecond)
		}
	}()
	for {
		m.desktop.mu.Lock()
		current := m.desktop.downloads[job.ID]
		if current == nil {
			m.desktop.mu.Unlock()
			return Fail("E_INTERRUPTED", "下载记录已被清理")
		}
		value := current.DownloadInfo
		m.desktop.mu.Unlock()
		if value.State != "running" {
			if value.State == "completed" {
				return nil
			}
			if value.Error != nil {
				return value.Error
			}
			return Fail("E_CANCELLED", "下载已取消")
		}
		progress := -1
		if value.Total > 0 {
			progress = int(value.Received * 100 / value.Total)
		}
		m.mu.Lock()
		if !taskTerminal(entry.Info.State) && entry.Info.Progress != progress {
			entry.Info.Progress = progress
			_ = m.saveLocked(entry)
		}
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			_ = m.desktop.CancelDownload(entry.ctx, job.ID)
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// allowed 区分任务所有者与管理员；管理员可看全局状态并取消，但重试仍要求本人和原能力。
func (m *TaskCenter) allowed(ctx context.Context, entry *taskEntry, ownerOnly bool) bool {
	if entry == nil {
		return false
	}
	origin, window := CallerIdentity(ctx)
	if entry.Origin == origin && entry.Window == window {
		return true
	}
	return !ownerOnly && Require(ctx, "tasks.manage") == nil
}

// infoLocked 为当前调用者计算可操作按钮，不把原任务参数或 token 暴露给其他窗口。
func (m *TaskCenter) infoLocked(ctx context.Context, entry *taskEntry) ManagedTask {
	info := entry.Info
	info.CanCancel = !taskTerminal(info.State) && m.allowed(ctx, entry, false)
	info.CanRetry = taskTerminal(info.State) && info.State != "completed" && m.allowed(ctx, entry, true) && (entry.command != nil || entry.downloadURL != "")
	return info
}

// List 返回本窗口任务，拥有 tasks.manage 时可查看本应用全部任务；按创建时间降序，历史最多 500 条。
func (m *TaskCenter) List(ctx context.Context) ([]ManagedTask, error) {
	if err := Require(ctx, "tasks"); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	values := []ManagedTask{}
	for _, entry := range m.entries {
		if m.allowed(ctx, entry, false) {
			values = append(values, m.infoLocked(ctx, entry))
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].CreatedAt > values[j].CreatedAt })
	return values, nil
}

// Poll 由业务 SDK 维持执行租约并领取就绪任务；管理员看列表不会替其他页面续租或领取回调。
func (m *TaskCenter) Poll(ctx context.Context, id string) (ManagedTask, error) {
	if err := Require(ctx, "tasks"); err != nil {
		return ManagedTask{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.entries[id]
	if !m.allowed(ctx, entry, true) {
		return ManagedTask{}, Fail("E_PERMISSION_DENIED", "任务不属于当前窗口")
	}
	entry.lastBeat = time.Now()
	if entry.Info.Kind == "business" && entry.Info.State == "ready" {
		entry.Info.State = "running"
		_ = m.saveLocked(entry)
	}
	return m.infoLocked(ctx, entry), nil
}

// Report 更新本窗口业务任务的进度与短消息，不能改动 Shell/下载器拥有的状态。
func (m *TaskCenter) Report(ctx context.Context, id string, progress int, message string) (ManagedTask, error) {
	if err := Require(ctx, "tasks"); err != nil {
		return ManagedTask{}, err
	}
	if progress < -1 || progress > 100 || len(message) > 512 {
		return ManagedTask{}, Fail("E_INVALID_ARGUMENT", "进度应为 -1–100，消息最多 512 字节")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.entries[id]
	if !m.allowed(ctx, entry, true) || entry.Info.Kind != "business" {
		return ManagedTask{}, Fail("E_PERMISSION_DENIED", "不能更新此任务")
	}
	if entry.Info.State != "running" && entry.Info.State != "cancelling" {
		return ManagedTask{}, Fail("E_BUSY", "任务不在运行中")
	}
	entry.lastBeat = time.Now()
	entry.Info.Progress = progress
	entry.Info.Message = message
	if err := m.saveLocked(entry); err != nil {
		return ManagedTask{}, err
	}
	return m.infoLocked(ctx, entry), nil
}

// Finish 接收前端业务的结束状态，取消请求优先于成功回包；重复结束安全返回，不会重新激活任务。
func (m *TaskCenter) Finish(ctx context.Context, id, state, message string) (ManagedTask, error) {
	if err := Require(ctx, "tasks"); err != nil {
		return ManagedTask{}, err
	}
	if state != "completed" && state != "failed" && state != "cancelled" || len(message) > 512 {
		return ManagedTask{}, Fail("E_INVALID_ARGUMENT", "结束状态或消息无效")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.entries[id]
	if !m.allowed(ctx, entry, true) || entry.Info.Kind != "business" {
		return ManagedTask{}, Fail("E_PERMISSION_DENIED", "不能结束此任务")
	}
	if !taskTerminal(entry.Info.State) && entry.Info.State != "running" && entry.Info.State != "cancelling" {
		return ManagedTask{}, Fail("E_BUSY", "任务尚未领取执行")
	}
	var err error
	if state == "failed" {
		err = Fail("E_BUSINESS_TASK", message)
	}
	m.finishLocked(entry, state, message, err)
	return m.infoLocked(ctx, entry), nil
}

// Cancel 取消排队任务或请求停止正在运行的任务；前端函数只有返回或租约失联后才释放保护，不假装能强杀 JS。
func (m *TaskCenter) Cancel(ctx context.Context, id string) error {
	if err := Require(ctx, "tasks"); err != nil {
		return err
	}
	m.mu.Lock()
	entry := m.entries[id]
	if !m.allowed(ctx, entry, false) {
		m.mu.Unlock()
		return Fail("E_PERMISSION_DENIED", "任务不可操作")
	}
	var cancel context.CancelFunc
	var native func()
	if entry.Info.State == "queued" || entry.Info.State == "ready" {
		m.finishLocked(entry, "cancelled", "已取消排队", nil)
	} else if !taskTerminal(entry.Info.State) {
		entry.Info.State = "cancelling"
		_ = m.saveLocked(entry)
		cancel = entry.cancel
		native = entry.nativeCancel
	}
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if native != nil {
		native()
	}
	return nil
}

// Retry 重新提交本窗口的失败原生任务并重新校验权限；重启后的历史没有执行参数，业务函数由前端 SDK 自行重试。
func (m *TaskCenter) Retry(ctx context.Context, id string) (ManagedTask, error) {
	if err := Require(ctx, "tasks"); err != nil {
		return ManagedTask{}, err
	}
	m.mu.Lock()
	entry := m.entries[id]
	if !m.allowed(ctx, entry, true) || !taskTerminal(entry.Info.State) || entry.Info.State == "completed" {
		m.mu.Unlock()
		return ManagedTask{}, Fail("E_PERMISSION_DENIED", "仅能重试本窗口的失败或取消任务")
	}
	title, command, raw, token, attempt := entry.Info.Title, entry.command, entry.downloadURL, entry.downloadToken, entry.Info.Attempt
	m.mu.Unlock()
	var result ManagedTask
	var err error
	if command != nil {
		result, err = m.EnqueueShell(ctx, title, *command)
	} else if raw != "" {
		result, err = m.EnqueueDownload(ctx, title, raw, token)
	} else {
		return result, Fail("E_UNSUPPORTED", "此历史任务需要在原业务页面重新提交")
	}
	if err == nil {
		m.mu.Lock()
		m.entries[result.ID].Info.Attempt = attempt + 1
		_ = m.saveLocked(m.entries[result.ID])
		result = m.infoLocked(ctx, m.entries[result.ID])
		m.mu.Unlock()
	}
	return result, err
}

// Remove 删除可见的终态历史，不删除命令产物、下载文件或数据库业务记录。
func (m *TaskCenter) Remove(ctx context.Context, id string) error {
	if err := Require(ctx, "tasks"); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.entries[id]
	if !m.allowed(ctx, entry, false) {
		return Fail("E_PERMISSION_DENIED", "任务不可操作")
	}
	if !taskTerminal(entry.Info.State) {
		return Fail("E_BUSY", "请先停止任务")
	}
	m.database.mu.Lock()
	_, err := m.database.db.Exec("DELETE FROM task_history WHERE id=?", id)
	m.database.mu.Unlock()
	if err == nil {
		delete(m.entries, id)
	}
	return databaseError(err)
}

// CloseWindow 中断已关闭窗口的任务并清掉内存重试参数，防止同名新窗口继承旧文件凭证或脚本。
func (m *TaskCenter) CloseWindow(window string) {
	m.mu.Lock()
	var cancels []func()
	for _, entry := range m.entries {
		if entry.Window != window {
			continue
		}
		if entry.cancel != nil {
			cancels = append(cancels, entry.cancel)
		}
		if entry.nativeCancel != nil {
			cancels = append(cancels, entry.nativeCancel)
		}
		if !taskTerminal(entry.Info.State) {
			m.finishLocked(entry, "interrupted", "所属窗口已关闭", Fail("E_INTERRUPTED", "所属窗口已关闭"))
		}
		entry.command = nil
		entry.downloadURL = ""
		entry.downloadToken = ""
	}
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// Shutdown 禁止新任务，落盘中断状态，取消原生执行并等待队列协程退出；之后才可关闭数据库。
func (m *TaskCenter) Shutdown() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	close(m.stop)
	var cancels []func()
	for _, entry := range m.entries {
		if entry.cancel != nil {
			cancels = append(cancels, entry.cancel)
		}
		if entry.nativeCancel != nil {
			cancels = append(cancels, entry.nativeCancel)
		}
		if !taskTerminal(entry.Info.State) {
			m.finishLocked(entry, "interrupted", "应用已退出", Fail("E_INTERRUPTED", "应用已退出"))
		}
	}
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	m.wg.Wait()
}
