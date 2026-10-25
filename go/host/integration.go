package host

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
	"hybrid-infra/go/setting"
)

// encodeDIB 将已校验头部的 24/32 位 DIB 像素转为 PNG；忽略旧式 DIB 未定义的 alpha 字节。
func encodeDIB(data []byte, width, height, bits int) ([]byte, error) {
	rows := height
	if rows < 0 {
		rows = -rows
	}
	stride := ((width*bits + 31) / 32) * 4
	if len(data) < stride*rows {
		return nil, Fail("E_UNSUPPORTED", "剪贴板位图像素不完整")
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, rows))
	for y := 0; y < rows; y++ {
		srcY := y
		if height > 0 {
			srcY = rows - 1 - y
		}
		for x := 0; x < width; x++ {
			src := srcY*stride + x*(bits/8)
			dst := y*img.Stride + x*4
			img.Pix[dst] = data[src+2]
			img.Pix[dst+1] = data[src+1]
			img.Pix[dst+2] = data[src]
			img.Pix[dst+3] = 255
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, Fail("E_SYSTEM", "剪贴板图像转换失败")
	}
	if out.Len() > maxFileBytes {
		return nil, Fail("E_TOO_LARGE", "转换后的 PNG 超过 512 KiB")
	}
	return out.Bytes(), nil
}

// pngDIB 为只识别标准位图格式的 Windows 程序生成自底向上的 32 位 DIB，同时保留 PNG 供支持透明度的程序使用。
func pngDIB(data []byte) ([]byte, error) {
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > maxCapturePixels {
		return nil, Fail("E_TOO_LARGE", "PNG 无效或像素超过截图限制")
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, Fail("E_INVALID_ARGUMENT", "不是有效的 PNG 图像")
	}
	width, height := img.Bounds().Dx(), img.Bounds().Dy()
	if int64(width)*int64(height) > maxCapturePixels {
		return nil, Fail("E_TOO_LARGE", "图像像素超过截图限制")
	}
	result := make([]byte, 40+width*height*4)
	binary.LittleEndian.PutUint32(result, 40)
	binary.LittleEndian.PutUint32(result[4:], uint32(width))
	binary.LittleEndian.PutUint32(result[8:], uint32(height))
	binary.LittleEndian.PutUint16(result[12:], 1)
	binary.LittleEndian.PutUint16(result[14:], 32)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r, g, b, a := img.At(x+img.Bounds().Min.X, y+img.Bounds().Min.Y).RGBA()
			i := 40 + ((height-1-y)*width+x)*4
			result[i] = byte(b >> 8)
			result[i+1] = byte(g >> 8)
			result[i+2] = byte(r >> 8)
			result[i+3] = byte(a >> 8)
		}
	}
	return result, nil
}

// ClipboardText 按 read/write/clear 操作文本剪贴板；明确返回占用错误，不把失败伪装成空文本。
func (d *Desktop) ClipboardText(operation, text string) (string, error) {
	if len(text) > maxFileBytes {
		return "", Fail("E_TOO_LARGE", "剪贴板文本超过 512 KiB")
	}
	switch operation {
	case "read":
		value, ok := d.app.Clipboard.Text()
		if !ok {
			return "", Fail("E_CLIPBOARD", "剪贴板没有文本或正在被占用")
		}
		return value, nil
	case "write", "clear":
		if operation == "clear" {
			text = ""
		}
		if !d.app.Clipboard.SetText(text) {
			return "", Fail("E_CLIPBOARD", "无法写入剪贴板")
		}
		return "", nil
	default:
		return "", Fail("E_INVALID_ARGUMENT", "operation 必须是 read、write 或 clear")
	}
}

// ClipboardImage 读写最多 512 KiB 的 PNG；写入前检查图像尺寸，拒绝解压后超过 400 万像素的输入。
func (d *Desktop) ClipboardImage(ctx context.Context, data []byte, write bool) ([]byte, error) {
	if write {
		if len(data) == 0 || len(data) > maxFileBytes {
			return nil, Fail("E_TOO_LARGE", "PNG 必须介于 1 字节和 512 KiB")
		}
		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return nil, Fail("E_INVALID_ARGUMENT", "仅支持 PNG 图像")
		}
		if config.Width > 4096 || config.Height > 4096 || int64(config.Width)*int64(config.Height) > 4*1024*1024 {
			return nil, Fail("E_TOO_LARGE", "PNG 尺寸超过限制")
		}
	}
	w, ok := d.app.Window.GetByName(caller(ctx))
	if !ok {
		return nil, Fail("E_NOT_FOUND", "调用窗口已关闭")
	}
	return clipboardImage(data, write, uintptr(w.NativeWindow()))
}

// Credential 在按数据目录和页面来源隔离的系统凭据命名空间中读写秘密；不写入日志、普通 KV 或配置文件。
func (d *Desktop) Credential(ctx context.Context, key, value, operation string) (string, error) {
	if !validID.MatchString(key) || len(value) > 2560 {
		return "", Fail("E_INVALID_ARGUMENT", "凭据名称必须是 1–80 位字母数字或 ._-，值最多 2560 字节")
	}
	if operation != "get" && operation != "set" && operation != "delete" {
		return "", Fail("E_INVALID_ARGUMENT", "operation 必须是 get、set 或 delete")
	}
	p, _ := ctx.Value(principalKey{}).(Principal)
	target := fmt.Sprintf("hybrid-infra/%x/%s", sha256.Sum256([]byte(d.dir+"\n"+p.Origin)), key)
	result, err := nativeCredential(target, []byte(value), operation)
	return string(result), err
}

// SettingsSnapshot 区分正在运行的启动配置、磁盘待生效配置与立即生效的界面偏好。
type SettingsSnapshot struct {
	Active          *setting.PkgInfo `json:"active"`
	Saved           *setting.PkgInfo `json:"saved"`
	Preferences     Preferences      `json:"preferences"`
	RestartRequired bool             `json:"restartRequired"`
	ConfigPath      string           `json:"configPath"`
	ConfigSource    string           `json:"configSource"`
	DataDir         string           `json:"dataDir"`
	AppID           string           `json:"appId"`
	ProtocolScheme  string           `json:"protocolScheme"`
	PendingFields   []string         `json:"pendingFields"`
}

// settingsPath 固定使用本次启动选中的配置文件，默认启动首次保存到当前应用目录，不因其他文件出现而切换。
func (d *Desktop) settingsPath() (string, string) {
	path, source := d.Controller.config.ConfigLocation()
	if path == "" {
		path, source = filepath.Join(d.dir, "settings.json"), "user"
	}
	return path, source
}

// readSavedSettings 读取固定路径中的当前应用配置；默认启动尚未保存文件时返回默认配置，显式配置删除则报错。
func (d *Desktop) readSavedSettings() (*setting.PkgInfo, error) {
	path, _ := d.settingsPath()
	original, _ := d.Controller.config.ConfigLocation()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) && original == "" {
		return setting.ReadConfig("", d.Controller.config.Identity())
	}
	return setting.ReadConfig(path, d.Controller.config.Identity())
}

// sameSettingField 将空数组/空映射与 nil 视为同一配置，避免 Wails 模型序列化后把未修改字段误报为待重启。
func sameSettingField(a, b reflect.Value) bool {
	if !a.IsValid() || !b.IsValid() {
		return a.IsValid() == b.IsValid()
	}
	if a.Kind() == b.Kind() && (a.Kind() == reflect.Slice || a.Kind() == reflect.Map) && a.Len() == 0 && b.Len() == 0 {
		return true
	}
	if a.Kind() == reflect.Map && b.Kind() == reflect.Map && a.Len() == b.Len() {
		for _, key := range a.MapKeys() {
			if !sameSettingField(a.MapIndex(key), b.MapIndex(key)) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a.Interface(), b.Interface())
}

// GetSettings 读取已保存配置并与生效快照比较；图标使用最近成功重载的配置，窗口和安全来源仍使用启动快照。
func (d *Desktop) GetSettings() (SettingsSnapshot, error) {
	data, _ := json.Marshal(d.Controller.config)
	var active setting.PkgInfo
	_ = json.Unmarshal(data, &active)
	active.AppID = d.Controller.config.Identity()
	if d.Controller.icons != nil {
		active.Icons = d.Controller.icons.ActiveConfig()
	}
	saved, err := d.readSavedSettings()
	if err != nil {
		return SettingsSnapshot{}, Fail("E_SETTINGS", "磁盘配置无法读取或校验失败")
	}
	fields := []string{}
	activeValue, savedValue := reflect.ValueOf(active), reflect.ValueOf(*saved)
	for index := 0; index < activeValue.NumField(); index++ {
		name := activeValue.Type().Field(index).Tag.Get("json")
		if name != "" && name != "-" && !sameSettingField(activeValue.Field(index), savedValue.Field(index)) {
			fields = append(fields, name)
		}
	}
	path, source := d.settingsPath()
	return SettingsSnapshot{Active: &active, Saved: saved, Preferences: d.GetPreferences(), RestartRequired: len(fields) > 0, ConfigPath: path, ConfigSource: source, DataDir: d.dir, AppID: d.Controller.config.Identity(), ProtocolScheme: d.Controller.config.ProtocolScheme(), PendingFields: fields}, nil
}

// ReloadIcons 读取固定配置文件中的图标设置并立即替换；地址加载失败保留现有图标，不更改来源权限。
func (d *Desktop) ReloadIcons(ctx context.Context) (SettingsSnapshot, error) {
	if d.Controller.icons == nil {
		return SettingsSnapshot{}, Fail("E_UNSUPPORTED", "当前宿主尚未安装图标管理器")
	}
	config, err := d.readSavedSettings()
	if err != nil {
		return SettingsSnapshot{}, Fail("E_SETTINGS", "图标配置无法读取或校验失败")
	}
	if err = d.Controller.icons.Reload(ctx, config.Icons); err != nil {
		return SettingsSnapshot{}, err
	}
	return d.GetSettings()
}

// SaveSettings 校验完整启动配置后原子保存；运行中的权限和窗口不热更新，避免并发调用时策略一半新一半旧。
func (d *Desktop) SaveSettings(config setting.PkgInfo) (SettingsSnapshot, error) {
	if err := config.Validate(); err != nil {
		return SettingsSnapshot{}, Fail("E_INVALID_ARGUMENT", err.Error())
	}
	if config.Identity() != d.Controller.config.Identity() {
		return SettingsSnapshot{}, Fail("E_INVALID_ARGUMENT", "应用身份不能在运行中修改；请在部署配置设置 appId，并使用独立数据目录启动")
	}
	path, _ := d.settingsPath()
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return SettingsSnapshot{}, err
	}
	d.mu.Lock()
	err = atomicWrite(path, data)
	d.mu.Unlock()
	if err != nil {
		return SettingsSnapshot{}, diskError(err)
	}
	return d.GetSettings()
}

// ResetPreferences 恢复界面偏好默认值；不删除凭据、下载文件、窗口状态或启动安全设置。
func (d *Desktop) ResetPreferences() (Preferences, error) {
	return d.SavePreferences(defaultPreferences())
}

// Route 校验入口路由为当前业务页面的站内路径或 hash；拒绝远程地址、相对协议、目录跳转和带控制字符的输入。
func Route(raw string) error {
	if len(raw) == 0 || len(raw) > 2048 || strings.ContainsAny(raw, "\r\n\\\x00") || strings.HasPrefix(raw, "//") || (!strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "#")) {
		return Fail("E_INVALID_ARGUMENT", "入口必须是 / 开头的站内路径或 # 路由")
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil {
		return Fail("E_INVALID_ARGUMENT", "入口路由无效")
	}
	if strings.Contains(u.Path, "..") || strings.HasPrefix(u.Path, "//") || strings.ContainsAny(u.Path, "\\\x00\r\n") {
		return Fail("E_INVALID_ARGUMENT", "入口路径无效")
	}
	return nil
}

// ActivateRoute 先唤醒主窗口，再通过宿主导航状态机加载已校验的站内路由；未完成业务任务会阻止页面替换。
func (c *Controller) ActivateRoute(route string) error {
	if err := Route(route); err != nil {
		return err
	}
	base, err := url.Parse(c.config.PageURL())
	if err != nil {
		return Fail("E_INVALID_ARGUMENT", "主页面地址无效")
	}
	// local 的站内绝对路由仍留在外置网页挂载目录；hash 路由按原入口解析，避免跳回外壳内置资源。
	if c.config.EffectivePageMode() == "local" && strings.HasPrefix(route, "/") {
		route = setting.LocalPagePrefix + strings.TrimPrefix(route, "/")
	}
	target, err := base.Parse(route)
	if err != nil {
		return Fail("E_INVALID_ARGUMENT", "路由无法解析")
	}
	c.WakeMain()
	c.mu.Lock()
	e := c.windows["main-window"]
	if e == nil {
		c.mu.Unlock()
		return Fail("E_NOT_FOUND", "主窗口不存在")
	}
	for _, task := range c.tasks {
		if task.WindowID == "main-window" {
			c.mu.Unlock()
			return Fail("E_BUSY", "主窗口有未完成任务，无法切换页面")
		}
	}
	e.target = target.String()
	c.mu.Unlock()
	return c.Retry("main-window", false)
}

// HandleActivation 仅接收当前应用身份派生的协议入口，不接收其他业务应用的路由或可执行命令。
func (c *Controller) HandleActivation(args []string) {
	for _, arg := range args {
		u, err := url.Parse(arg)
		if err != nil || u.Scheme != c.config.ProtocolScheme() || u.Host != "open" || u.User != nil || u.Path != "" || u.Fragment != "" {
			continue
		}
		if err = c.ActivateRoute(u.Query().Get("route")); err != nil && c.app != nil {
			c.app.Logger.Warn("protocol activation rejected", "error", err)
		}
	}
}

var shortcutPattern = regexp.MustCompile(`(?i)^ctrl\+shift\+(?:alt\+)?[a-z0-9]$`)

// SetShortcut 注册或注销带 Ctrl+Shift 的业务快捷键；只导航已校验路由，不允许页面注册任意系统命令。
// 全局快捷键由操作系统检测冲突；本地快捷键保留 F5/F12 等外壳固定入口。
func (d *Desktop) SetShortcut(ctx context.Context, key, route string, global, enable bool) error {
	key = strings.ToLower(key)
	if !shortcutPattern.MatchString(key) {
		return Fail("E_INVALID_ARGUMENT", "仅支持 Ctrl+Shift+[Alt+]字母或数字")
	}
	if enable {
		if err := Route(route); err != nil {
			return err
		}
	}
	d.shortcutMu.Lock()
	defer d.shortcutMu.Unlock()
	id := fmt.Sprintf("%t/%s", global, key)
	owner := resourceOwner(ctx)
	if previous, ok := d.shortcuts[id]; ok && previous != owner {
		return Fail("E_CONFLICT", "此快捷键已由其他页面注册")
	}
	if !enable {
		if _, ok := d.shortcuts[id]; !ok {
			return nil
		}
		if global {
			if err := d.app.GlobalShortcut.Unregister(key); err != nil {
				return Fail("E_SYSTEM", "注销全局快捷键失败")
			}
		} else {
			d.app.KeyBinding.Remove(key)
		}
		delete(d.shortcuts, id)
		return nil
	}
	if _, ok := d.shortcuts[id]; ok {
		return Fail("E_CONFLICT", "快捷键已注册，请先注销")
	}
	// 回调只进入固定主窗口路由，权限由注册服务验证；操作系统快捷键不依赖页面脚本持续存活。
	action := func() {
		if err := d.Controller.ActivateRoute(route); err != nil {
			d.app.Logger.Warn("shortcut activation rejected", "error", err)
		}
	}
	if global {
		if err := d.app.GlobalShortcut.Register(key, action); err != nil {
			return Fail("E_CONFLICT", "全局快捷键无效或已被其他程序占用")
		}
	} else {
		d.app.KeyBinding.Add(key, func(application.Window) { action() })
	}
	d.shortcuts[id] = owner
	return nil
}

// Notify 向原生通知中心发送消息并为点击登记固定站内路由；通知开关关闭时返回 E_DISABLED。
func (d *Desktop) Notify(title, body, route string) error {
	if len(title) == 0 || len(title) > 120 || len(body) > 2000 {
		return Fail("E_INVALID_ARGUMENT", "标题需为 1–120 字节，正文最多 2000 字节")
	}
	if err := Route(route); err != nil {
		return err
	}
	if !d.GetPreferences().Notifications {
		return Fail("E_DISABLED", "系统通知已在偏好设置中关闭")
	}
	return d.sendNotification(title, body, route)
}

// sendNotification 延迟初始化 Wails 原生通知服务；仅在实际使用时注册系统通知身份，点击回调使用宿主保存的路由。
func (d *Desktop) sendNotification(title, body, route string) error {
	d.notifyMu.Lock()
	defer d.notifyMu.Unlock()
	if d.notifier == nil {
		n := notifications.New()
		if err := n.ServiceStartup(d.app.Context(), application.ServiceOptions{}); err != nil {
			return Fail("E_NOTIFICATION", "初始化系统通知失败")
		}
		d.notifier = n
		d.notificationRoutes = map[string]string{}
		// 不信任通知返回的 UserInfo 中的路由，只使用本进程发送时登记的通知编号。
		n.OnNotificationResponse(func(result notifications.NotificationResult) {
			d.notifyMu.Lock()
			route, ok := d.notificationRoutes[result.Response.ID]
			delete(d.notificationRoutes, result.Response.ID)
			d.notifyMu.Unlock()
			if result.Error == nil && ok {
				if err := d.Controller.ActivateRoute(route); err != nil {
					d.app.Logger.Warn("notification activation rejected", "error", err)
				}
			}
		})
	}
	granted, err := d.notifier.RequestNotificationAuthorization()
	if err != nil || !granted {
		return Fail("E_PERMISSION_DENIED", "系统未允许发送通知")
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	d.notificationRoutes[id] = route
	// 有界保留最近 128 次点击路由，避免应用长期运行时积累无法回收的通知记录。
	d.notificationOrder = append(d.notificationOrder, id)
	if len(d.notificationOrder) > 128 {
		delete(d.notificationRoutes, d.notificationOrder[0])
		d.notificationOrder = d.notificationOrder[1:]
	}
	if err = d.notifier.SendNotification(notifications.NotificationOptions{ID: id, Title: title, Body: body}); err != nil {
		delete(d.notificationRoutes, id)
		return Fail("E_NOTIFICATION", "发送系统通知失败，请检查系统通知设置")
	}
	return nil
}

// ConfigureProtocol 显式注册或移除当前用户级的固定应用协议；不会在启动时自动修改系统注册表。
func (d *Desktop) ConfigureProtocol(enable bool) error {
	return registerProtocol(d.Controller.config.Identity(), d.Controller.config.ProtocolScheme(), enable)
}
