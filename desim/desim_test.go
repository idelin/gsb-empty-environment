package desim_test

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"desim"
)

type state struct {
	log []string
}

func (s *state) append(v string) { s.log = append(s.log, v) }

func cloneState(v interface{}) interface{} {
	s := v.(*state)
	return &state{log: append([]string(nil), s.log...)}
}

func newSim() *desim.Sim { return desim.New(&state{}, cloneState, nil) }

func logOf(s *desim.Sim) []string { return s.State().(*state).log }

func appendH(name string) desim.Handler {
	return func(ctx *desim.Context) error {
		ctx.State().(*state).append(name)
		return nil
	}
}

type model struct {
	keys []desim.Key
}

func (m *model) insert(k desim.Key) {
	i := sort.Search(len(m.keys), func(i int) bool { return k.Less(m.keys[i]) })
	m.keys = append(m.keys, desim.Key{})
	copy(m.keys[i+1:], m.keys[i:])
	m.keys[i] = k
}

func (m *model) pop() (desim.Key, bool) {
	if len(m.keys) == 0 {
		return desim.Key{}, false
	}
	k := m.keys[0]
	m.keys = m.keys[1:]
	return k, true
}

func TestSameTimeCascadeOrderedModel(t *testing.T) {
	sim := newSim()
	m := &model{}
	var executed []desim.Key

	var cascade func(name string, phase int) desim.Handler
	cascade = func(name string, phase int) desim.Handler {
		return func(ctx *desim.Context) error {
			executed = append(executed, ctx.Event.Key)
			ctx.State().(*state).append(name)
			if phase < 3 {
				ev, err := ctx.Schedule(name+"'", 0, phase+1, cascade(name+"'", phase+1))
				if err != nil {
					return err
				}
				m.insert(ev.Key)
			}
			return nil
		}
	}

	seeds := []struct {
		name  string
		time  desim.Time
		phase int
	}{
		{"a", 0, 0},
		{"b", 0, 1},
		{"c", 1, 0},
	}
	for _, seed := range seeds {
		ev, err := sim.Schedule(seed.name, seed.time, seed.phase, cascade(seed.name, seed.phase))
		if err != nil {
			t.Fatalf("schedule %s: %v", seed.name, err)
		}
		m.insert(ev.Key)
	}

	sim.Run()

	for i, got := range executed {
		want, ok := m.pop()
		if !ok {
			t.Fatalf("model exhausted at %d, sim executed %v", i, got)
		}
		if got != want {
			t.Fatalf("order mismatch at %d: sim=%v model=%v", i, got, want)
		}
	}
	if _, ok := m.pop(); ok {
		t.Fatal("model has leftover events: sim terminated early")
	}

	want := []string{"a", "b", "a'", "b'", "a''", "b''", "a'''", "c", "c'", "c''", "c'''"}
	if !reflect.DeepEqual(logOf(sim), want) {
		t.Fatalf("cascade order:\n got %v\nwant %v", logOf(sim), want)
	}
}

func TestPhaseGuard(t *testing.T) {
	sim := newSim()
	if _, err := sim.Schedule("e", 1, 1, appendH("e")); err != nil {
		t.Fatal(err)
	}
	sim.Run()

	if _, err := sim.Schedule("past-time", 0, 9, appendH("x")); err == nil {
		t.Fatal("expected rejection of past time")
	}
	if _, err := sim.Schedule("past-phase", 1, 0, appendH("x")); err == nil {
		t.Fatal("expected rejection of earlier phase at same time")
	}
	if _, err := sim.Schedule("same-phase", 1, 1, appendH("ok1")); err != nil {
		t.Fatalf("same phase as last executed must be allowed: %v", err)
	}
	if _, err := sim.Schedule("later-phase", 1, 2, appendH("ok2")); err != nil {
		t.Fatalf("later phase must be allowed: %v", err)
	}
	sim.Run()
	if want := []string{"e", "ok1", "ok2"}; !reflect.DeepEqual(logOf(sim), want) {
		t.Fatalf("got %v want %v", logOf(sim), want)
	}
}

func TestPhaseGuardInsideHandler(t *testing.T) {
	sim := newSim()
	var guardErr error
	_, err := sim.Schedule("root", 5, 2, func(ctx *desim.Context) error {
		if _, err := ctx.Schedule("backwards", 0, 1, appendH("x")); err != nil {
			guardErr = err
		}
		_, err := ctx.Schedule("forwards", 0, 3, appendH("ok"))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sim.Run()
	if guardErr == nil {
		t.Fatal("expected rejection of earlier-phase successor at same time")
	}
	if want := []string{"ok"}; !reflect.DeepEqual(logOf(sim), want) {
		t.Fatalf("got %v want %v", logOf(sim), want)
	}
}

func TestCancel(t *testing.T) {
	sim := newSim()
	a, _ := sim.Schedule("a", 0, 0, appendH("a"))
	b, _ := sim.Schedule("b", 1, 0, appendH("b"))

	if err := sim.Cancel(a.ID); err != nil {
		t.Fatal(err)
	}
	sim.Run()
	if want := []string{"b"}; !reflect.DeepEqual(logOf(sim), want) {
		t.Fatalf("got %v want %v", logOf(sim), want)
	}

	err := sim.Cancel(b.ID)
	if _, ok := err.(*desim.AlreadyExecutedError); !ok {
		t.Fatalf("expected AlreadyExecutedError, got %v", err)
	}
	if err := sim.Cancel(9999); err != desim.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestEmptyQueueTermination(t *testing.T) {
	sim := newSim()
	sim.Run()
	if sim.Step() {
		t.Fatal("Step on empty queue must return false")
	}

	if _, err := sim.Schedule("leaf", 0, 0, appendH("leaf")); err != nil {
		t.Fatal(err)
	}
	sim.Run()
	if sim.Step() {
		t.Fatal("queue must be empty after Run")
	}
	if want := []string{"leaf"}; !reflect.DeepEqual(logOf(sim), want) {
		t.Fatalf("got %v want %v", logOf(sim), want)
	}
}

func TestUndoCascadeCheckpointReplay(t *testing.T) {
	sim := newSim()

	hC := appendH("C")
	hB := func(ctx *desim.Context) error {
		ctx.State().(*state).append("B")
		_, err := ctx.Schedule("C", 1, 0, hC)
		return err
	}
	hA := func(ctx *desim.Context) error {
		ctx.State().(*state).append("A")
		_, err := ctx.Schedule("B", 1, 0, hB)
		return err
	}
	if _, err := sim.Schedule("A", 0, 0, hA); err != nil {
		t.Fatal(err)
	}
	if _, err := sim.Schedule("D", 3, 0, appendH("D")); err != nil {
		t.Fatal(err)
	}

	sim.Step()
	sim.Checkpoint()
	sim.Step()
	sim.Step()
	sim.Checkpoint()
	sim.Step()
	if want := []string{"A", "B", "C", "D"}; !reflect.DeepEqual(logOf(sim), want) {
		t.Fatalf("got %v want %v", logOf(sim), want)
	}

	b, _ := sim.Get(2) // 序号：A=0, D=1, B=2, C=3
	if b.Name != "B" {
		t.Fatalf("unexpected event: %s", b)
	}
	if err := sim.Undo(b.ID); err != nil {
		t.Fatal(err)
	}

	if want := []string{"A", "D"}; !reflect.DeepEqual(logOf(sim), want) {
		t.Fatalf("after undo got %v want %v", logOf(sim), want)
	}
	c, _ := sim.Get(3)
	if b.Status != desim.StatusUndone || c.Status != desim.StatusUndone {
		t.Fatalf("B,C must be undone: B=%s C=%s", b.Status, c.Status)
	}

	rl := sim.ReplayLog()
	var actions []string
	for _, e := range rl {
		actions = append(actions, e.Name+":"+e.Action)
	}
	wantActions := []string{"B:skipped-undone", "C:skipped-undone", "D:replayed"}
	if !reflect.DeepEqual(actions, wantActions) {
		t.Fatalf("replay log got %v want %v", actions, wantActions)
	}

	chain := sim.CausalChain()
	frags := []string{
		"A (t=0,p=0,#0) [executed]",
		"B (t=1,p=0,#2) [undone]",
		"C (t=2,p=0,#3) [undone]",
		"D (t=3,p=0,#1) [executed]",
	}
	for _, frag := range frags {
		if !strings.Contains(chain, frag) {
			t.Fatalf("causal chain missing %q:\n%s", frag, chain)
		}
	}

	if err := sim.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestUndoExternalCompensation(t *testing.T) {
	sim := newSim()
	var emitted []string

	hC := appendH("C")
	hX := func(ctx *desim.Context) error {
		ctx.State().(*state).append("X")
		if !ctx.Replay {
			emitted = append(emitted, "X")
		}
		_, err := ctx.Schedule("C", 1, 0, hC)
		return err
	}
	hA := func(ctx *desim.Context) error {
		ctx.State().(*state).append("A")
		_, err := ctx.ScheduleExternal("X", 1, 0, hX)
		return err
	}
	a, err := sim.Schedule("A", 0, 0, hA)
	if err != nil {
		t.Fatal(err)
	}
	sim.Run()
	if want := []string{"A", "X", "C"}; !reflect.DeepEqual(logOf(sim), want) {
		t.Fatalf("got %v want %v", logOf(sim), want)
	}

	if err := sim.Undo(a.ID); err != nil {
		t.Fatal(err)
	}

	if want := []string{"X"}; !reflect.DeepEqual(logOf(sim), want) {
		t.Fatalf("after undo got %v want %v", logOf(sim), want)
	}
	if want := []string{"X"}; !reflect.DeepEqual(emitted, want) {
		t.Fatalf("external effect must be emitted exactly once, got %v", emitted)
	}
	comps := sim.Compensations()
	if len(comps) != 1 || comps[0].Name != "X" {
		t.Fatalf("expected one compensation for X, got %v", comps)
	}
	x, _ := sim.Get(comps[0].EventID)
	if x.Status != desim.StatusExecuted {
		t.Fatalf("external event must remain executed, got %s", x.Status)
	}

	if err := sim.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestUndoStatuses(t *testing.T) {
	sim := newSim()
	a, _ := sim.Schedule("a", 0, 0, appendH("a"))
	b, _ := sim.Schedule("b", 1, 0, appendH("b"))

	if err := sim.Undo(a.ID); err != nil {
		t.Fatal(err)
	}
	if a.Status != desim.StatusCancelled {
		t.Fatalf("got %s", a.Status)
	}
	if err := sim.Undo(a.ID); err == nil {
		t.Fatal("undo of cancelled event must fail")
	}
	if err := sim.Undo(9999); err != desim.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	sim.Run()
	if err := sim.Undo(b.ID); err != nil {
		t.Fatal(err)
	}
	if b.Status != desim.StatusUndone {
		t.Fatalf("got %s", b.Status)
	}
	if err := sim.Undo(b.ID); err == nil {
		t.Fatal("undo of undone event must fail")
	}
	if err := sim.Verify(); err != nil {
		t.Fatal(err)
	}
}
