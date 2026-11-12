package netsec

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func testOps() VersionOps {
	return VersionOps{
		Satisfies: func(v, c string) bool {
			switch {
			case c == "" || c == "*":
				return true
			case strings.HasPrefix(c, ">="):
				return !lessVer(v, c[2:])
			default:
				return v == c
			}
		},
		Less: lessVer,
	}
}

func lessVer(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		na, _ := strconv.Atoi(pa[i])
		nb, _ := strconv.Atoi(pb[i])
		if na != nb {
			return na < nb
		}
	}
	return len(pa) < len(pb)
}

type spyCache struct {
	mu      sync.Mutex
	entries []string
}

func (c *spyCache) Put(id, version string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, id+"@"+version)
	return nil
}

func (c *spyCache) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func mustMap(t *testing.T, entries ...MapEntry) *SourceMap {
	t.Helper()
	m, err := NewSourceMap(entries...)
	if err != nil {
		t.Fatalf("NewSourceMap: %v", err)
	}
	return m
}

func publish(t *testing.T, idx *Index, id, version string, deps ...Requirement) {
	t.Helper()
	idx.add(id, version, deps)
}

// 公共源抢发同名高版本：映射只允许私有源，绝不取公共源的更高版本。
func TestRestoreIgnoresPublicHighVersion(t *testing.T) {
	private := NewIndex("private")
	public := NewIndex("public")
	publish(t, private, "corp.auth", "1.2.0")
	publish(t, public, "corp.auth", "9.9.9") // 攻击者抢发的高版本

	r := NewResolver(testOps(), private, public)
	r.ReplaceMap(mustMap(t, MapEntry{Pattern: "corp.*", Sources: []string{"private"}}))

	cache := &spyCache{}
	err := r.Restore([]Requirement{{ID: "corp.auth", Constraint: ">=1.0.0"}}, cache)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := cache.entries; len(got) != 1 || got[0] != "corp.auth@1.2.0" {
		t.Fatalf("cache = %v, want [corp.auth@1.2.0]", got)
	}
}

// 无回退：映射源缺少满足约束的版本时失败，不回退到公共源。
func TestNoFallbackToOtherSources(t *testing.T) {
	private := NewIndex("private")
	public := NewIndex("public")
	publish(t, private, "corp.auth", "1.0.0")
	publish(t, public, "corp.auth", "2.0.0")

	r := NewResolver(testOps(), private, public)
	r.ReplaceMap(mustMap(t, MapEntry{Pattern: "corp.*", Sources: []string{"private"}}))

	cache := &spyCache{}
	err := r.Restore([]Requirement{{ID: "corp.auth", Constraint: ">=2.0.0"}}, cache)
	if !errors.Is(err, ErrNoVersion) {
		t.Fatalf("err = %v, want ErrNoVersion", err)
	}
	if cache.count() != 0 {
		t.Fatalf("cache has %d entries, want 0", cache.count())
	}
}

// 最长前缀匹配：corp.db.* 比 corp.* 更具体，字面 ID 又比前缀更具体。
func TestLongestPrefixWins(t *testing.T) {
	a := NewIndex("a")
	b := NewIndex("b")
	c := NewIndex("c")
	publish(t, a, "corp.web", "1.0.0")
	publish(t, b, "corp.db.pool", "2.1.0")
	publish(t, c, "corp.db.pool.hot", "3.0.0")

	r := NewResolver(testOps(), a, b, c)
	r.ReplaceMap(mustMap(t,
		MapEntry{Pattern: "corp.*", Sources: []string{"a"}},
		MapEntry{Pattern: "corp.db.*", Sources: []string{"b"}},
		MapEntry{Pattern: "corp.db.pool.hot", Sources: []string{"c"}},
	))

	cache := &spyCache{}
	err := r.Restore([]Requirement{
		{ID: "corp.web", Constraint: "*"},
		{ID: "corp.db.pool", Constraint: "*"},
		{ID: "corp.db.pool.hot", Constraint: "*"},
	}, cache)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	want := map[string]bool{
		"corp.web@1.0.0":        true,
		"corp.db.pool@2.1.0":    true,
		"corp.db.pool.hot@3.0.0": true,
	}
	for _, e := range cache.entries {
		delete(want, e)
	}
	if len(want) != 0 {
		t.Fatalf("missing cache entries: %v (got %v)", want, cache.entries)
	}
}

// ID 比较不分大小写：模式、查询、索引任意大小写混合都能匹配。
func TestCaseInsensitiveMatching(t *testing.T) {
	idx := NewIndex("private")
	publish(t, idx, "Corp.DB.Pool", "2.1.0")

	r := NewResolver(testOps(), idx)
	r.ReplaceMap(mustMap(t, MapEntry{Pattern: "CORP.db.*", Sources: []string{"private"}}))

	cache := &spyCache{}
	err := r.Restore([]Requirement{{ID: "cOrP.Db.PoOl", Constraint: ">=2.0.0"}}, cache)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := cache.entries; len(got) != 1 || got[0] != "corp.db.pool@2.1.0" {
		t.Fatalf("cache = %v, want [corp.db.pool@2.1.0]", got)
	}
}

// 依赖图深处无映射：整次还原失败，缓存不得写入任何条目。
func TestDeepUnmappedFailsAndCacheStaysEmpty(t *testing.T) {
	idx := NewIndex("private")
	publish(t, idx, "corp.app", "1.0.0", Requirement{ID: "corp.mid", Constraint: ">=1.0.0"})
	publish(t, idx, "corp.mid", "1.1.0", Requirement{ID: "corp.deep", Constraint: ">=1.0.0"})
	publish(t, idx, "corp.deep", "1.0.0")

	r := NewResolver(testOps(), idx)
	r.ReplaceMap(mustMap(t,
		MapEntry{Pattern: "corp.app", Sources: []string{"private"}},
		MapEntry{Pattern: "corp.mid", Sources: []string{"private"}},
		// corp.deep 故意无映射
	))

	cache := &spyCache{}
	err := r.Restore([]Requirement{{ID: "corp.app", Constraint: "*"}}, cache)
	if !errors.Is(err, ErrNoSource) {
		t.Fatalf("err = %v, want ErrNoSource", err)
	}
	if cache.count() != 0 {
		t.Fatalf("cache has %d entries after failed restore, want 0", cache.count())
	}
}

// hookSource 在查询依赖后触发回调，用于模拟还原途中的管理员换表。
type hookSource struct {
	Source
	afterDeps func(id string)
}

func (h *hookSource) Deps(id, version string) []Requirement {
	reqs := h.Source.Deps(id, version)
	if h.afterDeps != nil {
		h.afterDeps(id)
	}
	return reqs
}

// 还原途中换表：一次还原只用开始时的快照，不得混用新旧模式。
func TestRestoreUsesSnapshotMap(t *testing.T) {
	private := NewIndex("private")
	evil := NewIndex("evil")
	publish(t, private, "corp.app", "1.0.0", Requirement{ID: "corp.lib", Constraint: ">=1.0.0"})
	publish(t, private, "corp.lib", "1.1.0")
	publish(t, evil, "corp.lib", "9.9.9")

	var r *Resolver
	var once sync.Once
	hooked := &hookSource{
		Source: private,
		afterDeps: func(id string) {
			if id == "corp.app" {
				once.Do(func() {
					// 还原进行中，管理员把 corp.* 改指到 evil
					r.ReplaceMap(mustMap(t, MapEntry{Pattern: "corp.*", Sources: []string{"evil"}}))
				})
			}
		},
	}
	r = NewResolver(testOps(), hooked, evil)
	r.ReplaceMap(mustMap(t, MapEntry{Pattern: "corp.*", Sources: []string{"private"}}))

	cache := &spyCache{}
	err := r.Restore([]Requirement{{ID: "corp.app", Constraint: "*"}}, cache)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	for _, e := range cache.entries {
		if e == "corp.lib@9.9.9" {
			t.Fatalf("restore mixed in new table mid-flight: %v", cache.entries)
		}
	}
	if len(cache.entries) != 2 {
		t.Fatalf("cache = %v, want 2 entries", cache.entries)
	}
}

// 同一保留前缀并发登记只有一个成功。
func TestConcurrentReserveSingleWinner(t *testing.T) {
	reg := NewRegistry()
	const n = 64
	var wins atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := reg.Reserve("corp.", fmt.Sprintf("owner-%d", i)); err == nil {
				wins.Add(1)
			} else if !errors.Is(err, ErrPrefixOwned) {
				t.Errorf("unexpected err: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("wins = %d, want exactly 1", wins.Load())
	}
}

// 保留前缀必须以点结尾。
func TestReserveRequiresDotSuffix(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Reserve("corp", "alice"); !errors.Is(err, ErrBadPrefix) {
		t.Fatalf("err = %v, want ErrBadPrefix", err)
	}
}

// 非所有者发布落入保留前缀的 ID 被拒，且不得出现在任何源的索引里。
func TestPublishRejectedNeverIndexed(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Reserve("corp.", "alice"); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	gate := NewGate(reg)
	private := NewIndex("private")
	public := NewIndex("public")

	err := gate.Publish(private, "mallory", "Corp.Auth", "9.9.9", nil)
	if !errors.Is(err, ErrPublishRejected) {
		t.Fatalf("err = %v, want ErrPublishRejected", err)
	}
	if vs := private.Versions("corp.auth"); len(vs) != 0 {
		t.Fatalf("rejected publish leaked into private index: %v", vs)
	}
	if vs := public.Versions("corp.auth"); len(vs) != 0 {
		t.Fatalf("rejected publish leaked into public index: %v", vs)
	}

	if err := gate.Publish(private, "alice", "corp.auth", "1.0.0", nil); err != nil {
		t.Fatalf("owner publish: %v", err)
	}
	if vs := private.Versions("corp.auth"); len(vs) != 1 || vs[0] != "1.0.0" {
		t.Fatalf("owner publish missing from index: %v", vs)
	}

	// 保留前缀之外的 ID 任何人可发布
	if err := gate.Publish(public, "mallory", "other.lib", "1.0.0", nil); err != nil {
		t.Fatalf("unreserved publish: %v", err)
	}
}
