package railsim

import "testing"

// 交会: two trains cross a shared single-track segment in opposite
// directions; the second must wait at the station and enters the shared
// segment at the exact tick the first releases it.
func TestMeetOnSingleTrack(t *testing.T) {
	tb := NewTable()
	ivs1, err := tb.SchedulePath("T1", []SegmentID{"A", "X", "B"}, []int{2, 3, 2}, 0, 50)
	if err != nil {
		t.Fatalf("T1: %v", err)
	}
	ivs2, err := tb.SchedulePath("T2", []SegmentID{"C", "X", "D"}, []int{2, 3, 2}, 0, 50)
	if err != nil {
		t.Fatalf("T2: %v", err)
	}
	want1 := []Interval{{0, 2}, {2, 5}, {5, 7}}
	want2 := []Interval{{0, 2}, {5, 8}, {8, 10}} // waits at station, enters X at release tick
	for i := range want1 {
		if ivs1[i] != want1[i] {
			t.Fatalf("T1[%d] = %v, want %v", i, ivs1[i], want1[i])
		}
	}
	for i := range want2 {
		if ivs2[i] != want2[i] {
			t.Fatalf("T2[%d] = %v, want %v", i, ivs2[i], want2[i])
		}
	}
	if ivs2[1].Start != ivs1[1].End {
		t.Fatalf("T2 should enter X at T1's release tick: %v vs %v", ivs2[1], ivs1[1])
	}
	if err := tb.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := Replay(tb.All()); err != nil {
		t.Fatalf("Replay: %v", err)
	}
}

// 站点允许等待: a train blocked on its next segment holds at the station;
// the gap between consecutive occupancies is the waiting time.
func TestStationWaiting(t *testing.T) {
	tb := NewTable()
	mustReserve(t, tb, "T0", []SegmentID{"X"}, []Interval{{0, 6}})
	ivs, err := tb.SchedulePath("T2", []SegmentID{"C", "X"}, []int{2, 3}, 0, 50)
	if err != nil {
		t.Fatalf("T2: %v", err)
	}
	if ivs[0] != (Interval{0, 2}) {
		t.Fatalf("T2 first leg = %v, want [0,2)", ivs[0])
	}
	if ivs[1] != (Interval{6, 9}) {
		t.Fatalf("T2 should wait at station until 6, got %v", ivs[1])
	}
	if err := Replay(tb.All()); err != nil {
		t.Fatalf("Replay: %v", err)
	}
}

// A path that cannot fit before the horizon fails atomically.
func TestSchedulePathHorizonFailure(t *testing.T) {
	tb := NewTable()
	mustReserve(t, tb, "T0", []SegmentID{"X"}, []Interval{{0, 8}})
	if _, err := tb.SchedulePath("T1", []SegmentID{"A", "X"}, []int{2, 3}, 0, 10); err == nil {
		t.Fatal("expected horizon failure")
	}
	if len(tb.All()["A"]) != 0 {
		t.Fatalf("partial reservation leaked: %v", tb.All()["A"])
	}
}
