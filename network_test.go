package desim

import "testing"

func testWorld() *World {
	return NewWorld(
		&Source{Interval: 3, Size: 100, Remaining: 12},
		&Queue{Cap: 4},
		&Link{Rate: 10, Delay: 2},
	)
}

func TestFIFOOrderAndDrops(t *testing.T) {
	w := testWorld()
	w.Sched.RunUntil(1000)

	delivered := uint64(len(w.Sn.Log))
	if delivered+w.Q.Dropped != 12 {
		t.Fatalf("delivered %d + dropped %d != 12", delivered, w.Q.Dropped)
	}
	if w.Q.Dropped == 0 {
		t.Fatal("expected drops with Cap=4 and a slow link")
	}
	// FIFO: delivered IDs strictly increasing.
	for i := 1; i < len(w.Sn.Log); i++ {
		if w.Sn.Log[i].ID <= w.Sn.Log[i-1].ID {
			t.Fatalf("delivery order broken at %d: %v", i, w.Sn.Log)
		}
	}
	// Every delivery respects transmission + propagation time.
	for _, d := range w.Sn.Log {
		born := 3 * (d.ID - 1)
		if d.Tick < born+10+2 {
			t.Fatalf("packet %d delivered at %d, before its earliest possible %d",
				d.ID, d.Tick, born+12)
		}
	}
}

func TestNoDropsWhenLinkKeepsUp(t *testing.T) {
	w := NewWorld(
		&Source{Interval: 20, Size: 100, Remaining: 5},
		&Queue{Cap: 1},
		&Link{Rate: 10, Delay: 2},
	)
	w.Sched.RunUntil(1000)

	if w.Q.Dropped != 0 {
		t.Fatalf("dropped = %d, want 0", w.Q.Dropped)
	}
	if len(w.Sn.Log) != 5 {
		t.Fatalf("delivered = %d, want 5", len(w.Sn.Log))
	}
	// With an idle link each packet arrives Born + tx(10) + delay(2).
	for i, d := range w.Sn.Log {
		want := uint64(20*i) + 12
		if d.Tick != want {
			t.Fatalf("packet %d at tick %d, want %d", d.ID, d.Tick, want)
		}
	}
}
