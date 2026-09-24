package cacheaudit

import (
	"errors"
	"testing"
)

// counterClock 返回可注入的单调时钟。
func counterClock() func() int64 {
	var t int64
	return func() int64 { t++; return t }
}

func TestLogBoundedAndReportsDrops(t *testing.T) {
	log := NewLog(3, counterClock())
	for i := 0; i < 5; i++ {
		log.Append(KindMiss, "k", 0, "", "", int64(i), int64(i))
	}
	evs := log.Events()
	if len(evs) != 3 {
		t.Fatalf("log size = %d, want bounded 3", len(evs))
	}
	if log.Dropped() != 2 {
		t.Fatalf("dropped = %d, want 2", log.Dropped())
	}
	if log.Complete() {
		t.Fatal("log with drops must not be complete")
	}
	// 现存事件应保持 Seq 升序且为最旧的被覆盖。
	for i := 1; i < len(evs); i++ {
		if evs[i].Seq <= evs[i-1].Seq {
			t.Fatalf("events out of order: %v", evs)
		}
	}
	if evs[0].Seq != 3 {
		t.Fatalf("oldest surviving seq = %d, want 3", evs[0].Seq)
	}
}

func TestUnpublishedLoadIsNotAHit(t *testing.T) {
	c := NewCache(64, func(key string) (string, uint64, error) {
		return "v1", 1, nil
	}, counterClock(), nil)
	defer c.Close()

	if _, _, err := c.Get("a"); err != nil { // miss + load，未发布
		t.Fatal(err)
	}
	if _, _, err := c.Get("a"); err != nil { // 仍未发布：必须记为失效而非命中
		t.Fatal(err)
	}
	if s := c.Stats(); s.Hits != 0 || s.Misses != 2 {
		t.Fatalf("before publish: %+v, want 0 hits 2 misses", s)
	}
	if !c.Publish("a") {
		t.Fatal("publish should succeed")
	}
	if _, ver, err := c.Get("a"); err != nil || ver != 1 {
		t.Fatalf("hit after publish: ver=%d err=%v", ver, err)
	}
	s := c.Stats()
	if s.Hits != 1 || s.Misses != 2 {
		t.Fatalf("after publish: %+v, want 1 hit 2 misses", s)
	}
	// 分母口径固定为 hits+misses。
	if got, want := s.HitRate(), 1.0/3.0; got != want {
		t.Fatalf("hit rate = %v, want %v", got, want)
	}
	if (Stats{}).HitRate() != 0 {
		t.Fatal("empty stats hit rate must be 0")
	}
}

func TestLoaderFailureIsInjectableAndCountedAsMiss(t *testing.T) {
	boom := errors.New("source down")
	c := NewCache(64, func(key string) (string, uint64, error) {
		return "", 0, boom
	}, counterClock(), nil)
	defer c.Close()

	if _, _, err := c.Get("a"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want injected %v", err, boom)
	}
	if s := c.Stats(); s.Misses != 1 || s.Hits != 0 {
		t.Fatalf("stats = %+v, want 1 miss", s)
	}
	var loadErr *Event
	for _, ev := range c.Log().Events() {
		if ev.Kind == KindLoad {
			e := ev
			loadErr = &e
		}
	}
	if loadErr == nil || loadErr.Err != "source down" {
		t.Fatalf("load failure not recorded: %+v", loadErr)
	}
}

func TestEvictRemovesEntry(t *testing.T) {
	c := NewCache(64, func(key string) (string, uint64, error) {
		return "v1", 1, nil
	}, counterClock(), nil)
	defer c.Close()

	c.Get("a")
	c.Publish("a")
	if !c.Evict("a") {
		t.Fatal("evict should succeed")
	}
	if c.Evict("a") {
		t.Fatal("second evict should report no entry")
	}
	if _, _, err := c.Get("a"); err != nil { // 淘汰后再次失效
		t.Fatal(err)
	}
	if s := c.Stats(); s.Misses != 2 {
		t.Fatalf("stats = %+v, want 2 misses", s)
	}
}

func TestCloseReleasesResourcesExactlyOnce(t *testing.T) {
	calls := 0
	c := NewCache(64, func(key string) (string, uint64, error) {
		return "v", 1, nil
	}, counterClock(), func() error { calls++; return nil })

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil { // 幂等
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("cleanup called %d times, want exactly 1", calls)
	}
	if _, _, err := c.Get("a"); !errors.Is(err, ErrClosed) {
		t.Fatalf("get after close: err = %v, want ErrClosed", err)
	}
	if c.Publish("a") || c.Evict("a") {
		t.Fatal("operations after close must be no-ops")
	}
}
