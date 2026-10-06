package railsim

import "testing"

func mustReserve(t *testing.T, tb *Table, train TrainID, segs []SegmentID, ivs []Interval) {
	t.Helper()
	if err := tb.Reserve(train, segs, ivs); err != nil {
		t.Fatalf("Reserve(%s) failed: %v", train, err)
	}
}

// 同刻释放后进入: half-open intervals allow entering at the release tick.
func TestHalfOpenSameTickRelease(t *testing.T) {
	tb := NewTable()
	mustReserve(t, tb, "T1", []SegmentID{"A", "B"}, []Interval{{0, 5}, {5, 9}})
	// T2 enters segment A at exactly the tick T1 releases it.
	mustReserve(t, tb, "T2", []SegmentID{"A"}, []Interval{{5, 8}})
	if err := tb.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := Replay(tb.All()); err != nil {
		t.Fatalf("Replay: %v", err)
	}
}

// 同向也不能重叠: any overlap on a segment is rejected.
func TestOverlapRejected(t *testing.T) {
	overlapping := []Interval{{9, 12}, {0, 1}, {3, 4}, {2, 11}, {0, 10}}
	for _, iv := range overlapping {
		tb := NewTable()
		mustReserve(t, tb, "T1", []SegmentID{"A"}, []Interval{{0, 10}})
		err := tb.Reserve("T2", []SegmentID{"A"}, []Interval{iv})
		if err == nil {
			t.Fatalf("expected conflict for %v against [0,10)", iv)
		}
		if _, isConflict := err.(*ConflictError); !isConflict {
			t.Fatalf("expected *ConflictError, got %T", err)
		}
		if got := len(tb.All()["A"]); got != 1 {
			t.Fatalf("failed reservation mutated table: %d occupancies", got)
		}
	}
}

// 申请一段路径需全量预约成功才确认，失败不留部分预约。
func TestAtomicPathReservation(t *testing.T) {
	tb := NewTable()
	mustReserve(t, tb, "T1", []SegmentID{"B"}, []Interval{{2, 4}})
	err := tb.Reserve("T2",
		[]SegmentID{"A", "B", "C"},
		[]Interval{{0, 2}, {2, 5}, {5, 7}})
	if err == nil {
		t.Fatal("expected conflict on segment B")
	}
	all := tb.All()
	if len(all["A"]) != 0 || len(all["C"]) != 0 {
		t.Fatalf("partial reservation leaked: %v", all)
	}
	if len(all["B"]) != 1 || all["B"][0].Train != "T1" {
		t.Fatalf("existing reservation disturbed: %v", all["B"])
	}
	if err := tb.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// 取消: released space becomes reservable, including the exact ticks.
func TestCancel(t *testing.T) {
	tb := NewTable()
	mustReserve(t, tb, "T1",
		[]SegmentID{"A", "B", "C"},
		[]Interval{{0, 2}, {2, 5}, {5, 7}})
	if n := tb.Cancel("T1"); n != 3 {
		t.Fatalf("Cancel removed %d, want 3", n)
	}
	if err := tb.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// The exact intervals freed by the cancel are reservable again.
	mustReserve(t, tb, "T2",
		[]SegmentID{"A", "B", "C"},
		[]Interval{{0, 2}, {2, 5}, {5, 7}})
	if err := Replay(tb.All()); err != nil {
		t.Fatalf("Replay: %v", err)
	}
}

// A route may revisit a segment, but its own intervals must not overlap.
func TestSelfOverlapRejected(t *testing.T) {
	tb := NewTable()
	err := tb.Reserve("T1",
		[]SegmentID{"A", "B", "A"},
		[]Interval{{0, 5}, {5, 6}, {4, 8}})
	if err == nil {
		t.Fatal("expected self-overlap conflict")
	}
	if len(tb.All()) != 0 {
		t.Fatalf("table not empty after failed reserve: %v", tb.All())
	}
	// Non-overlapping revisit is fine.
	mustReserve(t, tb, "T1",
		[]SegmentID{"A", "B", "A"},
		[]Interval{{0, 5}, {5, 6}, {6, 8}})
	if err := tb.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// 所有合法起点组合: exhaustively drive two trains over one shared segment
// from every start combination and in both booking orders; the final table
// must always scan and replay clean, and the second train must never
// overlap the first.
func TestExhaustiveStartsSingleSegment(t *testing.T) {
	orders := [][2]TrainID{{"T1", "T2"}, {"T2", "T1"}}
	dur := map[TrainID]int{"T1": 2, "T2": 3}
	EachStartCombo(2, 0, 4, func(starts []int) bool {
		ready := map[TrainID]int{"T1": starts[0], "T2": starts[1]}
		for _, order := range orders {
			tb := NewTable()
			got := make(map[TrainID]Interval)
			for _, id := range order {
				ivs, err := tb.SchedulePath(id, []SegmentID{"S"}, []int{dur[id]}, ready[id], 20)
				if err != nil {
					t.Fatalf("starts=%v order=%v: %v", starts, order, err)
				}
				got[id] = ivs[0]
			}
			if err := tb.Verify(); err != nil {
				t.Fatalf("starts=%v order=%v: %v", starts, order, err)
			}
			if err := Replay(tb.All()); err != nil {
				t.Fatalf("starts=%v order=%v: %v", starts, order, err)
			}
			if got["T1"].Overlaps(got["T2"]) {
				t.Fatalf("starts=%v order=%v: overlap %v vs %v", starts, order, got["T1"], got["T2"])
			}
			// The later-booked train enters no earlier than its ready tick.
			for _, id := range order {
				if got[id].Start < ready[id] {
					t.Fatalf("starts=%v: %s entered at %d before ready %d",
						starts, id, got[id].Start, ready[id])
				}
			}
		}
		return true
	})
}
