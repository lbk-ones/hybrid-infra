package host

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"debug/pe"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"hybrid-infra/go/setting"
)

// UpdateManifest 是离线 Ed25519 签名的原始载荷，绑定平台、频道、大小、哈希和数据兼容范围。
type UpdateManifest struct {
	Schema        int    `json:"schema"`
	Version       string `json:"version"`
	Channel       string `json:"channel"`
	Platform      string `json:"platform"`
	Arch          string `json:"arch"`
	URL           string `json:"url"`
	SHA256        string `json:"sha256"`
	Size          int64  `json:"size"`
	MinDataSchema int    `json:"minDataSchema"`
	MaxDataSchema int    `json:"maxDataSchema"`
}

// SignedUpdate 在原始字节上签名，避免不同 JSON 序列化方式导致验签歧义。
type SignedUpdate struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

// UpdateState 是前端可见的进度，不暴露下载地址、公钥或本地可执行文件路径。
type UpdateState struct {
	CanRollback bool    `json:"canRollback"`
	Enabled     bool    `json:"enabled"`
	Stage       string  `json:"stage"`
	Version     string  `json:"version"`
	Progress    float64 `json:"progress"`
	Code        string  `json:"code"`
	signed      SignedUpdate
	manifest    UpdateManifest
}

var releaseVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// newerRelease 比较三个整数版本；当前维护协议只发布正式版本，不接受模糊或带预发布后缀的版本。
func newerRelease(next, current string) bool {
	if !releaseVersion.MatchString(next) || !releaseVersion.MatchString(current) {
		return false
	}
	a, b := strings.Split(next, "."), strings.Split(current, ".")
	for i := range a {
		x, e := strconv.ParseUint(a[i], 10, 32)
		y, f := strconv.ParseUint(b[i], 10, 32)
		if e != nil || f != nil {
			return false
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// safeUpdateURL 仅允许 HTTPS，回环 HTTP 仅用于本机集成测试；不允许 URL 内嵌密码和片段。
func safeUpdateURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Host != "" && u.User == nil && u.Fragment == "" && (u.Scheme == "https" || u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))
}

// verifyUpdate 先验证签名再检查目标平台与数据版本，不信任服务器返回的未签名字段。
func verifyUpdate(envelope SignedUpdate, config setting.UpdateConfig) (UpdateManifest, error) {
	var result UpdateManifest
	payload, e := base64.StdEncoding.DecodeString(envelope.Payload)
	key, k := base64.StdEncoding.DecodeString(config.PublicKey)
	signature, s := base64.StdEncoding.DecodeString(envelope.Signature)
	if e != nil || k != nil || s != nil || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, payload, signature) {
		return result, Fail("E_UPDATE_SIGNATURE", "更新清单签名无效")
	}
	if json.Unmarshal(payload, &result) != nil {
		return result, Fail("E_UPDATE_INVALID", "更新清单格式无效")
	}
	hash, h := hex.DecodeString(result.SHA256)
	if result.Schema != 1 || !releaseVersion.MatchString(result.Version) || result.Channel != config.Channel || result.Platform != runtime.GOOS || result.Arch != runtime.GOARCH || !safeUpdateURL(result.URL) || h != nil || len(hash) != 32 || result.Size < 1 || result.Size > 256<<20 || result.MinDataSchema > DataSchema || result.MaxDataSchema < DataSchema {
		return result, Fail("E_UPDATE_INCOMPATIBLE", "更新平台、频道、数据版本或文件约束不匹配")
	}
	return result, nil
}

// updateClient 限制重定向次数和来源，避免可信更新地址被重定向到其他不可信站点。
func updateClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 4 || !safeUpdateURL(req.URL.String()) || req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host {
			return Fail("E_UPDATE_REDIRECT", "更新重定向超出可信来源")
		}
		return nil
	}}
}

// UpdateStatus 返回当前进度及上一轮工作进程结果，更新关闭时仍可查看本地状态。
func (m *Maintenance) UpdateStatus() UpdateState {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.update
	if b, e := os.ReadFile(filepath.Join(m.dir, "updates", "rollback.json")); e == nil {
		var record rollbackRecord
		if json.Unmarshal(b, &record) == nil && record.BackupID != "" {
			if info, e := os.Stat(filepath.Join(m.dir, "updates", "previous.exe")); e == nil && info.Mode().IsRegular() {
				state.CanRollback = true
			}
		}
	}
	state.Enabled = m.controller != nil && m.controller.config.Updates.Enabled
	if state.Stage == "idle" {
		var saved UpdateState
		if b, e := os.ReadFile(filepath.Join(m.dir, "updates", "result.json")); e == nil && json.Unmarshal(b, &saved) == nil {
			state.Stage = saved.Stage
			state.Version = saved.Version
			state.Code = saved.Code
		}
	}
	return state
}

// CheckUpdates 从固定启动配置下载并验证签名清单，不允许前端传入地址或公钥。
func (m *Maintenance) CheckUpdates(ctx context.Context) (UpdateState, error) {
	if !m.op.TryLock() {
		return m.UpdateStatus(), Fail("E_BUSY", "维护操作正在执行")
	}
	defer m.op.Unlock()
	if m.controller == nil || !m.controller.config.Updates.Enabled {
		return m.UpdateStatus(), Fail("E_UPDATE_DISABLED", "尚未配置并启用签名更新源")
	}
	m.mu.Lock()
	busy := m.cancel != nil
	m.mu.Unlock()
	if busy {
		return m.UpdateStatus(), Fail("E_BUSY", "更新正在下载")
	}
	config := m.controller.config.Updates
	request, e := http.NewRequestWithContext(ctx, http.MethodGet, config.ManifestURL, nil)
	if e != nil {
		return m.UpdateStatus(), Fail("E_UPDATE_NETWORK", "更新地址无效")
	}
	response, e := updateClient().Do(request)
	if e != nil {
		return m.UpdateStatus(), Fail("E_UPDATE_NETWORK", "无法获取更新清单")
	}
	defer response.Body.Close()
	body, e := io.ReadAll(io.LimitReader(response.Body, (512<<10)+1))
	if e != nil || response.StatusCode != 200 || len(body) > 512<<10 {
		return m.UpdateStatus(), Fail("E_UPDATE_NETWORK", "更新清单响应异常")
	}
	var signed SignedUpdate
	if json.Unmarshal(body, &signed) != nil {
		return m.UpdateStatus(), Fail("E_UPDATE_INVALID", "更新清单格式无效")
	}
	manifest, e := verifyUpdate(signed, config)
	if e != nil {
		return m.UpdateStatus(), e
	}
	stage := "up_to_date"
	if newerRelease(manifest.Version, Version) {
		stage = "available"
	}
	m.mu.Lock()
	m.update = UpdateState{Stage: stage, Version: manifest.Version, signed: signed, manifest: manifest}
	m.mu.Unlock()
	m.record("UPDATE_CHECKED")
	return m.UpdateStatus(), nil
}

// DownloadUpdate 启动受退出保护的后台下载；最多一个更新任务，取消时删除未完成文件。
func (m *Maintenance) DownloadUpdate(ctx context.Context) error {
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	if m.cancel != nil || m.update.Stage != "available" {
		m.mu.Unlock()
		return Fail("E_BUSY", "请先检查并确认存在可下载的更新")
	}
	manifest := m.update.manifest
	m.mu.Unlock()
	id, e := m.controller.BeginTask(ctx, "下载应用更新")
	if e != nil {
		return e
	}
	downloadCtx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.cancel = cancel
	m.update.Stage = "downloading"
	m.update.Progress = 0
	m.update.Code = ""
	m.mu.Unlock()
	// 下载独立于发起 RPC 的生命周期；显式取消、应用退出或任务结束时统一清理资源。
	go func() {
		defer cancel()
		defer m.controller.EndTask(ctx, id.ID)
		err := m.download(downloadCtx, manifest)
		m.mu.Lock()
		m.cancel = nil
		if err != nil {
			m.update.Stage = "available"
			m.update.Code = "E_UPDATE_DOWNLOAD"
		} else {
			m.update.Stage = "ready"
			m.update.Progress = 1
		}
		m.mu.Unlock()
		if err != nil {
			m.record("E_UPDATE_DOWNLOAD")
		} else {
			m.record("UPDATE_DOWNLOADED")
		}
	}()
	return nil
}

// CancelUpdate 取消当前更新流；任务会在网络请求退出后释放退出保护。
func (m *Maintenance) CancelUpdate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
}

// download 按签名中的大小及 SHA256 流式写入，校验成功前不会成为安装候选。
func (m *Maintenance) download(ctx context.Context, manifest UpdateManifest) error {
	dir := filepath.Join(m.dir, "updates")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(dir, ".download-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	req, e := http.NewRequestWithContext(ctx, "GET", manifest.URL, nil)
	if e != nil {
		return e
	}
	response, e := updateClient().Do(req)
	if e != nil {
		return e
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return Fail("E_UPDATE_DOWNLOAD", "下载响应异常")
	}
	hash := sha256.New()
	buffer := make([]byte, 64<<10)
	var count int64
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			count += int64(n)
			if count > manifest.Size {
				return Fail("E_UPDATE_HASH", "更新超过签名大小")
			}
			if _, e = io.MultiWriter(f, hash).Write(buffer[:n]); e != nil {
				return e
			}
			m.mu.Lock()
			m.update.Progress = float64(count) / float64(manifest.Size)
			m.mu.Unlock()
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if count != manifest.Size || hex.EncodeToString(hash.Sum(nil)) != manifest.SHA256 {
		return Fail("E_UPDATE_HASH", "更新大小或 SHA256 不匹配")
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = validateExecutable(f.Name()); e != nil {
		return e
	}
	return os.Rename(f.Name(), filepath.Join(dir, "candidate.exe"))
}

// validateExecutable 检查 Windows PE 架构，防止误把压缩包或其他平台文件作为可执行更新。
func validateExecutable(path string) error {
	if runtime.GOOS != "windows" {
		return Fail("E_UNSUPPORTED", "自动替换当前仅支持 Windows 便携版")
	}
	file, e := pe.Open(path)
	if e != nil {
		return Fail("E_UPDATE_INVALID", "更新不是有效 Windows 可执行文件")
	}
	defer file.Close()
	machine := uint16(pe.IMAGE_FILE_MACHINE_AMD64)
	if runtime.GOARCH == "arm64" {
		machine = pe.IMAGE_FILE_MACHINE_ARM64
	}
	if file.FileHeader.Machine != machine || file.FileHeader.Characteristics&pe.IMAGE_FILE_DLL != 0 {
		return Fail("E_UPDATE_INVALID", "更新可执行文件架构不匹配")
	}
	return nil
}
