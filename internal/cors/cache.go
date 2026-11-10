package cors

import (
	"sort"
	"strings"
)

// cacheKey 对应 Fetch 规范的预检缓存条目键：源 + URL + 凭据模式。
// 方法与头不作为键——同键条目内部记录已授权的方法/头集合。
type cacheKey struct {
	Source        string
	URLNoFragment string
	Credentials   bool
}

type cacheEntry struct {
	methods   map[string]bool
	headers   map[string]bool
	expiresAt int64 // -1 表示无 Max-Age，浏览器关闭前有效；本工具中视为不过期
	cachedAt  int64
}

// PreflightCache 是按源、目标、凭据条件隔离的预检缓存。
type PreflightCache struct {
	entries map[cacheKey]*cacheEntry
	now     int64
}

func NewPreflightCache() *PreflightCache {
	return &PreflightCache{entries: map[cacheKey]*cacheEntry{}}
}

func (c *PreflightCache) SetTime(now int64) { c.now = now }

func makeKey(source Origin, targetURL string, credentials bool) (cacheKey, error) {
	noFragment, _, err := splitFragment(targetURL)
	if err != nil {
		return cacheKey{}, err
	}
	return cacheKey{
		Source:        source.String(),
		URLNoFragment: noFragment,
		Credentials:   credentials,
	}, nil
}

// Lookup 查询缓存：过期条目先删除。返回（是否命中，缓存条目）。
func (c *PreflightCache) Lookup(source Origin, targetURL string, credentials bool,
	method string, headers []string) (bool, error) {

	key, err := makeKey(source, targetURL, credentials)
	if err != nil {
		return false, err
	}
	entry, ok := c.entries[key]
	if !ok {
		return false, nil
	}
	if entry.expiresAt >= 0 && c.now >= entry.expiresAt {
		delete(c.entries, key)
		return false, nil
	}
	upper := strings.ToUpper(method)
	if !entry.methods[upper] {
		return false, nil
	}
	for _, header := range headers {
		if !entry.headers[header] {
			return false, nil
		}
	}
	return true, nil
}

// Store 写入一次通过的预检；无 Max-Age 时用 -1 表示会话级有效。
func (c *PreflightCache) Store(source Origin, targetURL string, credentials bool,
	method string, headers []string, maxAge int64) error {

	key, err := makeKey(source, targetURL, credentials)
	if err != nil {
		return err
	}
	entry, ok := c.entries[key]
	if !ok {
		entry = &cacheEntry{methods: map[string]bool{}, headers: map[string]bool{}}
		c.entries[key] = entry
	}
	entry.methods[strings.ToUpper(method)] = true
	for _, header := range headers {
		entry.headers[header] = true
	}
	entry.cachedAt = c.now
	if maxAge <= 0 {
		entry.expiresAt = -1
	} else {
		entry.expiresAt = c.now + maxAge
	}
	return nil
}

// Snapshot 返回便于报告的缓存视图，按键排序。
type CacheSnapshotRow struct {
	Source      string   `json:"source"`
	Target      string   `json:"target"`
	Credentials bool     `json:"credentials"`
	Methods     []string `json:"methods"`
	Headers     []string `json:"headers"`
	CachedAt    int64    `json:"cached_at"`
	ExpiresAt   int64    `json:"expires_at"` // -1 表示会话级
	Live        bool     `json:"live"`
}

func (c *PreflightCache) Snapshot() []CacheSnapshotRow {
	rows := make([]CacheSnapshotRow, 0, len(c.entries))
	for key, entry := range c.entries {
		live := entry.expiresAt < 0 || c.now < entry.expiresAt
		methods := make([]string, 0, len(entry.methods))
		for method := range entry.methods {
			methods = append(methods, method)
		}
		sort.Strings(methods)
		headers := make([]string, 0, len(entry.headers))
		for header := range entry.headers {
			headers = append(headers, header)
		}
		sort.Strings(headers)
		rows = append(rows, CacheSnapshotRow{
			Source:      key.Source,
			Target:      key.URLNoFragment,
			Credentials: key.Credentials,
			Methods:     methods,
			Headers:     headers,
			CachedAt:    entry.cachedAt,
			ExpiresAt:   entry.expiresAt,
			Live:        live,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Source != rows[j].Source {
			return rows[i].Source < rows[j].Source
		}
		if rows[i].Target != rows[j].Target {
			return rows[i].Target < rows[j].Target
		}
		return !rows[i].Credentials && rows[j].Credentials
	})
	return rows
}

func splitFragment(raw string) (string, string, error) {
	if idx := indexByte(raw, '#'); idx >= 0 {
		return raw[:idx], raw[idx+1:], nil
	}
	return raw, "", nil
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
