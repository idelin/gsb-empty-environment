package dedup

import "testing"

func TestFirstPayloadWins(t *testing.T) {
	d := New(10)
	r1 := d.Ingest(Event{Source: "s1", ID: "a", EventTime: 5, Payload: "first"})
	if r1.Outcome != Accepted || r1.Payload != "first" {
		t.Fatalf("first ingest = %+v, want Accepted/first", r1)
	}
	r2 := d.Ingest(Event{Source: "s1", ID: "a", EventTime: 5, Payload: "first"})
	if r2.Outcome != Duplicate || r2.Payload != "first" || r2.Conflict {
		t.Fatalf("identical replay = %+v, want Duplicate/first/no conflict", r2)
	}
	r3 := d.Ingest(Event{Source: "s1", ID: "a", EventTime: 6, Payload: "second"})
	if r3.Outcome != Duplicate || r3.Payload != "first" || !r3.Conflict {
		t.Fatalf("conflicting replay = %+v, want Duplicate/first/conflict", r3)
	}
	if got, _, ok := d.Lookup("a"); !ok || got != "first" {
		t.Fatalf("stored payload = %q, want first", got)
	}
}

func TestConflictAcrossSources(t *testing.T) {
	d := New(10)
	d.Ingest(Event{Source: "s1", ID: "a", EventTime: 5, Payload: "p1"})
	r := d.Ingest(Event{Source: "s2", ID: "a", EventTime: 7, Payload: "p2"})
	if r.Outcome != Duplicate || r.Payload != "p1" || !r.Conflict {
		t.Fatalf("cross-source same id = %+v, want Duplicate/p1/conflict", r)
	}
}

func TestReclaimOnlyAfterWatermarkExceedsEventTimePlusRetention(t *testing.T) {
	d := New(5)
	d.Ingest(Event{Source: "s1", ID: "a", EventTime: 10, Payload: "p"})

	d.ReportProgress("s1", 15) // 水位 15,不严格大于 10+5,保留
	if d.Len() != 1 {
		t.Fatalf("at watermark 15 entry must be retained, len = %d", d.Len())
	}
	d.ReportProgress("s1", 16) // 水位 16 > 15,回收
	if d.Len() != 0 {
		t.Fatalf("at watermark 16 entry must be reclaimed, len = %d", d.Len())
	}
}

func TestWatermarkIsMinAcrossSources(t *testing.T) {
	d := New(5)
	d.Ingest(Event{Source: "s1", ID: "a", EventTime: 10, Payload: "p"})

	d.ReportProgress("s1", 14) // 水位 14 <= 10+5,保留
	if wm, _ := d.Watermark(); wm != 14 {
		t.Fatalf("watermark = %d, want 14", wm)
	}
	d.ReportProgress("s2", 12) // min(14,12)=12,但水位只增不减,保持 14
	if wm, _ := d.Watermark(); wm != 14 {
		t.Fatalf("watermark = %d, want 14 (monotonic)", wm)
	}
	d.ReportProgress("s2", 18) // 水位 min(14,18)=14,仍保留
	if wm, _ := d.Watermark(); wm != 14 {
		t.Fatalf("watermark = %d, want 14", wm)
	}
	if d.Len() != 1 {
		t.Fatalf("len = %d, want 1 (14 <= 10+5)", d.Len())
	}
	d.ReportProgress("s1", 16) // 水位 min(16,18)=16 > 15,回收
	if wm, _ := d.Watermark(); wm != 16 {
		t.Fatalf("watermark = %d, want 16", wm)
	}
	if d.Len() != 0 {
		t.Fatalf("len = %d, want 0 after watermark exceeds 10+5", d.Len())
	}
	d.ReportProgress("s3", 5) // 新来源进度低,但水位只增不减
	if wm, _ := d.Watermark(); wm != 16 {
		t.Fatalf("watermark = %d, want 16 (monotonic)", wm)
	}
}

func TestProgressRegressionIgnored(t *testing.T) {
	d := New(5)
	d.ReportProgress("s1", 50)
	d.ReportProgress("s1", 30) // 回退,忽略
	if wm, _ := d.Watermark(); wm != 50 {
		t.Fatalf("watermark = %d, want 50 after regressed report", wm)
	}
	d.ReportProgress("s1", 50) // 未推进,忽略
	d.ReportProgress("s1", 51)
	if wm, _ := d.Watermark(); wm != 51 {
		t.Fatalf("watermark = %d, want 51", wm)
	}
}

func TestReplayedReclaimedIDIsStaleNotNew(t *testing.T) {
	d := New(5)
	d.Ingest(Event{Source: "s1", ID: "a", EventTime: 10, Payload: "orig"})
	d.ReportProgress("s1", 20) // 水位 20 > 10+5,a 被回收
	if d.Len() != 0 {
		t.Fatalf("entry should be reclaimed")
	}
	r := d.Ingest(Event{Source: "s1", ID: "a", EventTime: 10, Payload: "orig"})
	if r.Outcome != Stale {
		t.Fatalf("replayed reclaimed id = %v, want Stale", r.Outcome)
	}
	if d.Len() != 0 {
		t.Fatalf("stale replay must not re-create an entry, len = %d", d.Len())
	}
}

func TestLateUnknownIDIsStale(t *testing.T) {
	d := New(5)
	d.ReportProgress("s1", 20)
	r := d.Ingest(Event{Source: "s1", ID: "never-seen", EventTime: 18, Payload: "p"})
	if r.Outcome != Stale {
		t.Fatalf("late unknown event = %v, want Stale", r.Outcome)
	}
	if d.Len() != 0 {
		t.Fatalf("stale event must not be stored")
	}
}

func TestUnknownSourceWithoutProgressAccepts(t *testing.T) {
	d := New(5)
	d.ReportProgress("s1", 100)
	r := d.Ingest(Event{Source: "s2", ID: "a", EventTime: 1, Payload: "p"})
	if r.Outcome != Accepted {
		t.Fatalf("event from source without progress = %v, want Accepted", r.Outcome)
	}
}

func TestStaleJudgedByOwnSourceProgress(t *testing.T) {
	d := New(5)
	d.ReportProgress("s1", 100)
	d.ReportProgress("s2", 3)
	// s2 的进度只有 3,事件时间 50 对 s2 而言是新事件,即使水位已是 3。
	r := d.Ingest(Event{Source: "s2", ID: "a", EventTime: 50, Payload: "p"})
	if r.Outcome != Accepted {
		t.Fatalf("event ahead of its source progress = %v, want Accepted", r.Outcome)
	}
	// s1 进度 100,事件时间 90 对 s1 过旧。
	r2 := d.Ingest(Event{Source: "s1", ID: "b", EventTime: 90, Payload: "p"})
	if r2.Outcome != Stale {
		t.Fatalf("event behind its source progress = %v, want Stale", r2.Outcome)
	}
}
