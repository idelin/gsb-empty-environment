package join

import "testing"

func TestBasicJoinWithinWindow(t *testing.T) {
	j := New(10, 5)
	if got := j.Add(Left, Event{Key: "a", TS: 100}); len(got) != 0 {
		t.Fatalf("no right event yet, want 0 pairs, got %d", len(got))
	}
	got := j.Add(Right, Event{Key: "a", TS: 108}) // |108-100|=8 <= 10
	if len(got) != 1 || got[0].Delta {
		t.Fatalf("want 1 on-time pair, got %+v", got)
	}
	if got[0].Left.TS != 100 || got[0].Right.TS != 108 {
		t.Fatalf("wrong pair contents: %+v", got[0])
	}
}

func TestNoJoinOutsideWindow(t *testing.T) {
	j := New(10, 5)
	j.Add(Left, Event{Key: "a", TS: 100})
	if got := j.Add(Right, Event{Key: "a", TS: 111}); len(got) != 0 {
		t.Fatalf("diff 11 > window 10, want 0 pairs, got %+v", got)
	}
	if got := j.Add(Right, Event{Key: "b", TS: 100}); len(got) != 0 {
		t.Fatalf("different key must not join, got %+v", got)
	}
}

func TestBoundaryJoin(t *testing.T) {
	j := New(10, 5)
	j.Add(Left, Event{Key: "a", TS: 100})
	if got := j.Add(Right, Event{Key: "a", TS: 110}); len(got) != 1 {
		t.Fatalf("diff == window must join, got %+v", got)
	}
	if got := j.Add(Right, Event{Key: "a", TS: 90}); len(got) != 1 {
		t.Fatalf("negative diff == window must join, got %+v", got)
	}
}

func TestLateWithinLatenessEmitsDelta(t *testing.T) {
	j := New(10, 5)
	j.Add(Left, Event{Key: "a", TS: 100})
	j.Add(Right, Event{Key: "a", TS: 100})
	j.AdvanceWatermark(Left, 100)
	j.AdvanceWatermark(Right, 100)

	// 迟到右事件 ts=97：<= 右水位 100，但 > 100-5=95，仍在更正期限内。
	got := j.Add(Right, Event{Key: "a", TS: 97})
	if len(got) != 1 || !got[0].Delta {
		t.Fatalf("want 1 delta pair for in-lateness late event, got %+v", got)
	}
	if len(j.LateReports()) != 0 {
		t.Fatalf("in-lateness event must not be reported, got %+v", j.LateReports())
	}
}

func TestLateBeyondLatenessReported(t *testing.T) {
	j := New(10, 5)
	j.Add(Left, Event{Key: "a", TS: 100})
	j.AdvanceWatermark(Left, 100)
	j.AdvanceWatermark(Right, 100)

	// 迟到右事件 ts=90：<= 100-5=95，超出更正期限。
	got := j.Add(Right, Event{Key: "a", TS: 90})
	if len(got) != 0 {
		t.Fatalf("expired late event must not join, got %+v", got)
	}
	reps := j.LateReports()
	if len(reps) != 1 || reps[0].Side != Right || reps[0].Event.TS != 90 || reps[0].Watermark != 100 {
		t.Fatalf("want 1 late report for the dropped event, got %+v", reps)
	}
}

func TestWatermarkMonotonic(t *testing.T) {
	j := New(10, 5)
	if !j.AdvanceWatermark(Left, 100) {
		t.Fatal("first advance must succeed")
	}
	if j.AdvanceWatermark(Left, 100) || j.AdvanceWatermark(Left, 99) {
		t.Fatal("non-increasing advance must be rejected")
	}
	if !j.AdvanceWatermark(Left, 101) {
		t.Fatal("increasing advance must succeed")
	}
}

func TestCleanupAfterBothWatermarksAdvance(t *testing.T) {
	j := New(10, 5)
	j.Add(Left, Event{Key: "a", TS: 100})
	j.Add(Right, Event{Key: "a", TS: 100})
	j.AdvanceWatermark(Left, 1000)
	j.AdvanceWatermark(Right, 1000)
	if l, r := j.Buffered("a"); l != 0 || r != 0 {
		t.Fatalf("state must be cleaned after both watermarks pass, got left=%d right=%d", l, r)
	}
}

// 核心验证：右侧水位停滞时，左侧水位再高也不能删除可匹配的左状态。
func TestStalledRightWatermarkKeepsLeftState(t *testing.T) {
	j := New(10, 5)
	j.Add(Left, Event{Key: "a", TS: 100})

	// 左侧水位大幅推进，右侧水位停滞不动。
	j.AdvanceWatermark(Left, 100000)
	if l, _ := j.Buffered("a"); l != 1 {
		t.Fatalf("left state must survive while right watermark stalls, got left=%d", l)
	}

	// 右侧水位推进到 95：左事件 ts=100，100+10=110 >= 95-5=90，仍需保留。
	j.AdvanceWatermark(Right, 95)
	if l, _ := j.Buffered("a"); l != 1 {
		t.Fatalf("left state still matchable (right horizon 90 <= 110), got left=%d", l)
	}

	// 右侧迟到的在更正期限内的右事件仍能与旧左事件匹配。
	got := j.Add(Right, Event{Key: "a", TS: 94})
	if len(got) != 1 || !got[0].Delta {
		t.Fatalf("late in-lateness right event must still match retained left state, got %+v", got)
	}

	// 右侧水位推进到 116：100+10=110 < 116-5=111，左事件才可被清理。
	j.AdvanceWatermark(Right, 116)
	if l, _ := j.Buffered("a"); l != 0 {
		t.Fatalf("left state must be cleaned once right watermark passes ts+window+lateness, got left=%d", l)
	}
}

// 对称验证：左侧水位停滞时，右侧水位再高也不能删除可匹配的右状态。
func TestStalledLeftWatermarkKeepsRightState(t *testing.T) {
	j := New(10, 5)
	j.Add(Right, Event{Key: "a", TS: 100})

	j.AdvanceWatermark(Right, 100000)
	if _, r := j.Buffered("a"); r != 1 {
		t.Fatalf("right state must survive while left watermark stalls, got right=%d", r)
	}

	j.AdvanceWatermark(Left, 95)
	got := j.Add(Left, Event{Key: "a", TS: 94})
	if len(got) != 1 || !got[0].Delta {
		t.Fatalf("late in-lateness left event must still match retained right state, got %+v", got)
	}

	j.AdvanceWatermark(Left, 116)
	if _, r := j.Buffered("a"); r != 0 {
		t.Fatalf("right state must be cleaned once left watermark passes ts+window+lateness, got right=%d", r)
	}
}

func TestDeltaPairIncludesBothEvents(t *testing.T) {
	j := New(10, 5)
	j.Add(Left, Event{Key: "a", TS: 55, Value: "L"})
	j.AdvanceWatermark(Right, 50)
	got := j.Add(Right, Event{Key: "a", TS: 49, Value: "R"})
	if len(got) != 1 || !got[0].Delta {
		t.Fatalf("want delta pair, got %+v", got)
	}
	if got[0].Left.Value != "L" || got[0].Right.Value != "R" {
		t.Fatalf("pair must carry both events, got %+v", got[0])
	}
}

func TestMultipleMatchesPerEvent(t *testing.T) {
	j := New(10, 5)
	j.Add(Left, Event{Key: "a", TS: 100})
	j.Add(Left, Event{Key: "a", TS: 105})
	j.Add(Left, Event{Key: "b", TS: 100})
	got := j.Add(Right, Event{Key: "a", TS: 103})
	if len(got) != 2 {
		t.Fatalf("right event must match both left events on key a, got %+v", got)
	}
}
