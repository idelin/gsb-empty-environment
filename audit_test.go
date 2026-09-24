package cacheaudit

import (
	"math/rand"
	"testing"
)

// ev 构造测试事件；seq 同时用于排序与来源引用。
func ev(seq uint64, start, end int64, kind Kind, key string, ver uint64, src string) Event {
	return Event{Seq: seq, Start: start, End: end, Kind: kind, Key: key, Version: ver, Source: src}
}

func TestAuditHealthyCacheHistory(t *testing.T) {
	c := NewCache(64, func(key string) (string, uint64, error) {
		return "v1", 1, nil
	}, counterClock(), nil)
	defer c.Close()

	c.Get("a")
	c.Publish("a")
	c.Get("a")
	c.Get("b")
	c.Publish("b")
	c.Get("b")

	rep := Audit(c.Log().Events(), c.Log().Dropped())
	if rep.Verdict != Linearizable {
		t.Fatalf("verdict = %v (%s), want LINEARIZABLE", rep.Verdict, rep.Reason)
	}
}

func TestAuditDetectsDefiniteStaleHit(t *testing.T) {
	events := []Event{
		ev(1, 1, 2, KindLoad, "a", 1, "loader"),
		ev(2, 3, 3, KindPublish, "a", 1, "load:1"),
		ev(3, 4, 5, KindLoad, "a", 2, "loader"),
		ev(4, 6, 6, KindPublish, "a", 2, "load:3"),
		ev(5, 7, 8, KindHit, "a", 1, "load:1"), // v2 已发布，仍返回 v1：确定陈旧
	}
	rep := Audit(events, 0)
	if rep.Verdict != Stale {
		t.Fatalf("verdict = %v (%s), want STALE", rep.Verdict, rep.Reason)
	}
	if len(rep.Culprits) != 1 {
		t.Fatalf("culprits = %v, want exactly 1", rep.Culprits)
	}
	c := rep.Culprits[0]
	if c.HitSeq != 5 || c.GotVersion != 1 || c.WantVersion != 2 || c.SourceSeq != 1 {
		t.Fatalf("culprit = %+v, want hit#5 got 1 want 2 source 1", c)
	}
}

func TestAuditHitAfterEvictIsStale(t *testing.T) {
	events := []Event{
		ev(1, 1, 2, KindLoad, "a", 1, "loader"),
		ev(2, 3, 3, KindPublish, "a", 1, "load:1"),
		ev(3, 4, 4, KindEvict, "a", 0, ""),
		ev(4, 5, 6, KindHit, "a", 1, "load:1"), // 淘汰后无任何合法可见版本
	}
	rep := Audit(events, 0)
	if rep.Verdict != Stale {
		t.Fatalf("verdict = %v, want STALE", rep.Verdict)
	}
	if rep.Culprits[0].WantVersion != 0 {
		t.Fatalf("want version = %d, want 0 (nothing visible)", rep.Culprits[0].WantVersion)
	}
}

func TestAuditMissWhileVisibleIsStale(t *testing.T) {
	events := []Event{
		ev(1, 1, 2, KindLoad, "a", 1, "loader"),
		ev(2, 3, 3, KindPublish, "a", 1, "load:1"),
		ev(3, 4, 5, KindMiss, "a", 0, ""), // 可见版本存在时不应失效
	}
	if rep := Audit(events, 0); rep.Verdict != Stale {
		t.Fatalf("verdict = %v (%s), want STALE", rep.Verdict, rep.Reason)
	}
}

func TestAuditConcurrentOverlapCanBeLinearizable(t *testing.T) {
	// 命中与发布区间重叠：存在把命中排在发布前的合法顺序。
	events := []Event{
		ev(1, 1, 2, KindLoad, "a", 1, "loader"),
		ev(2, 3, 3, KindPublish, "a", 1, "load:1"),
		ev(3, 4, 5, KindLoad, "a", 2, "loader"),
		ev(4, 4, 9, KindHit, "a", 1, "load:1"), // 与 publish#5 重叠
		ev(5, 6, 7, KindPublish, "a", 2, "load:3"),
	}
	if rep := Audit(events, 0); rep.Verdict != Linearizable {
		t.Fatalf("verdict = %v (%s), want LINEARIZABLE", rep.Verdict, rep.Reason)
	}
}

func TestAuditDroppedEventsAreInconclusive(t *testing.T) {
	events := []Event{
		ev(3, 1, 2, KindLoad, "a", 1, "loader"),
		ev(4, 3, 3, KindPublish, "a", 1, "load:3"),
	}
	rep := Audit(events, 2) // 环形覆盖丢片：必须证据不足，不得猜测
	if rep.Verdict != Inconclusive {
		t.Fatalf("verdict = %v, want INCONCLUSIVE", rep.Verdict)
	}
	if rep.Dropped != 2 {
		t.Fatalf("dropped = %d, want 2", rep.Dropped)
	}
}

func TestAuditSequenceGapIsInconclusive(t *testing.T) {
	events := []Event{
		ev(1, 1, 2, KindLoad, "a", 1, "loader"),
		ev(3, 3, 3, KindPublish, "a", 1, "load:1"), // seq 2 缺失
	}
	if rep := Audit(events, 0); rep.Verdict != Inconclusive {
		t.Fatalf("verdict = %v, want INCONCLUSIVE", rep.Verdict)
	}
}

func TestAuditMissingSourceLoadIsInconclusive(t *testing.T) {
	events := []Event{
		ev(1, 1, 2, KindLoad, "a", 2, "loader"),
		ev(2, 3, 3, KindPublish, "a", 2, "load:1"),
		ev(3, 4, 5, KindHit, "a", 1, "load:9"), // 来源加载 #9 不在日志中
	}
	rep := Audit(events, 0)
	if rep.Verdict != Inconclusive {
		t.Fatalf("verdict = %v, want INCONCLUSIVE", rep.Verdict)
	}
}

func TestAuditRingOverflowEndToEnd(t *testing.T) {
	c := NewCache(3, func(key string) (string, uint64, error) {
		return "v1", 1, nil
	}, counterClock(), nil)
	defer c.Close()
	for i := 0; i < 6; i++ {
		c.Get("a")
	}
	rep := Audit(c.Log().Events(), c.Log().Dropped())
	if rep.Verdict != Inconclusive || rep.Dropped == 0 {
		t.Fatalf("verdict = %v dropped = %d, want INCONCLUSIVE with drops", rep.Verdict, rep.Dropped)
	}
}

// TestLinearizableMatchesReference 用独立的全排列参考实现交叉核验主搜索。
func TestLinearizableMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	kinds := []Kind{KindHit, KindMiss, KindLoad, KindEvict, KindPublish}
	keys := []string{"a", "b"}
	for trial := 0; trial < 300; trial++ {
		n := 2 + rng.Intn(6) // 2..7 个事件
		events := make([]Event, n)
		for i := range events {
			start := int64(rng.Intn(12))
			e := Event{
				Seq:     uint64(i) + 1,
				Start:   start,
				End:     start + int64(rng.Intn(3)),
				Kind:    kinds[rng.Intn(len(kinds))],
				Key:     keys[rng.Intn(len(keys))],
				Version: uint64(rng.Intn(3)),
			}
			events[i] = e
		}
		got := linearizable(events)
		want := referenceLinearizable(events)
		if got != want {
			t.Fatalf("trial %d: linearizable=%v reference=%v\nevents=%v", trial, got, want, events)
		}
	}
}

func TestMinimizeKeepsMinimalStaleSubsetWithSourceEvidence(t *testing.T) {
	events := []Event{
		ev(1, 1, 2, KindLoad, "a", 1, "loader"),
		ev(2, 3, 3, KindPublish, "a", 1, "load:1"),
		ev(3, 4, 5, KindLoad, "a", 2, "loader"),
		ev(4, 6, 6, KindPublish, "a", 2, "load:3"),
		ev(5, 7, 8, KindHit, "a", 1, "load:1"),
		ev(6, 9, 10, KindMiss, "b", 0, ""), // 无关事件应被删掉
	}
	if rep := Audit(events, 0); rep.Verdict != Stale {
		t.Fatalf("precondition: verdict = %v, want STALE", rep.Verdict)
	}
	min := Minimize(events)
	if rep := Audit(min, 0); rep.Verdict != Stale {
		t.Fatalf("minimized verdict = %v, want STALE", rep.Verdict)
	}
	if len(min) >= len(events) {
		t.Fatalf("minimized to %d events, want fewer than %d", len(min), len(events))
	}
	// 1-极小性：再删任意一条都不再是 Stale。
	for i := range min {
		cand := append(append([]Event(nil), min[:i]...), min[i+1:]...)
		if rep := Audit(renumber(cand), 0); rep.Verdict == Stale {
			t.Fatalf("removing event %v still STALE: not minimal", min[i])
		}
	}
	// 来源版本证据必须保留：命中及其来源加载都在。
	var hit, srcLoad bool
	srcSeq := uint64(0)
	for _, e := range min {
		if e.Kind == KindHit {
			hit = true
			seq, ok := parseLoadSource(e.Source)
			if !ok {
				t.Fatalf("hit lost source evidence: %v", e)
			}
			srcSeq = seq
		}
	}
	for _, e := range min {
		if e.Kind == KindLoad && e.Seq == srcSeq {
			srcLoad = true
		}
	}
	if !hit || !srcLoad {
		t.Fatalf("minimized subset lost hit or source load: %v", min)
	}
}

func TestMinimizeOnNonStaleIsIdentity(t *testing.T) {
	events := []Event{
		ev(1, 1, 2, KindLoad, "a", 1, "loader"),
		ev(2, 3, 3, KindPublish, "a", 1, "load:1"),
	}
	if got := Minimize(events); len(got) != len(events) {
		t.Fatalf("minimize on linearizable history changed it: %v", got)
	}
}
