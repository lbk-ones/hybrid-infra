package host

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// CacheValue 包含命中状态和修订号；不存在与 JSON null 分别用 found=false 与 found=true 区分。
type CacheValue struct {
	Found     bool            `json:"found"`
	Value     json.RawMessage `json:"value"`
	Revision  uint64          `json:"revision"`
	ExpiresAt int64           `json:"expiresAt"`
}
type cacheKey struct{ scope, namespace, key string }
type cacheItem struct {
	value   CacheValue
	expires time.Time
}

// SharedCache 是进程共享的有界内存缓存；默认同来源跨窗口共享，显式 global 权限才允许跨来源共享，不落磁盘。
type SharedCache struct {
	mu       sync.Mutex
	entries  map[cacheKey]cacheItem
	revision uint64
	bytes    int
}

// NewSharedCache 创建空缓存，生命周期随桌面进程结束；持久化数据仍使用已有 Store 服务。
func NewSharedCache() *SharedCache { return &SharedCache{entries: map[cacheKey]cacheItem{}} }

// sharedScope 为共享资源计算来源命名空间；全局空间需要额外授权，不能通过自行填写来源字符串绕过。
func sharedScope(ctx context.Context, capability string, global bool) (string, error) {
	if err := Require(ctx, capability); err != nil {
		return "", err
	}
	if global {
		if err := Require(ctx, capability+".global"); err != nil {
			return "", err
		}
		return "app", nil
	}
	p, _ := ctx.Value(principalKey{}).(Principal)
	return "origin:" + p.Origin, nil
}

// cacheName 校验命名空间和键，不允许空名称、控制字符或过长名称；名称不作为磁盘路径使用。
func cacheName(value string) bool {
	return len(value) > 0 && len(value) <= 120 && !strings.ContainsAny(value, "\x00\r\n")
}

// copyCacheValue 复制 JSON 字节，防止调用者在锁外改写缓存内部数据。
func copyCacheValue(v CacheValue) CacheValue {
	v.Value = append(json.RawMessage(nil), v.Value...)
	return v
}

// prune 在持锁时回收过期条目，使 TTL 同时释放内存配额。
func (c *SharedCache) prune() {
	for key, item := range c.entries {
		if !item.expires.IsZero() && !time.Now().Before(item.expires) {
			c.bytes -= len(item.value.Value)
			delete(c.entries, key)
		}
	}
}

// Get 读取指定命名空间的缓存，过期或不存在返回 found=false，不把不存在误判为 null 值。
func (c *SharedCache) Get(ctx context.Context, namespace, key string, global bool) (CacheValue, error) {
	scope, err := sharedScope(ctx, "cache", global)
	if err != nil {
		return CacheValue{}, err
	}
	if !cacheName(namespace) || !cacheName(key) {
		return CacheValue{}, Fail("E_INVALID_ARGUMENT", "命名空间和键应为 1–120 字节")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune()
	return copyCacheValue(c.entries[cacheKey{scope, namespace, key}].value), nil
}

// Set 保存最多 64 KiB 的 JSON，ttlMs=0 表示进程内不过期；compare=true 时只在修订号匹配时更新，0 表示要求键不存在。
func (c *SharedCache) Set(ctx context.Context, namespace, key string, value json.RawMessage, ttlMs int64, global, compare bool, revision uint64) (CacheValue, error) {
	scope, err := sharedScope(ctx, "cache", global)
	if err != nil {
		return CacheValue{}, err
	}
	if !cacheName(namespace) || !cacheName(key) || !json.Valid(value) || len(value) > 65536 || ttlMs < 0 || ttlMs > 7*24*60*60*1000 {
		return CacheValue{}, Fail("E_INVALID_ARGUMENT", "缓存键、JSON、大小或 TTL 无效，单值最多 64 KiB，TTL 最多 7 天")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prune()
	id := cacheKey{scope, namespace, key}
	old, exists := c.entries[id]
	if compare && old.value.Revision != revision {
		return CacheValue{}, Fail("E_CONFLICT", "缓存已被其他窗口修改，请重新读取后重试")
	}
	if (!exists && len(c.entries) >= 4096) || c.bytes-len(old.value.Value)+len(value) > 16*1024*1024 {
		return CacheValue{}, Fail("E_LIMIT_EXCEEDED", "缓存最多 4096 项、总计 16 MiB")
	}
	c.revision++
	v := CacheValue{Found: true, Value: append(json.RawMessage(nil), value...), Revision: c.revision}
	item := cacheItem{value: v}
	if ttlMs > 0 {
		item.expires = time.Now().Add(time.Duration(ttlMs) * time.Millisecond)
		item.value.ExpiresAt = item.expires.UnixMilli()
	}
	c.entries[id] = item
	c.bytes += len(value) - len(old.value.Value)
	return copyCacheValue(item.value), nil
}

// Delete 移除当前空间中的单个键，重复删除不存在的键返回成功。
func (c *SharedCache) Delete(ctx context.Context, namespace, key string, global bool) error {
	scope, err := sharedScope(ctx, "cache", global)
	if err != nil {
		return err
	}
	if !cacheName(namespace) || !cacheName(key) {
		return Fail("E_INVALID_ARGUMENT", "缓存命名空间或键无效")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	id := cacheKey{scope, namespace, key}
	c.bytes -= len(c.entries[id].value.Value)
	delete(c.entries, id)
	return nil
}

// Clear 仅清理调用者指定的命名空间，不能通过空参数清空其他来源或全部业务缓存。
func (c *SharedCache) Clear(ctx context.Context, namespace string, global bool) error {
	scope, err := sharedScope(ctx, "cache", global)
	if err != nil {
		return err
	}
	if !cacheName(namespace) {
		return Fail("E_INVALID_ARGUMENT", "缓存命名空间无效")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, item := range c.entries {
		if id.scope == scope && id.namespace == namespace {
			c.bytes -= len(item.value.Value)
			delete(c.entries, id)
		}
	}
	return nil
}
