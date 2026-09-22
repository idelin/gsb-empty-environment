package sim

import "testing"

type probe struct {
	log  *[]string
	name string
}

func (p *probe) Receive(m Msg, send func(Msg), after func(any, int)) {
	*p.log = append(*p.log, m.Body.(string))
	// Forward scheduling proves timers move the virtual clock and ordering
	// stays FIFO among equal-time events.
	if m.Body.(string) == "start" {
		send(Msg{From: p.name, To: p.name, Body: "same-time-1"})
		send(Msg{From: p.name, To: p.name, Body: "same-time-2"})
		after("later", 5)
	}
}

func TestSimulatorVirtualTimeAndFIFO(t *testing.T) {
	var log []string
	a := &probe{log: &log, name: "a"}
	s := New(map[string]Actor{"a": a})
	s.Send(Msg{To: "a", Body: "start"})
	end, err := s.Run(100)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := []string{"start", "same-time-1", "same-time-2", "later"}
	if len(log) != len(want) {
		t.Fatalf("events = %v, want %v", log, want)
	}
	for i := range want {
		if log[i] != want[i] {
			t.Fatalf("event %d = %q, want %q (full %v)", i, log[i], want[i], log)
		}
	}
	if end != 5 {
		t.Fatalf("virtual end = %d, want 5", end)
	}
}

func TestSimulatorUnknownActor(t *testing.T) {
	s := New(map[string]Actor{})
	s.Send(Msg{To: "ghost", Body: "x"})
	if _, err := s.Run(10); err == nil {
		t.Fatal("expected error for unknown actor")
	}
}

func TestSimulatorEventBudget(t *testing.T) {
	looper := ActorFunc(func(m Msg, send func(Msg), after func(any, int)) {
		send(Msg{To: "l", Body: "again"})
	})
	s := New(map[string]Actor{"l": looper})
	s.Send(Msg{To: "l", Body: "again"})
	if _, err := s.Run(3); err == nil {
		t.Fatal("expected event-budget error to break livelock")
	}
}

// ActorFunc adapts a function into an Actor (test helper).
type ActorFunc func(m Msg, send func(Msg), after func(any, int))

func (f ActorFunc) Receive(m Msg, send func(Msg), after func(any, int)) {
	f(m, send, after)
}
