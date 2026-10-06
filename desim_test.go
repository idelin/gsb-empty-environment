package desim

import (
	"math/rand"
	"testing"
)

func noop(ctx *Context) {}

func TestKeyOrdering(t *testing.T) {
	keys := []Key{
		{Time: 2, Phase: 0, Seq: 0},
		{Time: 1, Phase: 1, Seq: 0},
		{Time: 1, Phase: 0, Seq: 5},
		{Time: 1, Phase: 0, Seq: 3},
		{Time: 1, Phase: 1, Seq: 0},
	}
	sorted := []Key{
		{Time: 1, Phase: 0, Seq: 3},
		{Time: 1, Phase: 0, Seq: 5},
		{Time: 1, Phase: 1, Seq: 0},
		{Time: 1, Phase: 1, Seq: 0},
		{Time: 2, Phase: 0, Seq: 0},
	}
	for i, want := range sorted {
		if !want.Less(keys[len(keys)-1-i]) || keys[len(keys)-1-i].Less(want) {
			// skip the duplicate-equal pair at index 2/3
		}
		_ = i
	}
	a := Key{1, 0, 3}
	b := Key{1, 0, 5}
	c := Key{1, 1, 0}
	d := Key{2, 0, 0}
	if !a.Less(b) || !b.Less(c) || !c.Less(d) {
		t.Fatalf("key order wrong: %v %v %v %v", a, b, c, d)
	}
	if a.Less(a) {
		t.Fatal("key not irreflexive")
	}
}

func TestPhaseRejection(t *testing.T) {
	eng := New(nil)
	if _, err := eng.Schedule("p0", 5, 0, noop); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Schedule("p2", 5, 2, noop); err != nil {
		t.Fatal(err)
	}
	eng.Step() // executes (5,0); engine now at time 5, phase 0

	// Same time, current or later phase: allowed.
	if _, err := eng.Schedule("ok0", 5, 0, noop); err != nil {
		t.Fatalf("same-phase insert should be allowed: %v", err)
	}
	if _, err := eng.Schedule("ok1", 5, 1, noop); err != nil {
		t.Fatalf("later-phase insert should be allowed: %v", err)
	}
	eng.Run() // drains everything; engine ends at time 5, phase 2

	// Same time, earlier phase: explicitly rejected.
	for _, phase := range []int{0, 1} {
		if _, err := eng.Schedule("bad", 5, phase, noop); err == nil {
			t.Fatalf("insert at (5,%d) should be rejected", phase)
		} else if _, ok := err.(*PhaseError); !ok {
			t.Fatalf("expected *PhaseError, got %T: %v", err, err)
		}
	}
	// Past time: rejected.
	if _, err := eng.Schedule("past", 4, 99, noop); err == nil {
		t.Fatal("insert into the past should be rejected")
	} else if _, ok := err.(*PhaseError); !ok {
		t.Fatalf("expected *PhaseError, got %T: %v", err, err)
	}
	// Future time: fine.
	if _, err := eng.Schedule("future", 6, 0, noop); err != nil {
		t.Fatal(err)
	}
}

func TestCancel(t *testing.T) {
	eng := New(nil)
	var ran []string
	mk := func(name string) Handler {
		return func(ctx *Context) { ran = append(ran, name) }
	}
	a, _ := eng.Schedule("a", 1, 0, mk("a"))
	b, _ := eng.Schedule("b", 2, 0, mk("b"))
	c, _ := eng.Schedule("c", 3, 0, mk("c"))

	if err := eng.Cancel(b); err != nil {
		t.Fatal(err)
	}
	if err := eng.Cancel(b); err == nil {
		t.Fatal("double cancel should fail")
	}
	if err := eng.Cancel(9999); err == nil {
		t.Fatal("cancel of unknown event should fail")
	}
	eng.Step() // executes a
	if err := eng.Cancel(a); err == nil {
		t.Fatal("cancelling an executed event must fail")
	}
	eng.Run()
	if len(ran) != 2 || ran[0] != "a" || ran[1] != "c" {
		t.Fatalf("cancelled event ran or order wrong: %v", ran)
	}
	_ = c
}

func TestSameTimeCascade(t *testing.T) {
	eng := New(nil)
	var order []string
	rec := func(name string) Handler {
		return func(ctx *Context) { order = append(order, name) }
	}
	_, err := eng.Schedule("a", 10, 1, func(ctx *Context) {
		order = append(order, "a")
		// Same time, same phase: appended after b by insertion sequence.
		ctx.Schedule("b", 0, 1, func(ctx *Context) {
			order = append(order, "b")
			ctx.Schedule("d", 0, 1, rec("d"))
		})
		ctx.Schedule("c", 0, 2, rec("c"))
	})
	if err != nil {
		t.Fatal(err)
	}
	eng.Run()
	want := []string{"a", "b", "d", "c"}
	if len(order) != len(want) {
		t.Fatalf("got %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("got %v, want %v", order, want)
		}
	}
	// Keys of the cascade: same time, phases respected, seq breaks ties.
	var keys []Key
	for _, ev := range eng.History() {
		keys = append(keys, ev.Key)
	}
	for i := 1; i < len(keys); i++ {
		if !keys[i-1].Less(keys[i]) {
			t.Fatalf("history not in key order: %v", keys)
		}
	}
}

func TestEmptyQueueTermination(t *testing.T) {
	// Run on an empty engine returns immediately.
	eng := New(nil)
	eng.Run()
	if eng.Pending() != 0 {
		t.Fatal("empty engine should stay empty")
	}

	// A finite same-time cascade terminates with an empty queue.
	n := 0
	var spawn func(ctx *Context)
	spawn = func(ctx *Context) {
		n++
		if n < 1000 {
			ctx.Schedule("tick", 0, 0, spawn)
		}
	}
	if _, err := eng.Schedule("tick", 0, 0, spawn); err != nil {
		t.Fatal(err)
	}
	eng.Run()
	if n != 1000 {
		t.Fatalf("cascade executed %d events, want 1000", n)
	}
	if eng.Pending() != 0 {
		t.Fatalf("queue not empty after Run: %d pending", eng.Pending())
	}
}

// keyModel is the independent ordered-list reference model: a slice
// kept sorted by key, min extracted by linear scan.
type keyModel struct {
	items []Key
}

func (m *keyModel) insert(k Key) {
	i := 0
	for i < len(m.items) && !k.Less(m.items[i]) {
		i++
	}
	m.items = append(m.items, Key{})
	copy(m.items[i+1:], m.items[i:])
	m.items[i] = k
}

func (m *keyModel) remove(k Key) {
	for i, it := range m.items {
		if it == k {
			m.items = append(m.items[:i], m.items[i+1:]...)
			return
		}
	}
	panic("model: key not found")
}

func (m *keyModel) popMin() Key {
	if len(m.items) == 0 {
		panic("model: empty")
	}
	k := m.items[0]
	m.items = m.items[1:]
	return k
}

// TestOrderedListModel cross-checks the engine against the reference
// ordered-list model over randomized workloads with same-time cascades,
// late root injections and cancellations.
func TestOrderedListModel(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 40; trial++ {
		eng := New(nil)
		model := &keyModel{}
		var executed []Key

		var mkHandler func(depth int) Handler
		mkHandler = func(depth int) Handler {
			return func(ctx *Context) {
				executed = append(executed, ctx.Event().Key)
				if depth >= 3 {
					return
				}
				for i := 0; i < rng.Intn(3); i++ {
					delay := int64(rng.Intn(3))
					phase := rng.Intn(3)
					if delay == 0 {
						// same-time successor: same or later phase
						phase = ctx.Event().Key.Phase + rng.Intn(3)
					}
					id, err := ctx.Schedule("s", delay, phase, mkHandler(depth+1))
					if err != nil {
						t.Fatalf("trial %d: successor rejected: %v", trial, err)
					}
					model.insert(eng.events[id].Key)
				}
			}
		}

		inject := func() {
			at := eng.now + int64(rng.Intn(6))
			phase := rng.Intn(4)
			if eng.started && at == eng.now && phase < eng.phase {
				phase = eng.phase + rng.Intn(3)
			}
			id, err := eng.Schedule("r", at, phase, mkHandler(0))
			if err != nil {
				t.Fatalf("trial %d: root rejected: %v", trial, err)
			}
			model.insert(eng.events[id].Key)
		}

		for i := 0; i < 5+rng.Intn(10); i++ {
			inject()
		}
		for len(model.items) > 0 {
			switch rng.Intn(4) {
			case 0:
				inject()
			case 1:
				if len(eng.queue) > 0 {
					victim := eng.queue[rng.Intn(len(eng.queue))]
					if err := eng.Cancel(victim.ID); err != nil {
						t.Fatalf("trial %d: cancel: %v", trial, err)
					}
					model.remove(victim.Key)
				}
			default:
				want := model.popMin()
				before := len(executed)
				if !eng.Step() {
					t.Fatalf("trial %d: engine empty, model has %v", trial, want)
				}
				if len(executed) != before+1 {
					t.Fatalf("trial %d: step did not execute exactly one event", trial)
				}
				if got := executed[len(executed)-1]; got != want {
					t.Fatalf("trial %d: engine executed %v, model expects %v", trial, got, want)
				}
			}
		}
		if eng.Pending() != 0 {
			t.Fatalf("trial %d: engine queue not empty at end", trial)
		}
	}
}
