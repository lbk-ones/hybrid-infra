package host

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

const maxFileBytes = 512 * 1024 // 小文件 RPC 限制；大文件使用下载管理器，避免突破 runtime 的 1 MiB 请求上限。

// FileGrant 是用户通过系统对话框授予当前窗口的文件访问凭证，路径仅用于显示，后续操作只接收 token。
type FileGrant struct {
	Token     string `json:"token"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	Writable  bool   `json:"writable"`
	owner     string
}

// DirectoryEntry 是已授权目录的单项元数据；不包含可绕过凭证校验的任意文件访问入口。
type DirectoryEntry struct {
	Name      string `json:"name"`
	Directory bool   `json:"directory"`
	Size      int64  `json:"size"`
}

// ListDirectory 列出已选目录最多 500 个非符号链接条目；读取子项须另行调用 OpenChild 取得只读凭证。
func (d *Desktop) ListDirectory(ctx context.Context, token string) ([]DirectoryEntry, error) {
	g, err := d.file(ctx, token, false)
	if err != nil {
		return nil, err
	}
	if !g.Directory {
		return nil, Fail("E_INVALID_ARGUMENT", "此凭证不是目录")
	}
	f, err := os.Open(g.Path)
	if err != nil {
		return nil, diskError(err)
	}
	defer f.Close()
	items, err := f.ReadDir(501)
	if err != nil && err != io.EOF {
		return nil, diskError(err)
	}
	if len(items) > 500 {
		return nil, Fail("E_TOO_LARGE", "目录超过 500 个条目，请选择更小的子目录")
	}
	result := []DirectoryEntry{}
	for _, item := range items {
		if item.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, e := item.Info()
		if e != nil {
			continue
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			continue
		}
		result = append(result, DirectoryEntry{Name: item.Name(), Directory: item.IsDir(), Size: info.Size()})
	}
	return result, nil
}

// OpenChild 只能从已授权目录打开单个直接子项；拒绝目录跳转、分隔符和符号链接，不隐式给予写权限。
func (d *Desktop) OpenChild(ctx context.Context, token, name string) (FileGrant, error) {
	g, err := d.file(ctx, token, false)
	if err != nil {
		return FileGrant{}, err
	}
	if !g.Directory || name == "." || name == ".." || name == "" || filepath.Base(name) != name || strings.ContainsAny(name, "/\\\x00:") {
		return FileGrant{}, Fail("E_INVALID_ARGUMENT", "必须使用目录中的直接子项名称")
	}
	path := filepath.Join(g.Path, name)
	info, err := os.Lstat(path)
	if err != nil {
		return FileGrant{}, diskError(err)
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return FileGrant{}, Fail("E_PERMISSION_DENIED", "只允许打开普通文件或目录")
	}
	return d.grant(ctx, path, info.IsDir(), false)
}

// Preferences 保存业务界面偏好；不允许写入密码、令牌或任意启动参数。
type Preferences struct {
	Theme         string `json:"theme"`
	Locale        string `json:"locale"`
	Notifications bool   `json:"notifications"`
}

// Desktop 管理第二阶段的文件凭证、下载和系统集成；服务层负责能力检查，本层再次检查资源归属。
type Desktop struct {
	Controller         *Controller
	app                *application.App
	dir                string
	mu                 sync.Mutex
	grants             map[string]FileGrant
	downloads          map[string]*downloadEntry
	preferences        Preferences
	shortcuts          map[string]string
	shortcutMu         sync.Mutex // 独立于文件和下载锁，避免原生快捷键调用等待主线程时阻塞窗口关闭清理。
	notifyMu           sync.Mutex
	notifier           *notifications.NotificationService
	notificationRoutes map[string]string
	notificationOrder  []string
}

// NewDesktop 创建桌面能力管理器，读取界面偏好，并在应用退出时取消下载；不默认启用任何来源权限。
func NewDesktop(app *application.App, c *Controller, dir string) *Desktop {
	d := &Desktop{Controller: c, app: app, dir: dir, grants: map[string]FileGrant{}, downloads: map[string]*downloadEntry{}, shortcuts: map[string]string{}, preferences: defaultPreferences()}
	if data, err := os.ReadFile(filepath.Join(dir, "preferences.json")); err == nil {
		var saved Preferences
		if json.Unmarshal(data, &saved) == nil && validatePreferences(saved) == nil {
			d.preferences = saved
		}
	}
	if app != nil {
		app.OnShutdown(d.shutdown)
		c.OnResourceWindowClosed(d.windowClosed)
	}
	return d
}

// randomID 生成无法通过顺序猜测的临时资源凭证；随机源失败时直接返回错误，不退化为可预测编号。
func randomID() (string, error) {
	var bytes [24]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

// resourceOwner 将经过授权的来源和真实窗口身份组成资源所有者；不同来源或窗口不能复用文件、下载或凭据命名空间。
func resourceOwner(ctx context.Context) string {
	p, _ := ctx.Value(principalKey{}).(Principal)
	return p.Origin + "\n" + p.WindowName
}

// diskError 将磁盘错误转成稳定的前端错误码，避免把操作系统内部信息泄露给页面。
func diskError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return Fail("E_NOT_FOUND", "文件不存在")
	}
	if errors.Is(err, os.ErrPermission) {
		return Fail("E_PERMISSION_DENIED", "系统拒绝访问此文件")
	}
	return Fail("E_IO", "磁盘操作失败，请检查文件占用、权限和剩余空间")
}

// SelectFile 显示绑定调用窗口的打开、目录或保存对话框；用户取消返回 E_CANCELLED，保存选择同时授权覆盖该文件。
func (d *Desktop) SelectFile(ctx context.Context, mode, name string) (FileGrant, error) {
	var path string
	var err error
	w, _ := d.app.Window.GetByName(caller(ctx))
	switch mode {
	case "open", "directory":
		dialog := d.app.Dialog.OpenFile().CanChooseFiles(mode == "open").CanChooseDirectories(mode == "directory").SetTitle("选择文件或目录")
		if w != nil {
			dialog.AttachToWindow(w)
		}
		path, err = dialog.PromptForSingleSelection()
	case "save":
		if name == "" {
			name = "export.txt"
		}
		if filepath.Base(name) != name || len(name) > 200 {
			return FileGrant{}, Fail("E_INVALID_ARGUMENT", "文件名不能包含目录")
		}
		dialog := d.app.Dialog.SaveFile().SetFilename(name).SetMessage("保存文件")
		if w != nil {
			dialog.AttachToWindow(w)
		}
		path, err = dialog.PromptForSingleSelection()
	default:
		return FileGrant{}, Fail("E_INVALID_ARGUMENT", "mode 必须是 open、directory 或 save")
	}
	if err != nil {
		// 固定 Wails beta.33 的 Windows 文件对话框使用内部 sentinel，无法直接导入；按其精确文本识别用户取消。
		if errors.Is(err, context.Canceled) || err.Error() == "cancelled by user" {
			return FileGrant{}, Fail("E_CANCELLED", "已取消文件选择")
		}
		return FileGrant{}, diskError(err)
	}
	if path == "" {
		return FileGrant{}, Fail("E_CANCELLED", "已取消文件选择")
	}
	if err = ctx.Err(); err != nil {
		return FileGrant{}, Fail("E_CANCELLED", "调用已取消，请重新选择")
	}
	return d.grant(ctx, path, mode == "directory", mode == "save")
}

// grant 将用户选中的真实路径登记为临时凭证；规范化父目录并拒绝最终符号链接，最多保留 512 份凭证。
func (d *Desktop) grant(ctx context.Context, path string, directory, writable bool) (FileGrant, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return FileGrant{}, diskError(err)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return FileGrant{}, diskError(err)
	}
	path = filepath.Join(parent, filepath.Base(path))
	if info, e := os.Lstat(path); e == nil && info.Mode()&os.ModeSymlink != 0 {
		return FileGrant{}, Fail("E_PERMISSION_DENIED", "请选择原始文件，不能使用符号链接")
	}
	id, err := randomID()
	if err != nil {
		return FileGrant{}, err
	}
	g := FileGrant{Token: id, Name: filepath.Base(path), Path: path, Directory: directory, Writable: writable, owner: resourceOwner(ctx)}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.grants) >= 512 {
		return FileGrant{}, Fail("E_BUSY", "文件凭证已达上限，请先释放不再使用的凭证")
	}
	d.grants[id] = g
	return g, nil
}

// file 校验文件凭证归属及写权限，并再次检查父路径和符号链接，防止选择之后被普通链接替换。
func (d *Desktop) file(ctx context.Context, token string, write bool) (FileGrant, error) {
	d.mu.Lock()
	g, ok := d.grants[token]
	d.mu.Unlock()
	if !ok || g.owner != resourceOwner(ctx) {
		return FileGrant{}, Fail("E_PERMISSION_DENIED", "文件凭证无效或不属于当前页面")
	}
	if write && (!g.Writable || g.Directory) {
		return FileGrant{}, Fail("E_PERMISSION_DENIED", "此凭证没有文件写入权限")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(g.Path))
	if err != nil {
		return FileGrant{}, diskError(err)
	}
	if filepath.Clean(parent) != filepath.Dir(g.Path) {
		return FileGrant{}, Fail("E_PERMISSION_DENIED", "文件目录已改变，请重新选择")
	}
	if info, e := os.Lstat(g.Path); e == nil && info.Mode()&os.ModeSymlink != 0 {
		return FileGrant{}, Fail("E_PERMISSION_DENIED", "文件已变为符号链接，请重新选择")
	}
	if info, e := os.Lstat(g.Path); e == nil && (g.Directory != info.IsDir() || (!g.Directory && !info.Mode().IsRegular())) {
		return FileGrant{}, Fail("E_INVALID_ARGUMENT", "目标类型已经变化，只允许普通文件和目录")
	}
	return g, nil
}

// ReadFile 读取选中的普通文件，最多 512 KiB；二进制数据通过 Wails 的 []byte 自动转换为 Base64。
func (d *Desktop) ReadFile(ctx context.Context, token string) ([]byte, error) {
	g, err := d.file(ctx, token, false)
	if err != nil {
		return nil, err
	}
	if g.Directory {
		return nil, Fail("E_INVALID_ARGUMENT", "目录不能作为普通文件读取")
	}
	f, err := os.Open(g.Path)
	if err != nil {
		return nil, diskError(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, diskError(err)
	}
	if !info.Mode().IsRegular() {
		return nil, Fail("E_INVALID_ARGUMENT", "只允许读取普通文件")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, diskError(err)
	}
	if len(data) > maxFileBytes {
		return nil, Fail("E_TOO_LARGE", "文件超过 512 KiB，请使用流式业务接口")
	}
	return data, nil
}

// atomicWrite 先在同目录写入临时文件再替换目标；写失败不会留下半个业务文件，临时文件按 0600 创建。
func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".hybrid-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// WriteFile 仅向保存对话框授予的目标写入最多 512 KiB 数据，成功后保留凭证以便业务继续导出。
func (d *Desktop) WriteFile(ctx context.Context, token string, data []byte) error {
	g, err := d.file(ctx, token, true)
	if err != nil {
		return err
	}
	if len(data) > maxFileBytes {
		return Fail("E_TOO_LARGE", "写入内容超过 512 KiB")
	}
	return diskError(atomicWrite(g.Path, data))
}

// ReleaseFile 主动销毁当前窗口的临时文件凭证；仍有下载占用的目标必须先取消并等待收尾，避免提交时失去授权。
func (d *Desktop) ReleaseFile(ctx context.Context, token string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if g, ok := d.grants[token]; ok && g.owner != resourceOwner(ctx) {
		return Fail("E_PERMISSION_DENIED", "不能释放其他页面的凭证")
	}
	for _, e := range d.downloads {
		if e.target.Token == token && e.State == "running" {
			return Fail("E_BUSY", "下载正在使用此凭证，请先取消并等待完成")
		}
	}
	delete(d.grants, token)
	return nil
}

// RevealFile 在系统文件管理器中展示已授权文件或目录，不接受页面传入任意路径。
func (d *Desktop) RevealFile(ctx context.Context, token string) error {
	g, err := d.file(ctx, token, false)
	if err != nil {
		return err
	}
	return revealPath(g.Path)
}

// defaultPreferences 返回可随时恢复的业务偏好默认值。
func defaultPreferences() Preferences {
	return Preferences{Theme: "system", Locale: "zh-CN", Notifications: true}
}

// validatePreferences 只接受已支持的主题和语言，避免自由键值被误用为启动配置或秘密存储。
func validatePreferences(p Preferences) error {
	if p.Theme != "system" && p.Theme != "light" && p.Theme != "dark" {
		return Fail("E_INVALID_ARGUMENT", "theme 必须是 system、light 或 dark")
	}
	if p.Locale != "zh-CN" && p.Locale != "en-US" {
		return Fail("E_INVALID_ARGUMENT", "locale 必须是 zh-CN 或 en-US")
	}
	return nil
}

// GetPreferences 读取当前生效的业务偏好副本，修改副本不会改变宿主设置。
func (d *Desktop) GetPreferences() Preferences {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.preferences
}

// SavePreferences 校验并持久化业务偏好；成功后立即影响通知开关，其余偏好由业务页面读取应用，无须重启。
func (d *Desktop) SavePreferences(p Preferences) (Preferences, error) {
	if err := validatePreferences(p); err != nil {
		return Preferences{}, err
	}
	data, _ := json.MarshalIndent(p, "", "  ")
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := atomicWrite(filepath.Join(d.dir, "preferences.json"), data); err != nil {
		return Preferences{}, diskError(err)
	}
	d.preferences = p
	return p, nil
}

// shutdown 取消全部后台下载；应用级全局快捷键由 Wails 在退出时统一释放。
func (d *Desktop) shutdown() {
	d.mu.Lock()
	for _, e := range d.downloads {
		if e.cancel != nil {
			e.cancel()
		}
	}
	d.mu.Unlock()
	d.notifyMu.Lock()
	n := d.notifier
	d.notifyMu.Unlock()
	if n != nil {
		_ = n.ServiceShutdown()
	}
}

// windowClosed 在原生窗口销毁后撤销文件凭证并取消该窗口的下载，防止复用窗口名称时继承上一次会话的授权。
// 全局快捷键和通知属于本次应用运行，不依赖注册页面持续打开；它们只能进入已经校验过的固定路由。
func (d *Desktop) windowClosed(name string) {
	suffix := "\n" + name
	d.mu.Lock()
	defer d.mu.Unlock()
	for token, g := range d.grants {
		if strings.HasSuffix(g.owner, suffix) {
			delete(d.grants, token)
		}
	}
	for id, e := range d.downloads {
		if strings.HasSuffix(e.owner, suffix) {
			e.cancel()
			delete(d.downloads, id)
		}
	}
}
