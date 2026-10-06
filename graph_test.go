package eventgraph

import (
	"math/rand"
	"reflect"
	"testing"
	"time"
)

// scenario builds a classic 3-process message exchange:
//
//	A: a1 --m1--> B        A: a1, a2(recv m2), a3
//	B: b1(recv m1) --m2--> A  B: b1, b2
//	C: c1 --m3--> B (never received -> informational only)
func scenario() []Event {
	base := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	at := func(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }
	return []Event{
		{ID: "a1", Process: "A", Seq: 1, Kind: KindSend, Message: "m1", WallClock: at(1)},
		{ID: "a2", Process: "A", Seq: 2, Kind: KindRecv, Message: "m2", WallClock: at(9)},
		{ID: "a3", Process: "A", Seq: 3, Kind: KindLocal, WallClock: at(10)},
		{ID: "b1", Process: "B", Seq: 1, Kind: KindRecv, Message: "m1", WallClock: at(2)},
		{ID: "b2", Process: "B", Seq: 2, Kind: KindSend, Message: "m2", WallClock: at(3)},
		{ID: "c1", Process: "C", Seq: 1, Kind: KindSend, Message: "m3", WallClock: at(1)},
	}
}

func topoIDs(order []*Event) []string {
	ids := make([]string, len(order))
	for i, e := range order {
		ids[i] = e.ID
	}
	return ids
}

func TestOutOfOrderImportDeterministic(t *testing.T) {
	events := scenario()

	var wantOrder []string
	var wantClocks map[string]uint64
	for trial := 0; trial < 50; trial++ {
		shuffled := append([]Event{}, events...)
		rand.New(rand.NewSource(int64(trial))).Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		g := NewGraph()
		for _, e := range shuffled {
			g.AddEvent(e)
		}
		order, err := g.TopoSort()
		if err != nil {
			t.Fatalf("trial %d: TopoSort: %v", trial, err)
		}
		clocks, err := g.LamportClocks()
		if err != nil {
			t.Fatalf("trial %d: LamportClocks: %v", trial, err)
		}
		if trial == 0 {
			wantOrder = topoIDs(order)
			wantClocks = clocks
			continue
		}
		if got := topoIDs(order); !reflect.DeepEqual(got, wantOrder) {
			t.Fatalf("trial %d: order %v != %v", trial, got, wantOrder)
		}
		if !reflect.DeepEqual(clocks, wantClocks) {
			t.Fatalf("trial %d: clocks %v != %v", trial, clocks, wantClocks)
		}
	}

	// Invariants: only the informational unmatched send m3.
	g := NewGraph()
	for _, e := range events {
		g.AddEvent(e)
	}
	vs := g.Validate()
	if len(vs) != 1 || vs[0].Kind != ViolationUnmatchedSend || vs[0].Message != "m3" {
		t.Fatalf("expected only unmatched-send m3, got %v", vs)
	}
}

func TestLamportClockValues(t *testing.T) {
	g := NewGraph()
	for _, e := range scenario() {
		g.AddEvent(e)
	}
	clocks, err := g.LamportClocks()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]uint64{
		"a1": 1, "b1": 2, "b2": 3, "a2": 4, "a3": 5, "c1": 1,
	}
	if !reflect.DeepEqual(clocks, want) {
		t.Fatalf("clocks %v != %v", clocks, want)
	}
	if !g.HappensBefore("a1", "a3") {
		t.Fatal("a1 should happen-before a3 transitively")
	}
	if g.HappensBefore("c1", "a1") {
		t.Fatal("c1 and a1 are concurrent")
	}
}

func TestWallClockNeverDrivesCausality(t *testing.T) {
	// Misleading wall clocks: b1 displays an EARLIER time than a1 even
	// though a1 -> b1 causally. Ordering must follow causality only.
	g := NewGraph()
	g.AddEvent(Event{ID: "a1", Process: "A", Seq: 1, Kind: KindSend, Message: "m",
		WallClock: time.Unix(1000, 0)})
	g.AddEvent(Event{ID: "b1", Process: "B", Seq: 1, Kind: KindRecv, Message: "m",
		WallClock: time.Unix(1, 0)})
	order, err := g.TopoSort()
	if err != nil {
		t.Fatal(err)
	}
	if got := topoIDs(order); !reflect.DeepEqual(got, []string{"a1", "b1"}) {
		t.Fatalf("order %v, want [a1 b1]", got)
	}
	clocks, _ := g.LamportClocks()
	if clocks["b1"] <= clocks["a1"] {
		t.Fatalf("recv clock %d must exceed send clock %d", clocks["b1"], clocks["a1"])
	}
}

func TestConsistentDuplicateMerges(t *testing.T) {
	g := NewGraph()
	late := Event{ID: "a1", Process: "A", Seq: 1, Kind: KindLocal, WallClock: time.Unix(500, 0)}
	early := late
	early.WallClock = time.Unix(100, 0)

	g.AddEvent(late)
	g.AddEvent(early) // identical causal content -> merge
	g.AddEvent(late)  // exact duplicate -> merge

	if n := len(g.Events()); n != 1 {
		t.Fatalf("got %d events, want 1", n)
	}
	if got := g.Event("a1").WallClock; !got.Equal(time.Unix(100, 0)) {
		t.Fatalf("merge kept wall clock %v, want earliest %v", got, time.Unix(100, 0))
	}
	if c := g.Contradictions(); len(c) != 0 {
		t.Fatalf("unexpected contradictions: %v", c)
	}
}

func TestConflictingDuplicateReported(t *testing.T) {
	g := NewGraph()
	g.AddEvent(Event{ID: "a1", Process: "A", Seq: 1, Kind: KindSend, Message: "m"})
	g.AddEvent(Event{ID: "a1", Process: "A", Seq: 2, Kind: KindRecv, Message: "m"}) // conflict

	cs := g.Contradictions()
	if len(cs) != 1 || cs[0].Kind != "conflicting-duplicate" {
		t.Fatalf("want one conflicting-duplicate, got %v", cs)
	}
	if g.Event("a1").Seq != 1 {
		t.Fatal("original event must be retained")
	}
	if n := len(g.Events()); n != 1 {
		t.Fatalf("conflicting duplicate must not be stored, got %d events", n)
	}
}

func TestContradictoryAssociationCycleEvidence(t *testing.T) {
	// Process P receives m at seq 1 but sends m at seq 2: the message edge
	// send->recv contradicts process order recv->send, closing a cycle.
	g := NewGraph()
	g.AddEvent(Event{ID: "p1", Process: "P", Seq: 1, Kind: KindRecv, Message: "m"})
	g.AddEvent(Event{ID: "p2", Process: "P", Seq: 2, Kind: KindSend, Message: "m"})

	cs := g.Contradictions()
	if len(cs) != 1 || cs[0].Kind != "cycle" {
		t.Fatalf("want one cycle contradiction, got %v", cs)
	}
	want := []string{"p1", "p2", "p1"}
	if !reflect.DeepEqual(cs[0].Cycle, want) {
		t.Fatalf("cycle evidence %v, want %v", cs[0].Cycle, want)
	}

	if _, err := g.TopoSort(); err == nil {
		t.Fatal("TopoSort must fail on cyclic graph")
	} else if ce, ok := err.(*CycleError); !ok || len(ce.Cycle) < 2 {
		t.Fatalf("want *CycleError with evidence, got %v", err)
	}

	vs := g.Validate()
	found := false
	for _, v := range vs {
		if v.Kind == ViolationCycle {
			found = true
		}
	}
	if !found {
		t.Fatalf("Validate must report a cycle violation, got %v", vs)
	}
}

func TestValidateMessageInvariants(t *testing.T) {
	g := NewGraph()
	g.AddEvent(Event{ID: "a1", Process: "A", Seq: 1, Kind: KindSend, Message: "lost"})
	g.AddEvent(Event{ID: "b1", Process: "B", Seq: 1, Kind: KindRecv, Message: "ghost"})
	g.AddEvent(Event{ID: "b3", Process: "B", Seq: 3, Kind: KindLocal}) // seq gap at 2
	g.AddEvent(Event{ID: "c1", Process: "C", Seq: 1, Kind: KindSend, Message: "m"})
	g.AddEvent(Event{ID: "d1", Process: "D", Seq: 1, Kind: KindRecv, Message: "m"})
	g.AddEvent(Event{ID: "d2", Process: "D", Seq: 2, Kind: KindRecv, Message: "m"}) // dup recv

	kinds := map[ViolationKind]bool{}
	for _, v := range g.Validate() {
		kinds[v.Kind] = true
	}
	for _, want := range []ViolationKind{
		ViolationUnmatchedSend, ViolationRecvWithoutSend,
		ViolationSeqGap, ViolationDuplicateRecv,
	} {
		if !kinds[want] {
			t.Fatalf("missing violation %s in %v", want, kinds)
		}
	}
	if kinds[ViolationCycle] {
		t.Fatal("no cycle expected here")
	}
}

func TestDuplicateSendViolation(t *testing.T) {
	g := NewGraph()
	g.AddEvent(Event{ID: "a1", Process: "A", Seq: 1, Kind: KindSend, Message: "m"})
	g.AddEvent(Event{ID: "b1", Process: "B", Seq: 1, Kind: KindSend, Message: "m"})
	g.AddEvent(Event{ID: "c1", Process: "C", Seq: 1, Kind: KindRecv, Message: "m"})

	found := false
	for _, v := range g.Validate() {
		if v.Kind == ViolationDuplicateSend && v.Message == "m" {
			found = true
			if !reflect.DeepEqual(v.Events, []string{"a1", "b1"}) {
				t.Fatalf("duplicate send events %v, want [a1 b1]", v.Events)
			}
		}
	}
	if !found {
		t.Fatal("duplicate-send violation not reported")
	}
	if len(g.Contradictions()) == 0 {
		t.Fatal("duplicate send should also record a contradiction")
	}
}

func TestDeterministicTieBreaking(t *testing.T) {
	// Concurrent events must always order by ID, not insertion order.
	g := NewGraph()
	g.AddEvent(Event{ID: "z1", Process: "Z", Seq: 1})
	g.AddEvent(Event{ID: "m1", Process: "M", Seq: 1})
	g.AddEvent(Event{ID: "a1", Process: "A", Seq: 1})
	order, err := g.TopoSort()
	if err != nil {
		t.Fatal(err)
	}
	if got := topoIDs(order); !reflect.DeepEqual(got, []string{"a1", "m1", "z1"}) {
		t.Fatalf("order %v, want [a1 m1 z1]", got)
	}
}
