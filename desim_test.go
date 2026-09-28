package desim

import (
	"reflect"
	"strings"
	"testing"
)

// Same-tick events fire ordered by category (packet < link < timer), then
// by scheduling sequence number within a category.
func TestSameTickOrdering(t *testing.T) {
	sch := NewScheduler()
	sch.ScheduleAt(10, CatTimer, EvTimer, Payload{TimerID: 1})
	sch.ScheduleAt(10, CatLink, EvLinkFree, Payload{LinkID: 2})
	sch.ScheduleAt(10, CatPacket, EvPacketArrive, Payload{NodeID: 3})
	sch.ScheduleAt(10, CatTimer, EvTimer, Payload{TimerID: 4})
	sch.ScheduleAt(3, CatTimer, EvTimer, Payload{TimerID: 5})

	var got []Event
	for {
		ev, ok := sch.Next()
		if !ok {
			break
		}
		got = append(got, ev)
	}
	if len(got) != 5 {
		t.Fatalf("want 5 events, got %d", len(got))
	}
	if got[0].Tick != 3 || got[0].Data.TimerID != 5 {
		t.Fatalf("tick 3 timer first, got %+v", got[0])
	}
	wantKinds := []EventKind{EvPacketArrive, EvLinkFree, EvTimer, EvTimer}
	for i, k := range wantKinds {
		if got[i+1].Tick != 10 || got[i+1].Kind != k {
			t.Fatalf("position %d: want tick 10 kind %d, got %+v", i+1, k, got[i+1])
		}
	}
	if got[3].Data.TimerID != 1 || got[4].Data.TimerID != 4 {
		t.Fatalf("same-category order must follow seq, got %d then %d",
			got[3].Data.TimerID, got[4].Data.TimerID)
	}
}

// A cancelled event never fires, and a stale (old-generation) handle cannot
// cancel a new event that recycles the same slot.
func TestCancelGenerationalHandle(t *testing.T) {
	sch := NewScheduler()
	h1 := sch.ScheduleAt(5, CatTimer, EvTimer, Payload{TimerID: 1})
	if !sch.Cancel(h1) {
		t.Fatal("cancel of live event failed")
	}
	if sch.Cancel(h1) {
		t.Fatal("double cancel must fail")
	}
	if sch.Live() != 0 {
		t.Fatalf("live=%d after cancel", sch.Live())
	}
	if _, ok := sch.Next(); ok {
		t.Fatal("cancelled event fired")
	}

	// Schedule again: the slot freed above is reused with a bumped
	// generation, so the old handle must not affect the new event.
	h2 := sch.ScheduleAt(6, CatTimer, EvTimer, Payload{TimerID: 2})
	if h2.Slot != h1.Slot {
		t.Fatalf("expected slot reuse, h1=%+v h2=%+v", h1, h2)
	}
	if sch.Cancel(h1) {
		t.Fatal("stale handle cancelled a recycled slot")
	}
	ev, ok := sch.Next()
	if !ok || ev.Data.TimerID != 2 || ev.Tick != 6 {
		t.Fatalf("recycled event lost: ok=%v ev=%+v", ok, ev)
	}
	// After firing, its own handle is stale too.
	if sch.Cancel(h2) {
		t.Fatal("cancel of already-fired event must fail")
	}
}

// Events scheduled while dispatching an event at tick T run after all
// same-category events already pending at tick T (defined by seq order).
func TestSameTickInsertionDuringDispatch(t *testing.T) {
	sch := NewScheduler()
	sch.ScheduleAt(5, CatTimer, EvTimer, Payload{TimerID: 1})
	sch.ScheduleAt(5, CatTimer, EvTimer, Payload{TimerID: 2})

	var order []int
	for {
		ev, ok := sch.Next()
		if !ok {
			break
		}
		order = append(order, ev.Data.TimerID)
		if ev.Data.TimerID == 1 {
			// New same-tick event: must land behind timer 2.
			sch.ScheduleAt(5, CatTimer, EvTimer, Payload{TimerID: 3})
		}
	}
	if !reflect.DeepEqual(order, []int{1, 2, 3}) {
		t.Fatalf("order=%v, want [1 2 3]", order)
	}
}

func buildSim(seed uint64) *Sim {
	s := NewSim(seed)
	a := s.AddNode("A")
	b := s.AddNode("B")
	c := s.AddNode("C")
	s.AddLink(a, b, 2, 5, 100) // 2 B/tick, 5 tick latency
	s.AddLink(b, c, 1, 10, 50) // 1 B/tick, 10 tick latency
	s.AddGenerator(a, c, 10, 40, 7, 3)
	s.AddGenerator(b, c, 5, 15, 11, 0)
	return s
}

// Restoring a checkpoint and continuing must produce a trace identical to
// an uninterrupted run.
func TestCheckpointRestoreConsistency(t *testing.T) {
	const split, end = 300, 800

	full := buildSim(42)
	full.RunUntil(end)

	partial := buildSim(42)
	partial.RunUntil(split)
	cp, err := partial.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	partial.RunUntil(end)

	restored, err := Restore(cp)
	if err != nil {
		t.Fatal(err)
	}
	restored.RunUntil(end)

	if !reflect.DeepEqual(partial.Trace, full.Trace) {
		t.Fatal("checkpoint+continue diverged from continuous run")
	}
	if !reflect.DeepEqual(restored.Trace, full.Trace) {
		t.Fatal("restored run diverged from continuous run")
	}
	if restored.Delivered != full.Delivered || restored.TotalDelay != full.TotalDelay {
		t.Fatalf("stats diverged: restored=(%d,%d) full=(%d,%d)",
			restored.Delivered, restored.TotalDelay, full.Delivered, full.TotalDelay)
	}
	if full.Delivered == 0 {
		t.Fatal("sanity: no packets delivered")
	}
}

// A cancelled generator emits no further packets, and its pending timer
// event never fires.
func TestCancelGeneratorStopsTraffic(t *testing.T) {
	s := buildSim(7)
	s.RunUntil(200)
	s.CancelGenerator(1)
	s.RunUntil(600)

	for _, line := range s.Trace {
		if strings.Contains(line, "emit") && strings.Contains(line, "gen=1") {
			var tick int
			if _, err := sscanf(line, &tick); err == nil && tick > 200 {
				t.Fatalf("gen 1 emitted after cancel: %q", line)
			}
		}
	}
	if s.Gens[1].Active {
		t.Fatal("generator still active")
	}
	// Generator 0 must be unaffected.
	if s.Gens[0].Sent == 0 {
		t.Fatal("gen 0 stopped unexpectedly")
	}
}

// sscanf extracts the leading integer of a trace line.
func sscanf(line string, out *int) (int, error) {
	n := 0
	for _, r := range strings.TrimSpace(line) {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	*out = n
	return 1, nil
}

// Link serialization: a 10-byte packet on a 2 B/tick link occupies the
// transmitter for 5 ticks; a queued packet starts only after that.
func TestLinkSerializationAndQueue(t *testing.T) {
	s := NewSim(1)
	a := s.AddNode("A")
	b := s.AddNode("B")
	s.AddLink(a, b, 2, 5, 1000)

	s.Inject(a, b, 10) // ser=5, busy [0,5), arrives at 5+5=10
	s.Inject(a, b, 8)  // queued, ser=4, busy [5,9), arrives at 9+5=14
	s.RunUntil(100)

	var deliverTicks []string
	for _, line := range s.Trace {
		if strings.Contains(line, "deliver") {
			deliverTicks = append(deliverTicks, strings.TrimSpace(line))
		}
	}
	if len(deliverTicks) != 2 {
		t.Fatalf("want 2 deliveries, got %v", deliverTicks)
	}
	if !strings.HasPrefix(deliverTicks[0], "10 ") || !strings.HasPrefix(deliverTicks[1], "14 ") {
		t.Fatalf("arrival ticks wrong: %v", deliverTicks)
	}
	if s.Delivered != 2 {
		t.Fatalf("delivered=%d", s.Delivered)
	}
}

// Packets exceeding the queue's byte capacity are dropped, not lost silently.
func TestQueueDrop(t *testing.T) {
	s := NewSim(1)
	a := s.AddNode("A")
	b := s.AddNode("B")
	s.AddLink(a, b, 1, 1, 12) // 12-byte queue

	s.Inject(a, b, 10) // transmits immediately (ser=10)
	s.Inject(a, b, 10) // queued (10 <= 12)
	s.Inject(a, b, 10) // dropped (20+10 > 12)
	s.RunUntil(100)

	if s.Links[0].Q.Drops != 1 {
		t.Fatalf("drops=%d, want 1", s.Links[0].Q.Drops)
	}
	if s.Delivered != 2 {
		t.Fatalf("delivered=%d, want 2", s.Delivered)
	}
}

// Scheduling in the past is a programming error and must panic.
func TestScheduleInPastPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	sch := NewScheduler()
	sch.ScheduleAt(10, CatTimer, EvTimer, Payload{})
	sch.Next()
	sch.ScheduleAt(9, CatTimer, EvTimer, Payload{})
}
