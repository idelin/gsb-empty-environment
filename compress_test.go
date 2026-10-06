package eventlog

import (
	"math/rand"
	"testing"
)

func randomEvents(r *rand.Rand, n int) []Event {
	events := make([]Event, n)
	for i := range events {
		op := OpKind(r.Intn(4))
		arg := r.Intn(21) - 10
		if op == OpMul {
			arg = r.Intn(7) - 3 // keep values from exploding
		}
		events[i] = Event{Op: op, Arg: arg}
	}
	return events
}

// TestCompressVerifyRandomLogs checks, over random logs, that compression is
// equivalent to the original execution: the compressed segment verifies, its
// replay reproduces the original final state, and every public observation
// point is preserved.
func TestCompressVerifyRandomLogs(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for trial := 0; trial < 500; trial++ {
		initial := State{Value: r.Intn(21) - 10}
		events := randomEvents(r, r.Intn(60))
		entries, final := Execute(initial, events, func(i int, e Event) bool {
			return r.Intn(3) == 0
		})
		c := Compress(initial, entries, final)
		if err := c.Verify(); err != nil {
			t.Fatalf("trial %d: valid compression rejected: %v", trial, err)
		}
		replayedEntries, replayedFinal := Execute(c.Initial, c.Expand(), nil)
		if replayedFinal != final {
			t.Fatalf("trial %d: replayed final %v != original final %v", trial, replayedFinal, final)
		}
		if len(replayedEntries) != len(entries) {
			t.Fatalf("trial %d: replayed %d events, original had %d", trial, len(replayedEntries), len(entries))
		}
		obs := 0
		for i, entry := range entries {
			if !entry.Public {
				continue
			}
			if obs >= len(c.Observations) || c.Observations[obs].Index != i || c.Observations[obs].Value != entry.Observe {
				t.Fatalf("trial %d: observation point at entry %d not preserved", trial, i)
			}
			obs++
		}
		if obs != len(c.Observations) {
			t.Fatalf("trial %d: %d observations recorded, original had %d", trial, len(c.Observations), obs)
		}
	}
}

// TestCompressMergesOnlyConsecutiveIdentical ensures compression never
// reorders events to build longer runs.
func TestCompressMergesOnlyConsecutiveIdentical(t *testing.T) {
	events := []Event{
		{Op: OpAdd, Arg: 1}, {Op: OpAdd, Arg: 1},
		{Op: OpMul, Arg: 2},
		{Op: OpAdd, Arg: 1},
	}
	entries, final := Execute(State{}, events, nil)
	c := Compress(State{}, entries, final)
	want := []Run{
		{Event: Event{Op: OpAdd, Arg: 1}, Count: 2},
		{Event: Event{Op: OpMul, Arg: 2}, Count: 1},
		{Event: Event{Op: OpAdd, Arg: 1}, Count: 1},
	}
	if len(c.Runs) != len(want) {
		t.Fatalf("got %d runs, want %d: %+v", len(c.Runs), len(want), c.Runs)
	}
	for i, run := range want {
		if c.Runs[i] != run {
			t.Fatalf("run %d: got %+v, want %+v", i, c.Runs[i], run)
		}
	}
}
