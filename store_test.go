package tagcache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func newStore(opts Options) *Store {
	if opts.Source == nil {
		opts.Source = func(_ context.Context, key string) (any, error) {
			return "value-" + key, nil
		}
	}
	return New(opts)
}

func mustGet(t *testing.T, s *Store, key string, tags ...string) any {
	t.Helper()
	v, err := s.Get(context.Background(), key, tags...)
	if err != nil {
		t.Fatalf("Get(%q) failed: %v", key, err)
	}
	return v
}

// 同条目两个标签不重复计费：一次加载只计 1 次，命中不再计费。
func TestMultipleTagsSingleBilling(t *testing.T) {
	s := newStore(Options{})
	mustGet(t, s, "k1", "tagA", "tagB", "tagA") // duplicate tag ignored
	if got := s.Stats().BilledLoads; got != 1 {
		t.Fatalf("BilledLoads = %d, want 1", got)
	}
	mustGet(t, s, "k1", "tagA")
	mustGet(t, s, "k1", "tagB")
	if got := s.Stats().BilledLoads; got != 1 {
		t.Fatalf("BilledLoads after hits = %d, want 1", got)
	}
}

// 标签版本变化使相关条目失效并触发重新加载。
func TestTagVersionInvalidatesEntries(t *testing.T) {
	var n int
	s := newStore(Options{
		Source: func(_ context.Context, key string) (any, error) {
			n++
			return fmt.Sprintf("v%d", n), nil
		},
	})
	if v := mustGet(t, s, "k1", "tagA"); v != "v1" {
		t.Fatalf("got %v, want v1", v)
	}
	s.InvalidateTag("tagA")
	if v := mustGet(t, s, "k1", "tagA"); v != "v2" {
		t.Fatalf("after invalidation got %v, want v2", v)
	}
	if got := s.Stats().BilledLoads; got != 2 {
		t.Fatalf("BilledLoads = %d, want 2", got)
	}
}

// 删除条目清理反向索引，资源（空索引集合）被释放。
func TestDeleteCleansReverseIndex(t *testing.T) {
	s := newStore(Options{})
	mustGet(t, s, "k1", "tagA", "tagB")
	mustGet(t, s, "k2", "tagA")
	s.Delete("k1")
	if _, ok := s.tagIndex["tagB"]; ok {
		t.Fatal("tagB reverse index not released after Delete")
	}
	if _, ok := s.tagIndex["tagA"]["k1"]; ok {
		t.Fatal("k1 still present in tagA reverse index")
	}
	s.Delete("k2")
	st := s.Stats()
	if st.Entries != 0 || st.Tags != 0 {
		t.Fatalf("resources not released: %+v", st)
	}
}

// 加载中标签变化需代际校验：提交时发现代际变化则丢弃并重试。
func TestGenerationCheckDuringLoad(t *testing.T) {
	var s *Store
	var calls int
	s = newStore(Options{
		Source: func(_ context.Context, key string) (any, error) {
			calls++
			if calls == 1 {
				s.InvalidateTag("tagA") // tag changes mid-load
				return "stale", nil
			}
			return "fresh", nil
		},
		MaxLoadAttempts: 3,
	})
	v, err := s.Get(context.Background(), "k1", "tagA")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if v != "fresh" {
		t.Fatalf("got %v, want fresh (stale load must be discarded)", v)
	}
	if calls != 2 {
		t.Fatalf("source calls = %d, want 2", calls)
	}
	// 竞态不留下旧有效条目：仓库中只有按新代际提交的条目。
	if len(s.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(s.entries))
	}
	if s.entries["k1"].tags["tagA"] != s.tagGen["tagA"] {
		t.Fatal("stored entry generation does not match current tag generation")
	}
}

// 失效与写入持续竞态时耗尽重试，走失败处理且不留下旧有效条目。
func TestGenerationConflictExhaustsAttempts(t *testing.T) {
	var s *Store
	var failedKey string
	var failedErr error
	s = newStore(Options{
		Source: func(_ context.Context, key string) (any, error) {
			s.InvalidateTag("tagA") // every load races with invalidation
			return "stale", nil
		},
		MaxLoadAttempts: 3,
		OnFailure: func(key string, err error) {
			failedKey, failedErr = key, err
		},
	})
	_, err := s.Get(context.Background(), "k1", "tagA")
	if !errors.Is(err, ErrGenerationConflict) {
		t.Fatalf("err = %v, want ErrGenerationConflict", err)
	}
	if failedKey != "k1" || !errors.Is(failedErr, ErrGenerationConflict) {
		t.Fatalf("failure handler got (%q, %v)", failedKey, failedErr)
	}
	if got := s.Stats().Entries; got != 0 {
		t.Fatalf("stale entry left behind, entries = %d", got)
	}
	if got := s.Stats().BilledLoads; got != 0 {
		t.Fatalf("discarded loads billed: %d", got)
	}
}

// 删标签不误删其他标签关系：条目保留并继续通过其余标签命中。
func TestDeleteTagKeepsOtherTagRelations(t *testing.T) {
	s := newStore(Options{})
	mustGet(t, s, "k1", "tagA", "tagB")
	s.DeleteTag("tagA")
	if _, ok := s.tagIndex["tagA"]; ok {
		t.Fatal("tagA index not released")
	}
	if _, ok := s.tagGen["tagA"]; ok {
		t.Fatal("tagA generation state not released")
	}
	if _, ok := s.tagIndex["tagB"]["k1"]; ok != true {
		t.Fatal("tagB relation wrongly removed")
	}
	if v := mustGet(t, s, "k1", "tagB"); v != "value-k1" {
		t.Fatalf("got %v, want cached value-k1", v)
	}
	if got := s.Stats().BilledLoads; got != 1 {
		t.Fatalf("BilledLoads = %d, want 1 (no reload)", got)
	}
}

// 失效只驱逐相关条目，其他标签下的条目不受影响。
func TestInvalidateTagEvictsOnlyRelated(t *testing.T) {
	s := newStore(Options{})
	mustGet(t, s, "k1", "tagA")
	mustGet(t, s, "k2", "tagB")
	s.InvalidateTag("tagA")
	if v := mustGet(t, s, "k2", "tagB"); v != "value-k2" {
		t.Fatalf("k2 wrongly evicted, got %v", v)
	}
	if got := s.Stats().BilledLoads; got != 2 {
		t.Fatalf("BilledLoads = %d, want 2 (k2 served from cache)", got)
	}
	if _, ok := s.entries["k1"]; ok {
		t.Fatal("k1 not evicted by tagA invalidation")
	}
}

// 数据源失败通过注入的失败处理上报，且不落缓存。
func TestSourceFailurePropagates(t *testing.T) {
	boom := errors.New("boom")
	var gotKey string
	var gotErr error
	s := newStore(Options{
		Source:    func(_ context.Context, key string) (any, error) { return nil, boom },
		OnFailure: func(key string, err error) { gotKey, gotErr = key, err },
	})
	_, err := s.Get(context.Background(), "k1", "tagA")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if gotKey != "k1" || !errors.Is(gotErr, boom) {
		t.Fatalf("failure handler got (%q, %v)", gotKey, gotErr)
	}
	if got := s.Stats().Entries; got != 0 {
		t.Fatalf("failed load cached, entries = %d", got)
	}
}

// 时钟可注入：推进假时钟使条目过期并重新加载。
func TestExpiryUsesInjectedClock(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s := newStore(Options{
		Clock: func() time.Time { return now },
		TTL:   time.Minute,
	})
	mustGet(t, s, "k1", "tagA")
	now = now.Add(2 * time.Minute)
	mustGet(t, s, "k1", "tagA")
	if got := s.Stats().BilledLoads; got != 2 {
		t.Fatalf("BilledLoads = %d, want 2 (expired entry reloaded)", got)
	}
	if got := s.Stats().Entries; got != 1 {
		t.Fatalf("expired entry not released, entries = %d", got)
	}
}

// 并发失效与写入竞态：结束后内部状态必须自洽，无旧有效条目。
func TestConcurrentInvalidateAndWrite(t *testing.T) {
	s := newStore(Options{MaxLoadAttempts: 50})
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_, _ = s.Get(context.Background(), fmt.Sprintf("k%d", i%8), "tagA", "tagB")
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if i%2 == 0 {
				s.InvalidateTag("tagA")
			} else {
				s.InvalidateTag("tagB")
			}
		}
	}()
	wg.Wait()
	assertConsistent(t, s)
}

// assertConsistent 校验：每个条目携带的代际都是当前代际（无旧有效条目），
// 且正反向索引完全一致。
func assertConsistent(t *testing.T, s *Store) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, e := range s.entries {
		for tag, gen := range e.tags {
			if s.tagGen[tag] != gen {
				t.Errorf("entry %q has stale generation for tag %q", key, tag)
			}
			if _, ok := s.tagIndex[tag][key]; !ok {
				t.Errorf("entry %q missing from reverse index of tag %q", key, tag)
			}
		}
	}
	for tag, set := range s.tagIndex {
		if len(set) == 0 {
			t.Errorf("empty reverse index set leaked for tag %q", tag)
		}
		for key := range set {
			e, ok := s.entries[key]
			if !ok {
				t.Errorf("reverse index %q points at deleted entry %q", tag, key)
				continue
			}
			if _, ok := e.tags[tag]; !ok {
				t.Errorf("entry %q does not reference tag %q in its tag set", key, tag)
			}
		}
	}
}
