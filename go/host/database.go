package host

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const databaseSnapshotFile = "business-data.json"
const maxRecordBytes = 64 << 10
const maxBusinessBytes = 8 << 20

var databaseField = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// Document 是带乐观锁版本的业务 JSON 记录，时间使用 Unix 毫秒，数据按应用目录和网页来源隔离。
type Document struct {
	Collection string          `json:"collection"`
	ID         string          `json:"id"`
	Data       json.RawMessage `json:"data"`
	Revision   int64           `json:"revision"`
	CreatedAt  int64           `json:"createdAt"`
	UpdatedAt  int64           `json:"updatedAt"`
}

// DocumentQuery 提供集合内分页、顶层字段精确匹配和稳定排序，不接受原始 SQL 或文件路径。
type DocumentQuery struct {
	Collection string                     `json:"collection"`
	Where      map[string]json.RawMessage `json:"where"`
	Offset     int                        `json:"offset"`
	Limit      int                        `json:"limit"`
	SortBy     string                     `json:"sortBy"`
	Descending bool                       `json:"descending"`
}

// DocumentPage 返回真实总数及下一偏移量；响应接近 512 KiB 时提前结束本页，调用方按 nextOffset 继续取。
type DocumentPage struct {
	Items      []Document `json:"items"`
	Total      int        `json:"total"`
	NextOffset int        `json:"nextOffset"`
	HasMore    bool       `json:"hasMore"`
}

// DocumentChange 是原子批处理中的一次写入或删除，revision=-1 无条件、0 仅新建、正数要求版本一致。
type DocumentChange struct {
	Operation  string          `json:"operation"`
	Collection string          `json:"collection"`
	ID         string          `json:"id"`
	Data       json.RawMessage `json:"data"`
	Revision   int64           `json:"revision"`
}

// BusinessDatabase 将业务记录和任务历史放在固定 SQLite 文件中；互斥锁串行化事务和逻辑快照，连接不向网页暴露。
type BusinessDatabase struct {
	mu sync.Mutex
	db *sql.DB
}

// OpenBusinessDatabase 打开当前应用的数据库并处理待恢复快照；创建受限表结构，不加载外部扩展或执行网页 SQL。
func OpenBusinessDatabase(dir string) (*BusinessDatabase, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "business.sqlite")
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, Fail("E_DATABASE", "数据库必须为普通文件")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	connection, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	connection.SetMaxOpenConns(1)
	connection.SetMaxIdleConns(1)
	d := &BusinessDatabase{db: connection}
	var version int
	if err = connection.QueryRow("PRAGMA user_version").Scan(&version); err == nil && version > 1 {
		err = Fail("E_DATABASE_VERSION", "数据库版本高于当前程序")
	}
	if err == nil {
		_, err = connection.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=DELETE; PRAGMA synchronous=FULL; PRAGMA max_page_count=8192;
CREATE TABLE IF NOT EXISTS documents(origin TEXT NOT NULL, collection TEXT NOT NULL, id TEXT NOT NULL, data TEXT NOT NULL, revision INTEGER NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, PRIMARY KEY(origin,collection,id));
CREATE INDEX IF NOT EXISTS documents_page ON documents(origin,collection,updated_at,id);
CREATE TABLE IF NOT EXISTS task_history(id TEXT PRIMARY KEY, origin TEXT NOT NULL, window TEXT NOT NULL, info TEXT NOT NULL);
PRAGMA user_version=1;`)
	}
	if err != nil {
		connection.Close()
		return nil, databaseError(err)
	}
	if data, e := os.ReadFile(filepath.Join(dir, databaseSnapshotFile)); e == nil {
		if err = d.importSnapshot(data); err == nil {
			err = os.Remove(filepath.Join(dir, databaseSnapshotFile))
		}
		if err != nil {
			connection.Close()
			return nil, err
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		connection.Close()
		return nil, e
	}
	return d, nil
}

// Close 在应用退出时释放 SQLite 连接；写入使用事务和 FULL 同步策略，不依赖定时自动保存。
func (d *BusinessDatabase) Close() error { return d.db.Close() }

// databaseError 保留业务错误码，其他数据库错误返回固定信息，不把 SQL、磁盘路径或业务值带到日志。
func databaseError(err error) error {
	if err == nil {
		return nil
	}
	var own *Error
	if errors.As(err, &own) {
		return err
	}
	return Fail("E_DATABASE", "本地数据库操作失败，请检查空间、数据大小或占用情况")
}

// databaseOrigin 检查数据库权限和实际来源；同一来源的多个窗口共享记录，不跨来源读取。
func databaseOrigin(ctx context.Context) (string, error) {
	if err := Require(ctx, "database"); err != nil {
		return "", err
	}
	origin, _ := CallerIdentity(ctx)
	if origin == "" {
		return "", Fail("E_PERMISSION_DENIED", "数据库调用缺少有效来源")
	}
	return origin, nil
}

// documentID 限制集合和记录标识，不把名字作为 SQL 标识符或文件路径使用。
func documentID(collection, id string) error {
	if !validID.MatchString(collection) || !validID.MatchString(id) {
		return Fail("E_INVALID_ARGUMENT", "集合和 ID 应为 1–80 位字母数字或 ._-")
	}
	return nil
}

// scanDocument 将查询列转成文档对象，复用在单条读取和分页中，JSON 始终作为数据返回。
func scanDocument(row interface{ Scan(...any) error }) (Document, error) {
	var document Document
	var data string
	err := row.Scan(&document.Collection, &document.ID, &data, &document.Revision, &document.CreatedAt, &document.UpdatedAt)
	document.Data = json.RawMessage(data)
	return document, err
}

// Get 读取本来源的一条记录，不存在时返回 E_NOT_FOUND，避免把空对象误认为真实业务数据。
func (d *BusinessDatabase) Get(ctx context.Context, collection, id string) (Document, error) {
	origin, err := databaseOrigin(ctx)
	if err != nil {
		return Document{}, err
	}
	if err = documentID(collection, id); err != nil {
		return Document{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	value, err := scanDocument(d.db.QueryRowContext(ctx, "SELECT collection,id,data,revision,created_at,updated_at FROM documents WHERE origin=? AND collection=? AND id=?", origin, collection, id))
	if errors.Is(err, sql.ErrNoRows) {
		return value, Fail("E_NOT_FOUND", "记录不存在")
	}
	return value, databaseError(err)
}

// Query 对集合做有界分页；where 只接受顶层标量精确匹配，字段、值和 JSON 路径均参数化。
func (d *BusinessDatabase) Query(ctx context.Context, query DocumentQuery) (DocumentPage, error) {
	page := DocumentPage{Items: []Document{}}
	origin, err := databaseOrigin(ctx)
	if err != nil {
		return page, err
	}
	if !validID.MatchString(query.Collection) || query.Offset < 0 || query.Offset > 1000000 || query.Limit < 0 || query.Limit > 100 || len(query.Where) > 10 {
		return page, Fail("E_INVALID_ARGUMENT", "集合、分页或过滤数量无效，单页最多 100 条")
	}
	if query.Limit == 0 {
		query.Limit = 20
	}
	where := "origin=? AND collection=?"
	args := []any{origin, query.Collection}
	keys := make([]string, 0, len(query.Where))
	for key := range query.Where {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := query.Where[key]
		var scalar any
		if !databaseField.MatchString(key) || len(value) > 1024 || json.Unmarshal(value, &scalar) != nil {
			return page, Fail("E_INVALID_ARGUMENT", "过滤字段或值无效")
		}
		if _, object := scalar.(map[string]any); object {
			return page, Fail("E_INVALID_ARGUMENT", "过滤值只支持字符串、数字、布尔或 null")
		}
		if _, array := scalar.([]any); array {
			return page, Fail("E_INVALID_ARGUMENT", "过滤值只支持标量")
		}
		where += " AND json_type(data,?)=json_type(?,'$') AND json_extract(data,?) IS json_extract(?,'$')"
		args = append(args, "$."+key, string(value), "$."+key, string(value))
	}
	order := "updated_at"
	switch query.SortBy {
	case "", "updatedAt":
	case "createdAt":
		order = "created_at"
	case "id":
		order = "id"
	default:
		return page, Fail("E_INVALID_ARGUMENT", "排序字段仅支持 updatedAt、createdAt 或 id")
	}
	if query.Descending {
		order += " DESC"
	} else {
		order += " ASC"
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err = d.db.QueryRowContext(ctx, "SELECT count(*) FROM documents WHERE "+where, args...).Scan(&page.Total); err != nil {
		return page, databaseError(err)
	}
	rows, err := d.db.QueryContext(ctx, "SELECT collection,id,data,revision,created_at,updated_at FROM documents WHERE "+where+" ORDER BY "+order+",id ASC LIMIT ? OFFSET ?", append(args, query.Limit, query.Offset)...)
	if err != nil {
		return page, databaseError(err)
	}
	defer rows.Close()
	size := 0
	for rows.Next() {
		value, e := scanDocument(rows)
		if e != nil {
			return page, databaseError(e)
		}
		size += len(value.Data) + 256
		if size > 512<<10 {
			break
		}
		page.Items = append(page.Items, value)
	}
	if err = rows.Err(); err != nil {
		return page, databaseError(err)
	}
	page.NextOffset = query.Offset + len(page.Items)
	page.HasMore = page.NextOffset < page.Total
	return page, nil
}

// Batch 将最多 100 项写入或删除放在同一事务内，任何版本冲突或错误都会回滚整批，业务 JSON 总量限制 8 MiB。
func (d *BusinessDatabase) Batch(ctx context.Context, changes []DocumentChange) ([]Document, error) {
	origin, err := databaseOrigin(ctx)
	if err != nil {
		return nil, err
	}
	if len(changes) == 0 || len(changes) > 100 {
		return nil, Fail("E_INVALID_ARGUMENT", "批处理需要 1–100 项操作")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, databaseError(err)
	}
	defer tx.Rollback()
	results := []Document{}
	for _, change := range changes {
		if err = documentID(change.Collection, change.ID); err != nil {
			return nil, err
		}
		if change.Revision < -1 {
			return nil, Fail("E_INVALID_ARGUMENT", "revision 不能小于 -1")
		}
		old, e := scanDocument(tx.QueryRowContext(ctx, "SELECT collection,id,data,revision,created_at,updated_at FROM documents WHERE origin=? AND collection=? AND id=?", origin, change.Collection, change.ID))
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return nil, databaseError(e)
		}
		exists := e == nil
		if change.Revision >= 0 && (change.Revision == 0 && exists || change.Revision > 0 && (!exists || old.Revision != change.Revision)) {
			return nil, Fail("E_CONFLICT", "记录版本已变化，请重新读取后提交")
		}
		switch change.Operation {
		case "put":
			var object map[string]json.RawMessage
			if len(change.Data) == 0 || len(change.Data) > maxRecordBytes || json.Unmarshal(change.Data, &object) != nil || object == nil {
				return nil, Fail("E_INVALID_ARGUMENT", "数据必须是最多 64 KiB 的 JSON 对象")
			}
			var total, count int
			if err = tx.QueryRowContext(ctx, "SELECT coalesce(sum(length(CAST(data AS BLOB))),0),count(*) FROM documents").Scan(&total, &count); err != nil {
				return nil, databaseError(err)
			}
			if !exists && count >= 20000 {
				return nil, Fail("E_TOO_LARGE", "业务记录最多 20000 条，请归档后继续")
			}
			if total-len(old.Data)+len(change.Data) > maxBusinessBytes {
				return nil, Fail("E_TOO_LARGE", "本地业务数据已达 8 MiB，请归档后继续")
			}
			now := time.Now().UnixMilli()
			created := old.CreatedAt
			if !exists {
				created = now
			}
			value := Document{Collection: change.Collection, ID: change.ID, Data: change.Data, Revision: old.Revision + 1, CreatedAt: created, UpdatedAt: now}
			_, err = tx.ExecContext(ctx, "INSERT INTO documents(origin,collection,id,data,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(origin,collection,id) DO UPDATE SET data=excluded.data,revision=excluded.revision,updated_at=excluded.updated_at", origin, value.Collection, value.ID, string(value.Data), value.Revision, created, now)
			results = append(results, value)
		case "delete":
			_, err = tx.ExecContext(ctx, "DELETE FROM documents WHERE origin=? AND collection=? AND id=?", origin, change.Collection, change.ID)
		default:
			return nil, Fail("E_INVALID_ARGUMENT", "operation 仅支持 put 或 delete")
		}
		if err != nil {
			return nil, databaseError(err)
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, databaseError(err)
	}
	return results, nil
}

// storedDocument 是备份专用的带来源记录，来源不交给普通数据库调用者自由指定。
type storedDocument struct {
	Origin string `json:"origin"`
	Document
}

// databaseSnapshot 是可校验的逻辑备份，避免复制正在写入的 SQLite 数据页或遗漏日志文件。
type databaseSnapshot struct {
	Schema    int              `json:"schema"`
	Documents []storedDocument `json:"documents"`
	Tasks     []storedTask     `json:"tasks"`
}

// exportSnapshot 在数据库锁内读取业务和任务历史的一致快照，供既有备份、恢复和更新回滚使用。
func (d *BusinessDatabase) exportSnapshot() ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	snapshot := databaseSnapshot{Schema: 1, Documents: []storedDocument{}, Tasks: []storedTask{}}
	rows, err := d.db.Query("SELECT origin,collection,id,data,revision,created_at,updated_at FROM documents ORDER BY origin,collection,id")
	if err != nil {
		return nil, databaseError(err)
	}
	for rows.Next() {
		var value storedDocument
		var data string
		if err = rows.Scan(&value.Origin, &value.Collection, &value.ID, &data, &value.Revision, &value.CreatedAt, &value.UpdatedAt); err != nil {
			break
		}
		value.Data = json.RawMessage(data)
		snapshot.Documents = append(snapshot.Documents, value)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, databaseError(err)
	}
	snapshot.Tasks, err = d.loadTaskHistoryLocked()
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(snapshot)
	if len(data) > 16<<20 {
		return nil, Fail("E_TOO_LARGE", "数据库备份超过 16 MiB")
	}
	return data, err
}

// importSnapshot 在启动开放 RPC 前原子恢复所有表；任何非法记录都会撤销事务，旧备份用空快照明确清空新增数据库。
func (d *BusinessDatabase) importSnapshot(data []byte) error {
	var snapshot databaseSnapshot
	if !databaseSnapshotValid(data) || json.Unmarshal(data, &snapshot) != nil {
		return Fail("E_BACKUP_INVALID", "数据库快照格式无效")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.db.Begin()
	if err != nil {
		return databaseError(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM documents; DELETE FROM task_history"); err != nil {
		return databaseError(err)
	}
	total := 0
	for _, value := range snapshot.Documents {
		var object map[string]json.RawMessage
		total += len(value.Data)
		if Origin(value.Origin) != value.Origin || value.Origin == "" || documentID(value.Collection, value.ID) != nil || value.Revision < 1 || len(value.Data) > maxRecordBytes || json.Unmarshal(value.Data, &object) != nil || object == nil || total > maxBusinessBytes {
			return Fail("E_BACKUP_INVALID", "数据库快照包含非法记录")
		}
		if _, err = tx.Exec("INSERT INTO documents VALUES(?,?,?,?,?,?,?)", value.Origin, value.Collection, value.ID, string(value.Data), value.Revision, value.CreatedAt, value.UpdatedAt); err != nil {
			return databaseError(err)
		}
	}
	for _, value := range snapshot.Tasks {
		if err = validateStoredTask(value); err != nil {
			return err
		}
		encoded, _ := json.Marshal(value.Info)
		if _, err = tx.Exec("INSERT INTO task_history VALUES(?,?,?,?)", value.Info.ID, value.Origin, value.Window, string(encoded)); err != nil {
			return databaseError(err)
		}
	}
	return databaseError(tx.Commit())
}

// snapshotDatabaseFile 为尚未注册服务的迁移/更新进程读取快照，不启动后台任务，不消费待恢复标记。
func snapshotDatabaseFile(dir string) ([]byte, error) {
	path := filepath.Join(dir, databaseSnapshotFile)
	if data, err := os.ReadFile(path); err == nil {
		return data, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(dir, "business.sqlite")); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	db, err := OpenBusinessDatabase(dir)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return db.exportSnapshot()
}

// databaseSnapshotValid 在覆盖任何业务文件前检查全部记录、重复主键和容量，避免恢复一半才发现数据库不能导入。
func databaseSnapshotValid(data []byte) bool {
	var value databaseSnapshot
	if len(data) > 16<<20 || json.Unmarshal(data, &value) != nil || value.Schema != 1 || len(value.Documents) > 20000 || len(value.Tasks) > 500 {
		return false
	}
	seen := map[string]bool{}
	total := 0
	for _, record := range value.Documents {
		key := record.Origin + "\n" + record.Collection + "\n" + record.ID
		var object map[string]json.RawMessage
		total += len(record.Data)
		if seen[key] || Origin(record.Origin) != record.Origin || record.Origin == "" || documentID(record.Collection, record.ID) != nil || record.Revision < 1 || record.CreatedAt < 0 || record.UpdatedAt < 0 || len(record.Data) > maxRecordBytes || json.Unmarshal(record.Data, &object) != nil || object == nil || total > maxBusinessBytes {
			return false
		}
		seen[key] = true
	}
	seen = map[string]bool{}
	for _, task := range value.Tasks {
		if seen[task.Info.ID] || validateStoredTask(task) != nil {
			return false
		}
		seen[task.Info.ID] = true
	}
	return true
}
