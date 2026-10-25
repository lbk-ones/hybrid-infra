package host

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxDownloadBytes int64 = 2 * 1024 * 1024 * 1024

// DownloadInfo 是下载任务快照；终态为 completed、cancelled 或 failed，progress 可通过 ListDownloads 轮询。
type DownloadInfo struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Name      string    `json:"name"`
	State     string    `json:"state"`
	Received  int64     `json:"received"`
	Total     int64     `json:"total"`
	Error     *Error    `json:"error,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}
type downloadEntry struct {
	DownloadInfo
	owner    string
	target   FileGrant
	ctx      context.Context
	cancel   context.CancelFunc
	task     Task
	observed string
}

// downloadURL 只接受不带凭据的 HTTP(S) 地址；不允许 file、data、协议相对地址或包含换行的输入。
func downloadURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || strings.ContainsAny(raw, "\r\n\\") {
		return Fail("E_INVALID_ARGUMENT", "下载地址必须是不带用户名密码的 HTTP(S) URL")
	}
	return nil
}

// StartDownload 启动独立于短期 RPC 生命周期的后台下载，目标必须由保存对话框授权；同一目标只允许一个运行任务。
func (d *Desktop) StartDownload(ctx context.Context, raw, token string) (DownloadInfo, error) {
	if err := downloadURL(raw); err != nil {
		return DownloadInfo{}, err
	}
	g, err := d.file(ctx, token, true)
	if err != nil {
		return DownloadInfo{}, err
	}
	d.mu.Lock()
	locked := true
	defer func() {
		if locked {
			d.mu.Unlock()
		}
	}()
	running := 0
	for _, e := range d.downloads {
		if e.State == "running" {
			running++
			if e.target.Path == g.Path {
				return DownloadInfo{}, Fail("E_BUSY", "此目标已有下载任务")
			}
		}
	}
	if running >= 4 || len(d.downloads) >= 100 {
		return DownloadInfo{}, Fail("E_BUSY", "最多同时下载 4 个文件，历史记录最多 100 条")
	}
	id, err := randomID()
	if err != nil {
		return DownloadInfo{}, err
	}
	label := []rune(g.Name)
	if len(label) > 100 {
		label = label[:100]
	}
	task, err := d.Controller.BeginTask(ctx, "下载 "+string(label))
	if err != nil {
		return DownloadInfo{}, err
	}
	background, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	e := &downloadEntry{DownloadInfo: DownloadInfo{ID: id, URL: raw, Name: g.Name, State: "running", Total: -1, CreatedAt: time.Now()}, owner: resourceOwner(ctx), target: g, ctx: background, cancel: cancel, task: task}
	d.downloads[id] = e
	d.mu.Unlock()
	locked = false
	if d.Controller.taskCenter != nil {
		e.observed = d.Controller.taskCenter.observeNative(ctx, "download", "下载 "+g.Name, nil, raw, token, func() { _ = d.CancelDownload(taskContext(ctx), id) })
	}
	// 下载在独立协程中运行，直到完成、用户取消、30 分钟超时或应用退出；退出保护在收尾时释放。
	go d.transfer(ctx, e)
	return DownloadInfo{ID: id, URL: raw, Name: g.Name, State: "running", Total: -1, CreatedAt: e.CreatedAt}, nil
}

// transfer 流式下载到同目录临时文件，只有完整成功且目标仍符合原授权时才原子替换，失败和取消都清理临时内容。
func (d *Desktop) transfer(ownerCtx context.Context, e *downloadEntry) {
	var result error
	defer func() {
		e.cancel()
		_ = d.Controller.EndTask(ownerCtx, e.task.ID)
		d.mu.Lock()
		if result == nil {
			e.State = "completed"
		} else if errors.Is(result, context.Canceled) {
			e.State = "cancelled"
			e.Error = &Error{Code: "E_CANCELLED", Message: "下载已取消"}
		} else {
			e.State = "failed"
			e.Error = &Error{Code: "E_DOWNLOAD_FAILED", Message: "下载失败，请检查网络、地址、磁盘权限或文件大小后重试"}
		}
		value := e.DownloadInfo
		d.mu.Unlock()
		if d.Controller.taskCenter != nil {
			var failure error
			if value.Error != nil {
				failure = value.Error
			}
			d.Controller.taskCenter.observeProgress(e.observed, -1, value.State, failure)
		}
	}()
	req, err := http.NewRequestWithContext(e.ctx, http.MethodGet, e.URL, nil)
	if err != nil {
		result = err
		return
	}
	// 每次重定向都重新校验协议和凭据，不向目标自动附加业务 Cookie 或登录令牌。
	client := &http.Client{Timeout: 30 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		return downloadURL(req.URL.String())
	}}
	resp, err := client.Do(req)
	if err != nil {
		result = err
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || resp.ContentLength > maxDownloadBytes {
		result = errors.New("status or size rejected")
		return
	}
	d.mu.Lock()
	e.Total = resp.ContentLength
	d.mu.Unlock()
	f, err := os.CreateTemp(filepath.Dir(e.target.Path), ".hybrid-download-*")
	if err != nil {
		result = err
		return
	}
	defer os.Remove(f.Name())
	defer f.Close()
	reader := io.LimitReader(resp.Body, maxDownloadBytes+1)
	buffer := make([]byte, 64*1024)
	for {
		if err = e.ctx.Err(); err != nil {
			result = err
			return
		}
		n, readErr := reader.Read(buffer)
		if n > 0 {
			written, writeErr := f.Write(buffer[:n])
			if writeErr != nil {
				result = writeErr
				return
			}
			d.mu.Lock()
			e.Received += int64(written)
			tooLarge := e.Received > maxDownloadBytes
			progress := -1
			if e.Total > 0 {
				progress = int(e.Received * 100 / e.Total)
				if progress > 100 {
					progress = 100
				}
			}
			d.mu.Unlock()
			if d.Controller.taskCenter != nil {
				d.Controller.taskCenter.observeProgress(e.observed, progress, "running", nil)
			}
			if tooLarge {
				result = errors.New("download too large")
				return
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			result = readErr
			return
		}
	}
	if err = f.Sync(); err != nil {
		result = err
		return
	}
	if err = f.Close(); err != nil {
		result = err
		return
	}
	if _, err = d.file(ownerCtx, e.target.Token, true); err != nil {
		result = err
		return
	}
	// 与 CancelDownload 共用锁，确保取消成功之后不会再把临时内容提交到目标。
	d.mu.Lock()
	defer d.mu.Unlock()
	if err = e.ctx.Err(); err != nil {
		result = err
		return
	}
	result = os.Rename(f.Name(), e.target.Path)
}

// ListDownloads 返回当前来源和窗口所属下载，按创建时间降序排列；不跨窗口泄露地址和文件名。
func (d *Desktop) ListDownloads(ctx context.Context) []DownloadInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := []DownloadInfo{}
	for _, e := range d.downloads {
		if e.owner == resourceOwner(ctx) {
			result = append(result, e.DownloadInfo)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result
}

// CancelDownload 请求中断本页面的任务；已结束的任务幂等返回成功，不删除已经完成的文件。
func (d *Desktop) CancelDownload(ctx context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.downloads[id]
	if !ok {
		return Fail("E_NOT_FOUND", "下载任务不存在")
	}
	if e.owner != resourceOwner(ctx) {
		return Fail("E_PERMISSION_DENIED", "不能操作其他页面的下载")
	}
	if e.State == "running" {
		e.cancel()
	}
	return nil
}

// RetryDownload 为失败或已取消的任务重新从头下载，不保留断点；目标授权仍须有效，完成文件不会被自动重试覆盖。
func (d *Desktop) RetryDownload(ctx context.Context, id string) (DownloadInfo, error) {
	d.mu.Lock()
	e, ok := d.downloads[id]
	if !ok || e.owner != resourceOwner(ctx) {
		d.mu.Unlock()
		return DownloadInfo{}, Fail("E_PERMISSION_DENIED", "下载任务不存在或不属于当前页面")
	}
	state, raw, token := e.State, e.URL, e.target.Token
	d.mu.Unlock()
	if state != "failed" && state != "cancelled" {
		return DownloadInfo{}, Fail("E_BUSY", "仅失败或取消的任务允许重试")
	}
	return d.StartDownload(ctx, raw, token)
}

// OpenDownload 仅打开本页面已完成下载的文件，或显示其所在目录；不会打开未完成的临时文件。
func (d *Desktop) OpenDownload(ctx context.Context, id string, reveal bool) error {
	d.mu.Lock()
	e, ok := d.downloads[id]
	if !ok || e.owner != resourceOwner(ctx) {
		d.mu.Unlock()
		return Fail("E_PERMISSION_DENIED", "下载任务不存在或不属于当前页面")
	}
	state, token := e.State, e.target.Token
	d.mu.Unlock()
	if state != "completed" {
		return Fail("E_BUSY", "下载尚未完成")
	}
	g, err := d.file(ctx, token, false)
	if err != nil {
		return err
	}
	if reveal {
		return revealPath(g.Path)
	}
	return d.app.Browser.OpenFile(g.Path)
}

// RemoveDownload 清理已结束任务的历史记录，不删除下载文件；运行任务必须先取消并等待收尾。
func (d *Desktop) RemoveDownload(ctx context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.downloads[id]
	if !ok {
		return nil
	}
	if e.owner != resourceOwner(ctx) {
		return Fail("E_PERMISSION_DENIED", "不能清理其他页面的下载")
	}
	if e.State == "running" {
		return Fail("E_BUSY", "请先取消运行中的下载")
	}
	delete(d.downloads, id)
	// 没有其他下载引用时同时释放目标凭证；记录清理不会删除磁盘文件。
	referenced := false
	for _, other := range d.downloads {
		if other.target.Token == e.target.Token {
			referenced = true
			break
		}
	}
	if !referenced {
		delete(d.grants, e.target.Token)
	}
	return nil
}
