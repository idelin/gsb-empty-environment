package dedup

import "testing"

func TestFirstPayloadWins(t *testing.T) {
	d := NewDeduper(10)
	if v, p := d.Observe(Event{Source: "s1", ID: "a", Time: 100, Payload: "first"}); v != New || p != "first" {
		t.Fatalf("first observe = %v,%q", v, p)
	}
	// 同 ID 不同载荷: 保留首次载荷
	if v, p := d.Observe(Event{Source: "s1", ID: "a", Time: 101, Payload: "second"}); v != Duplicate || p != "first" {
		t.Fatalf("dup observe = %v,%q", v, p)
	}
	// 同 ID 跨来源冲突: 仍然首次载荷获胜
	if v, p := d.Observe(Event{Source: "s2", ID: "a", Time: 102, Payload: "other-src"}); v != Duplicate || p != "first" {
		t.Fatalf("cross-source dup = %v,%q", v, p)
	}
}

func TestWatermarkReclaimBoundary(t *testing.T) {
	d := NewDeduper(10)
	d.Observe(Event{Source: "s1", ID: "a", Time: 100, Payload: "x"})

	// 水位 == time+retention 时不得回收(需严格超过)
	d.Advance("s1", 110)
	if !d.Has("a") {
		t.Fatal("reclaimed at watermark == time+retention, want kept")
	}
	if v, _ := d.Observe(Event{Source: "s1", ID: "a", Time: 100, Payload: "y"}); v != Duplicate {
		t.Fatalf("at boundary want Duplicate, got %v", v)
	}

	// 水位严格超过后才回收
	d.Advance("s1", 111)
	if d.Has("a") {
		t.Fatal("not reclaimed at watermark > time+retention")
	}
	if d.Reclaimed() != 1 {
		t.Fatalf("reclaimed = %d, want 1", d.Reclaimed())
	}
}

func TestReplayOfReclaimedIsStaleNotNew(t *testing.T) {
	d := NewDeduper(10)
	d.Observe(Event{Source: "s1", ID: "a", Time: 100, Payload: "orig"})
	d.Advance("s1", 200) // 回收 a
	if d.Has("a") {
		t.Fatal("setup: a should be reclaimed")
	}

	// 重放已回收 ID: 必须判 Stale, 不能当新事件
	if v, p := d.Observe(Event{Source: "s1", ID: "a", Time: 100, Payload: "replay"}); v != Stale || p != "" {
		t.Fatalf("replay = %v,%q, want Stale", v, p)
	}
	// Stale 不得留下记录, 再次重放仍是 Stale
	if d.Has("a") {
		t.Fatal("stale replay must not create a record")
	}
	if v, _ := d.Observe(Event{Source: "s1", ID: "a", Time: 100, Payload: "replay2"}); v != Stale {
		t.Fatalf("second replay = %v, want Stale", v)
	}
}

func TestStaleJudgedByExplicitSourceProgress(t *testing.T) {
	d := NewDeduper(10)
	d.Advance("s1", 500)
	// 从未见过的 ID, 但事件时间 <= 来源进度: 过旧, 不是新事件
	if v, _ := d.Observe(Event{Source: "s1", ID: "ghost", Time: 400, Payload: "p"}); v != Stale {
		t.Fatalf("old event = %v, want Stale", v)
	}
	// 事件时间 > 来源进度: 合法新事件
	if v, _ := d.Observe(Event{Source: "s1", ID: "fresh", Time: 501, Payload: "p"}); v != New {
		t.Fatalf("fresh event = %v, want New", v)
	}
}

func TestProgressIsPerSource(t *testing.T) {
	d := NewDeduper(10)
	d.Advance("s1", 1000)
	// s2 未上报进度, 其旧事件不受 s1 进度影响; 但水位已由 s1 建立,
	// time+retention < watermark 仍判 Stale
	if v, _ := d.Observe(Event{Source: "s2", ID: "x", Time: 500, Payload: "p"}); v != Stale {
		t.Fatalf("s2 event below watermark = %v, want Stale", v)
	}
	// 在水位之上则正常判 New
	if v, _ := d.Observe(Event{Source: "s2", ID: "y", Time: 995, Payload: "p"}); v != New {
		t.Fatalf("s2 event above watermark = %v, want New", v)
	}
}

func TestWatermarkIsMinAcrossSources(t *testing.T) {
	d := NewDeduper(10)
	d.Observe(Event{Source: "s1", ID: "a", Time: 100, Payload: "x"})
	d.Advance("s2", 105) // 滞后来源拖住水位
	d.Advance("s1", 500)
	if wm, ok := d.Watermark(); !ok || wm != 105 {
		t.Fatalf("watermark = %d,%v, want 105,true", wm, ok)
	}
	if !d.Has("a") {
		t.Fatal("lagging source must hold back reclamation")
	}
	d.Advance("s2", 500)
	if wm, _ := d.Watermark(); wm != 500 {
		t.Fatalf("watermark = %d, want 500", wm)
	}
	if d.Has("a") {
		t.Fatal("all sources advanced, a should be reclaimed")
	}
}

func TestAdvanceIsMonotonic(t *testing.T) {
	d := NewDeduper(10)
	d.Advance("s1", 300)
	d.Advance("s1", 200) // 倒退被忽略
	if p, _ := d.SourceProgress("s1"); p != 300 {
		t.Fatalf("progress = %d, want 300", p)
	}
	d.Advance("s1", 300) // 相等也忽略
	if p, _ := d.SourceProgress("s1"); p != 300 {
		t.Fatalf("progress = %d, want 300", p)
	}
}

func TestNoReclaimWithoutAnyProgress(t *testing.T) {
	d := NewDeduper(0)
	d.Observe(Event{Source: "s1", ID: "a", Time: 1, Payload: "x"})
	if _, ok := d.Watermark(); ok {
		t.Fatal("watermark must be invalid before any Advance")
	}
	// 无显式进度时, 任何事件都不会被判 Stale / 被回收
	if v, _ := d.Observe(Event{Source: "s2", ID: "b", Time: 0, Payload: "y"}); v != New {
		t.Fatalf("no-progress event = %v, want New", v)
	}
}

func TestDuplicateDoesNotExtendRetention(t *testing.T) {
	d := NewDeduper(10)
	d.Observe(Event{Source: "s1", ID: "a", Time: 100, Payload: "first"})
	// 同 ID 冲突事件携带更晚的时间, 不得刷新回收基准
	d.Observe(Event{Source: "s1", ID: "a", Time: 500, Payload: "second"})
	d.Advance("s1", 111)
	if d.Has("a") {
		t.Fatal("duplicate with later time must not extend retention")
	}
}

func TestNewSourceDoesNotRegressWatermark(t *testing.T) {
	d := NewDeduper(10)
	d.Advance("s1", 500)
	d.Advance("s2", 3) // 新来源低进度首次上报
	if wm, _ := d.Watermark(); wm != 500 {
		t.Fatalf("watermark regressed to %d, want 500", wm)
	}
	// 新来源随后的推进正常参与水位计算(需旧来源也推进)
	d.Advance("s1", 700)
	d.Advance("s2", 600)
	if wm, _ := d.Watermark(); wm != 600 {
		t.Fatalf("watermark = %d, want 600", wm)
	}
}

func TestReplayFromNewSourceStillStale(t *testing.T) {
	d := NewDeduper(10)
	d.Observe(Event{Source: "s1", ID: "a", Time: 100, Payload: "orig"})
	d.Advance("s1", 500) // 回收 a
	// 重放来自一个刚加入、进度很低的来源: 仍须凭水位判旧
	d.Advance("s2", 3)
	if v, _ := d.Observe(Event{Source: "s2", ID: "a", Time: 100, Payload: "replay"}); v != Stale {
		t.Fatalf("replay via new source = %v, want Stale", v)
	}
}
