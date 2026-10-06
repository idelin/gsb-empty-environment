package eventlog

import (
	"math/rand"
	"testing"
)

// mergeByCount naively merges events by type counts, ignoring order: all
// Adds become one Add of the summed args, all Muls one Mul of the product,
// and so on. This is only valid for commuting operations.
func mergeByCount(events []Event) []Run {
	add, sub, mul := 0, 0, 1
	hasAdd, hasSub, hasMul, hasSet := false, false, false, false
	set := 0
	for _, e := range events {
		switch e.Op {
		case OpAdd:
			add += e.Arg
			hasAdd = true
		case OpSub:
			sub += e.Arg
			hasSub = true
		case OpMul:
			mul *= e.Arg
			hasMul = true
		case OpSet:
			set = e.Arg
			hasSet = true
		}
	}
	var runs []Run
	if hasAdd {
		runs = append(runs, Run{Event: Event{Op: OpAdd, Arg: add}, Count: 1})
	}
	if hasSub {
		runs = append(runs, Run{Event: Event{Op: OpSub, Arg: sub}, Count: 1})
	}
	if hasMul {
		runs = append(runs, Run{Event: Event{Op: OpMul, Arg: mul}, Count: 1})
	}
	if hasSet {
		runs = append(runs, Run{Event: Event{Op: OpSet, Arg: set}, Count: 1})
	}
	return runs
}

// compressByCount builds a bad compression: events merged by type counts but
// with the original anchors (initial/final summaries and observations).
func compressByCount(initial State, entries []Entry, final State) CompressedLog {
	c := CompressedLog{Initial: initial, Final: final, Runs: mergeByCount(expandEntries(entries))}
	for i, entry := range entries {
		if entry.Public {
			c.Observations = append(c.Observations, Observation{Index: i, Value: entry.Observe})
		}
	}
	return c
}

func expandEntries(entries []Entry) []Event {
	events := make([]Event, len(entries))
	for i, entry := range entries {
		events[i] = entry.Event
	}
	return events
}

// TestNonCommutativeCountMergeRejected checks that merging non-commuting
// operations by type counts is rejected by the verifier.
func TestNonCommutativeCountMergeRejected(t *testing.T) {
	cases := []struct {
		name    string
		initial int
		events  []Event
		public  map[int]bool
	}{
		{
			name:    "add then mul diverges in final state",
			initial: 0,
			events:  []Event{{Op: OpAdd, Arg: 1}, {Op: OpMul, Arg: 3}, {Op: OpAdd, Arg: 2}},
			public:  map[int]bool{},
		},
		{
			name:    "set rescues final state but observation diverges",
			initial: 0,
			events:  []Event{{Op: OpSet, Arg: 5}, {Op: OpAdd, Arg: 1}, {Op: OpMul, Arg: 2}, {Op: OpSet, Arg: 9}},
			public:  map[int]bool{0: true, 2: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			initial := State{Value: tc.initial}
			entries, final := Execute(initial, tc.events, func(i int, e Event) bool {
				return tc.public[i]
			})
			good := Compress(initial, entries, final)
			if err := good.Verify(); err != nil {
				t.Fatalf("valid compression rejected: %v", err)
			}
			bad := compressByCount(initial, entries, final)
			if err := bad.Verify(); err == nil {
				t.Fatalf("count-merged compression accepted: %+v", bad)
			}
		})
	}
}

// TestCountMergeRejectedRandomLogs merges random logs by type counts and
// requires the verifier to reject the result whenever the merged replay
// actually diverges from the original anchors.
func TestCountMergeRejectedRandomLogs(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	rejected := 0
	for trial := 0; trial < 500; trial++ {
		initial := State{Value: r.Intn(11) - 5}
		events := randomEvents(r, 3+r.Intn(20))
		entries, final := Execute(initial, events, func(i int, e Event) bool {
			return r.Intn(2) == 0
		})
		bad := compressByCount(initial, entries, final)
		// Determine independently whether the merged replay diverges.
		_, mergedFinal := Execute(initial, bad.Expand(), nil)
		diverges := mergedFinal != final
		for _, obs := range bad.Observations {
			_, mergedEntries := replayAt(initial, bad.Expand(), obs.Index)
			if mergedEntries != obs.Value {
				diverges = true
			}
		}
		if !diverges {
			continue // coincidental match; nothing to reject
		}
		if err := bad.Verify(); err == nil {
			t.Fatalf("trial %d: diverging count-merged compression accepted", trial)
		}
		rejected++
	}
	if rejected == 0 {
		t.Fatal("no diverging count-merged compression was produced")
	}
}

func replayAt(initial State, events []Event, index int) (State, int) {
	state := initial
	for i := 0; i <= index && i < len(events); i++ {
		state = events[i].Apply(state)
	}
	return state, state.Value
}

// TestCommutativeCountMergeAccepted shows the boundary of the rule: when all
// operations commute (only Add/Sub) and there are no intermediate public
// observation points, count-based merging is a valid compression.
func TestCommutativeCountMergeAccepted(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for trial := 0; trial < 200; trial++ {
		initial := State{Value: r.Intn(11) - 5}
		n := 1 + r.Intn(20)
		events := make([]Event, n)
		for i := range events {
			op := OpAdd
			if r.Intn(2) == 0 {
				op = OpSub
			}
			events[i] = Event{Op: op, Arg: r.Intn(21) - 10}
		}
		entries, final := Execute(initial, events, nil)
		c := compressByCount(initial, entries, final)
		if err := c.Verify(); err != nil {
			t.Fatalf("trial %d: commutative count-merged compression rejected: %v", trial, err)
		}
	}
}

// TestBadCompressionsRejected tampers with an otherwise valid compression in
// various ways and requires the verifier to reject every tampered copy.
func TestBadCompressionsRejected(t *testing.T) {
	initial := State{Value: 1}
	events := []Event{{Op: OpAdd, Arg: 2}, {Op: OpMul, Arg: 3}, {Op: OpSub, Arg: 1}}
	entries, final := Execute(initial, events, func(i int, e Event) bool { return i == 1 })
	good := Compress(initial, entries, final)
	if err := good.Verify(); err != nil {
		t.Fatalf("valid compression rejected: %v", err)
	}

	cases := []struct {
		name   string
		tamper func(c *CompressedLog)
	}{
		{"wrong final summary", func(c *CompressedLog) { c.Final.Value++ }},
		{"wrong initial summary", func(c *CompressedLog) { c.Initial.Value++ }},
		{"dropped event", func(c *CompressedLog) { c.Runs = c.Runs[:len(c.Runs)-1] }},
		{"reordered events", func(c *CompressedLog) { c.Runs[0], c.Runs[1] = c.Runs[1], c.Runs[0] }},
		{"wrong observation value", func(c *CompressedLog) { c.Observations[0].Value++ }},
		{"observation index out of range", func(c *CompressedLog) { c.Observations[0].Index = 42 }},
		{"observation indices not increasing", func(c *CompressedLog) {
			c.Observations = append(c.Observations, Observation{Index: 0, Value: 3})
		}},
		{"zero-count run", func(c *CompressedLog) { c.Runs[0].Count = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := good
			bad.Runs = append([]Run(nil), good.Runs...)
			bad.Observations = append([]Observation(nil), good.Observations...)
			tc.tamper(&bad)
			if err := bad.Verify(); err == nil {
				t.Fatalf("tampered compression accepted: %+v", bad)
			}
		})
	}
}
