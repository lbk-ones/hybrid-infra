package host

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"sync"
	"time"
)

const DataSchema = 2
const backupLimit = 48 << 20

var backupFiles = []string{"store.json", "preferences.json", "window-state.json", "data-version.json", databaseSnapshotFile}
var diagnosticCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// DiagnosticEvent 只记录固定类别和错误码，不收集页面地址、命令输出、业务数据或秘密值。
type DiagnosticEvent struct {
	Time string `json:"time"`
	Code string `json:"code"`
}

// BackupInfo 是备份清单；ID 只在固定备份目录解析，不接受前端文件路径。
type BackupInfo struct {
	ID      string            `json:"id"`
	Created string            `json:"created"`
	Version string            `json:"version"`
	Schema  int               `json:"schema"`
	Size    int64             `json:"size"`
	Hashes  map[string]string `json:"hashes"`
}

// Maintenance 串行管理迁移、备份和签名更新；诊断锁独立，避免下载期间阻塞状态查询。
type Maintenance struct {
	controller *Controller
	dir        string
	op         sync.Mutex
	mu         sync.Mutex
	events     []DiagnosticEvent
	update     UpdateState
	cancel     func()
	desktop    *Desktop
	database   *BusinessDatabase
}

// NewMaintenance 创建维护管理器；不发起网络请求，也不修改启动设置。
func NewMaintenance(c *Controller, dir string) *Maintenance {
	return &Maintenance{controller: c, dir: dir, events: []DiagnosticEvent{}, update: UpdateState{Stage: "idle"}}
}

// PrepareData 在单实例检查之后取得数据目录独占锁并恢复、迁移；返回的释放函数须保持到应用退出。
func (m *Maintenance) PrepareData() (func(), error) {
	release, err := acquireDataLock(m.dir)
	if err != nil {
		return nil, err
	}
	if err = InitializeData(m.dir); err != nil {
		release()
		return nil, err
	}
	if m.controller != nil {
		m.controller.store = newStateStore(m.dir)
	}
	// 只重载本程序生成的结构化记录；即使磁盘文件被替换，也不把自由文本带入导出包。
	if b, e := os.ReadFile(filepath.Join(m.dir, "diagnostics.json")); e == nil && len(b) <= 64<<10 {
		var previous []DiagnosticEvent
		if json.Unmarshal(b, &previous) == nil {
			for _, event := range previous {
				if _, e = time.Parse(time.RFC3339, event.Time); e == nil && diagnosticCode.MatchString(event.Code) {
					m.events = append(m.events, event)
				}
			}
			if len(m.events) > 128 {
				m.events = m.events[len(m.events)-128:]
			}
		}
	}
	return release, nil
}

// maintenanceID 使用随机 ID 隔离临时文件，避免时间戳碰撞和前端路径穿越。
func maintenanceID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// jsonWrite 在同目录原子替换 JSON，避免中途退出留下半个文件。
func jsonWrite(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, b)
}

// dataVersion 拒绝未来数据版本，防止旧程序误写新格式。
func dataVersion(dir string) (int, error) {
	b, e := os.ReadFile(filepath.Join(dir, "data-version.json"))
	if errors.Is(e, os.ErrNotExist) {
		return 0, nil
	}
	if e != nil {
		return 0, e
	}
	var v struct {
		Schema int `json:"schema"`
	}
	if json.Unmarshal(b, &v) != nil || v.Schema < 0 || v.Schema > DataSchema {
		return 0, Fail("E_DATA_VERSION", "数据版本高于当前程序或版本文件损坏")
	}
	return v.Schema, nil
}

// InitializeData 在服务读取 KV 前执行待恢复操作和版本迁移；升级旧格式前保留完整白名单备份。
func InitializeData(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	m := NewMaintenance(nil, dir)
	pending := filepath.Join(dir, "pending-restore.json")
	b, err := os.ReadFile(pending)
	if err == nil {
		var request struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(b, &request) != nil {
			return Fail("E_BACKUP_INVALID", "恢复请求损坏")
		}
		if err = m.restore(request.ID); err != nil {
			return err
		}
		if err = os.Remove(pending); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	v, err := dataVersion(dir)
	if err != nil {
		return err
	}
	if v == DataSchema {
		return nil
	}
	if _, err = m.createBackup(); err != nil {
		return err
	}
	return jsonWrite(filepath.Join(dir, "data-version.json"), map[string]int{"schema": DataSchema})
}

// RecordError 只收集宿主公开错误码；不把底层异常文本写入诊断包。
func (m *Maintenance) RecordError(err error) {
	if err == nil {
		return
	}
	code := "E_INTERNAL"
	var e *Error
	if errors.As(err, &e) {
		code = e.Code
	}
	m.record(code)
}

// record 保存最多 128 条结构化诊断事件，并原子更新有界本地文件。
func (m *Maintenance) record(code string) {
	if !diagnosticCode.MatchString(code) {
		code = "E_INTERNAL"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, DiagnosticEvent{time.Now().UTC().Format(time.RFC3339), code})
	if len(m.events) > 128 {
		m.events = m.events[len(m.events)-128:]
	}
	_ = jsonWrite(filepath.Join(m.dir, "diagnostics.json"), m.events)
}

// Diagnostics 返回不含用户内容的运行摘要；内存单位为字节，窗口和任务仅返回数量。
func (m *Maintenance) Diagnostics() map[string]any {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	m.mu.Lock()
	events := append([]DiagnosticEvent{}, m.events...)
	m.mu.Unlock()
	windows, tasks := 0, 0
	if m.controller != nil {
		m.controller.mu.RLock()
		windows = len(m.controller.windows)
		tasks = len(m.controller.tasks)
		m.controller.mu.RUnlock()
	}
	return map[string]any{"version": Version, "protocol": ProtocolVersion, "dataSchema": DataSchema, "platform": runtime.GOOS, "arch": runtime.GOARCH, "goVersion": runtime.Version(), "goroutines": runtime.NumGoroutine(), "memoryBytes": memory.Alloc, "windowCount": windows, "taskCount": tasks, "events": events}
}

// ExportDiagnostics 使用已有的文件凭证保存诊断，不开放新的任意路径写入入口。
func (m *Maintenance) ExportDiagnostics(ctx context.Context, token string, data []byte) error {
	if m.desktop == nil {
		return Fail("E_INTERNAL", "文件服务尚未初始化")
	}
	grant, err := m.desktop.file(ctx, token, true)
	if err != nil {
		return err
	}
	if grant.Directory {
		return Fail("E_INVALID_ARGUMENT", "请选择保存文件")
	}
	return atomicWrite(grant.Path, data)
}

// AttachDesktop 在服务注册期间接入文件凭证管理器。
func (m *Maintenance) AttachDesktop(desktop *Desktop) { m.desktop = desktop }

// AttachDatabase 接入在线逻辑快照；备份不直接复制可能正在写入的 SQLite 文件。
func (m *Maintenance) AttachDatabase(database *BusinessDatabase) { m.database = database }

// backupPath 将不透明 ID 限制为 32 位小写十六进制，拒绝任意路径和扩展名。
func (m *Maintenance) backupPath(id string) (string, error) {
	b, err := hex.DecodeString(id)
	if err != nil || len(b) != 16 || hex.EncodeToString(b) != id {
		return "", Fail("E_INVALID_ARGUMENT", "备份 ID 无效")
	}
	return filepath.Join(m.dir, "backups", id+".zip"), nil
}

// createBackup 在调用者持有维护锁且停止业务写入时保存白名单；设置、凭据与日志不在备份范围。
func (m *Maintenance) createBackup() (BackupInfo, error) {
	info := BackupInfo{ID: maintenanceID(), Created: time.Now().UTC().Format(time.RFC3339), Version: Version, Hashes: map[string]string{}}
	v, err := dataVersion(m.dir)
	if err != nil {
		return info, err
	}
	info.Schema = v
	var buffer bytes.Buffer
	w := zip.NewWriter(&buffer)
	total := 0
	for _, name := range backupFiles {
		path := filepath.Join(m.dir, name)
		if name == databaseSnapshotFile {
			var data []byte
			var e error
			if m.database != nil {
				data, e = m.database.exportSnapshot()
			} else {
				data, e = snapshotDatabaseFile(m.dir)
			}
			if e != nil {
				return info, e
			}
			if data == nil {
				continue
			}
			if !databaseSnapshotValid(data) {
				return info, Fail("E_BACKUP_INVALID", "数据库快照无效")
			}
			total += len(data)
			if total > backupLimit {
				return info, Fail("E_TOO_LARGE", "备份数据超限")
			}
			hash := sha256.Sum256(data)
			info.Hashes[name] = hex.EncodeToString(hash[:])
			entry, e := w.Create(name)
			if e != nil {
				return info, e
			}
			if _, e = entry.Write(data); e != nil {
				return info, e
			}
			continue
		}
		stat, e := os.Lstat(path)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return info, e
		}
		if !stat.Mode().IsRegular() || stat.Size() > 16<<20 {
			return info, Fail("E_BACKUP_INVALID", "备份文件必须是大小不超过 16 MiB 的普通 JSON 文件")
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return info, e
		}
		total += len(b)
		if len(b) > 16<<20 || total > backupLimit || !json.Valid(b) {
			return info, Fail("E_BACKUP_INVALID", "备份数据必须是有效 JSON 且大小不能超限")
		}
		h := sha256.Sum256(b)
		info.Hashes[name] = hex.EncodeToString(h[:])
		entry, e := w.Create(name)
		if e != nil {
			return info, e
		}
		if _, e = entry.Write(b); e != nil {
			return info, e
		}
	}
	entry, err := w.Create("manifest.json")
	if err != nil {
		return info, err
	}
	if err = json.NewEncoder(entry).Encode(info); err != nil {
		return info, err
	}
	if err = w.Close(); err != nil {
		return info, err
	}
	info.Size = int64(buffer.Len())
	path, _ := m.backupPath(info.ID)
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return info, err
	}
	return info, atomicWrite(path, buffer.Bytes())
}

// readBackup 先校验所有条目、路径、解压上限和 SHA256，再向恢复流程返回数据，拒绝部分恢复。
func (m *Maintenance) readBackup(id string) (BackupInfo, map[string][]byte, error) {
	var info BackupInfo
	path, err := m.backupPath(id)
	if err != nil {
		return info, nil, err
	}
	stat, err := os.Stat(path)
	if err != nil {
		return info, nil, err
	}
	if stat.Size() > backupLimit+(1<<20) {
		return info, nil, Fail("E_BACKUP_INVALID", "备份文件过大")
	}
	r, err := zip.OpenReader(path)
	if err != nil {
		return info, nil, err
	}
	defer r.Close()
	data := map[string][]byte{}
	total := 0
	for _, f := range r.File {
		allowed := f.Name == "manifest.json"
		for _, name := range backupFiles {
			allowed = allowed || name == f.Name
		}
		if !allowed || f.Mode()&os.ModeSymlink != 0 || data[f.Name] != nil {
			return info, nil, Fail("E_BACKUP_INVALID", "备份包含非法或重复条目")
		}
		reader, e := f.Open()
		if e != nil {
			return info, nil, e
		}
		b, e := io.ReadAll(io.LimitReader(reader, (16<<20)+1))
		_ = reader.Close()
		total += len(b)
		if e != nil || len(b) > 16<<20 || total > backupLimit || !json.Valid(b) {
			return info, nil, Fail("E_BACKUP_INVALID", "备份内容损坏或超限")
		}
		data[f.Name] = b
	}
	if json.Unmarshal(data["manifest.json"], &info) != nil || info.ID != id || info.Schema < 0 || info.Schema > DataSchema {
		return info, nil, Fail("E_BACKUP_INVALID", "备份版本或清单无效")
	}
	delete(data, "manifest.json")
	if len(data) != len(info.Hashes) {
		return info, nil, Fail("E_BACKUP_INVALID", "备份条目不完整")
	}
	for name, b := range data {
		if name == databaseSnapshotFile && !databaseSnapshotValid(b) {
			return info, nil, Fail("E_BACKUP_INVALID", "数据库快照损坏")
		}
		h := sha256.Sum256(b)
		if info.Hashes[name] != hex.EncodeToString(h[:]) {
			return info, nil, Fail("E_BACKUP_INVALID", "备份校验失败")
		}
	}
	if b, ok := data["data-version.json"]; ok {
		var v struct {
			Schema int `json:"schema"`
		}
		if json.Unmarshal(b, &v) != nil || v.Schema != info.Schema {
			return info, nil, Fail("E_BACKUP_INVALID", "备份数据版本不一致")
		}
	} else if info.Schema != 0 {
		return info, nil, Fail("E_BACKUP_INVALID", "缺少数据版本")
	}
	info.Size = stat.Size()
	return info, data, nil
}

// ListBackups 只列出验证通过的本地备份，损坏文件不会成为恢复候选。
func (m *Maintenance) ListBackups() []BackupInfo {
	m.op.Lock()
	defer m.op.Unlock()
	return m.listBackups()
}

// listBackups 供已持有维护锁的调用者读取清单，按创建时间从新到旧排列。
func (m *Maintenance) listBackups() []BackupInfo {
	result := []BackupInfo{}
	items, _ := os.ReadDir(filepath.Join(m.dir, "backups"))
	for _, item := range items {
		if item.IsDir() || filepath.Ext(item.Name()) != ".zip" {
			continue
		}
		id := item.Name()[:len(item.Name())-4]
		info, _, err := m.readBackup(id)
		if err == nil {
			result = append(result, info)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Created > result[j].Created })
	return result
}

// CreateBackup 暂停 Store/设置调用后创建一致性备份；窗口状态由宿主先落盘。
func (m *Maintenance) CreateBackup() (BackupInfo, error) {
	m.op.Lock()
	defer m.op.Unlock()
	if m.controller != nil {
		m.controller.Policy.dataGate.Lock()
		defer m.controller.Policy.dataGate.Unlock()
		if err := m.controller.store.save(); err != nil {
			return BackupInfo{}, err
		}
	}
	info, err := m.createBackup()
	if err == nil {
		m.record("BACKUP_CREATED")
	}
	return info, err
}

// StageRestore 完整验证备份后排队，下次启动在 KV 初始化之前恢复，避免运行中的内存缓存覆盖恢复结果。
func (m *Maintenance) StageRestore(id string) error {
	m.op.Lock()
	defer m.op.Unlock()
	if _, _, err := m.readBackup(id); err != nil {
		return err
	}
	return jsonWrite(filepath.Join(m.dir, "pending-restore.json"), map[string]string{"id": id})
}

// restore 在进程未加载业务数据时恢复白名单；先备份当前状态，写入失败则还原已触及的文件。
func (m *Maintenance) restore(id string) error {
	_, data, err := m.readBackup(id)
	if err != nil {
		return err
	}
	old, err := m.createBackup()
	if err != nil {
		return err
	}
	if err = m.replaceData(data); err != nil {
		_, previous, e := m.readBackup(old.ID)
		if e == nil {
			e = m.replaceData(previous)
		}
		if e != nil {
			return Fail("E_RESTORE_FAILED", "恢复失败，自动还原也失败；请保留备份目录")
		}
		return err
	}
	return nil
}

// replaceData 替换白名单文件并移除备份中不存在的文件，只供启动恢复或更新工作进程使用。
func (m *Maintenance) replaceData(data map[string][]byte) error {
	for _, name := range backupFiles {
		path := filepath.Join(m.dir, name)
		if b, ok := data[name]; ok {
			if err := atomicWrite(path, b); err != nil {
				return err
			}
		} else if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	// 旧备份没有数据库时写入空恢复标记，下一次打开 SQLite 会事务性清空，避免混入恢复前的新业务记录。
	if _, ok := data[databaseSnapshotFile]; !ok {
		return atomicWrite(filepath.Join(m.dir, databaseSnapshotFile), []byte(`{"schema":1,"documents":[],"tasks":[]}`))
	}
	return nil
}
