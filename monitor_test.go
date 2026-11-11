package netsec

import (
	"fmt"
	"sync"
	"testing"
)

// allEvents is the short-short event set used for exhaustive permutation.
var allEvents = []EventKind{
	EventInitiate,
	EventCancelRequest,
	EventCancelConfirm,
	EventResultGenerated,
	EventResultConsumed,
}

func permutations(items []EventKind) [][]EventKind {
	var out [][]EventKind
	var walk func(prefix, rest []EventKind)
	walk = func(prefix, rest []EventKind) {
		if len(rest) == 0 {
			out = append(out, append([]EventKind(nil), prefix...))
			return
		}
		for i, k := range rest {
			next := append(append([]EventKind(nil), rest[:i]...), rest[i+1:]...)
			walk(append(prefix, k), next)
		}
	}
	walk(nil, items)
	return out
}

// refViolation is the independent reference model's expectation.
type refViolation struct {
	pos  int // 1-based position in the permutation
	kind ViolationKind
}

// reference simulates the contract directly from the event order and lists
// the violations a correct monitor must report, with their positions.
func reference(seq []EventKind) []refViolation {
	var initiated, requested, confirmed, generated, consumed bool
	var out []refViolation
	for i, k := range seq {
		pos := i + 1
		switch k {
		case EventInitiate:
			initiated = true
		case EventCancelRequest:
			if !initiated {
				out = append(out, refViolation{pos, ViolationEventBeforeInitiate})
			} else {
				requested = true
			}
		case EventCancelConfirm:
			if !initiated {
				out = append(out, refViolation{pos, ViolationEventBeforeInitiate})
			} else {
				if !requested {
					out = append(out, refViolation{pos, ViolationConfirmWithoutRequest})
				}
				confirmed = true
			}
		case EventResultGenerated:
			if !initiated {
				out = append(out, refViolation{pos, ViolationEventBeforeInitiate})
			} else {
				generated = true
			}
		case EventResultConsumed:
			switch {
			case !initiated:
				out = append(out, refViolation{pos, ViolationEventBeforeInitiate})
			case confirmed:
				out = append(out, refViolation{pos, ViolationConsumeAfterConfirm})
			case !generated:
				out = append(out, refViolation{pos, ViolationConsumeWithoutResult})
			case consumed:
				out = append(out, refViolation{pos, ViolationDuplicateConsume})
			default:
				consumed = true
			}
		}
	}
	return out
}

// TestAllShortPermutations feeds every permutation of the five lifecycle
// events through the monitor and checks violation kinds and positions
// against an independent reference model.
func TestAllShortPermutations(t *testing.T) {
	perms := permutations(allEvents)
	if len(perms) != 120 {
		t.Fatalf("expected 120 permutations, got %d", len(perms))
	}
	barrierHits := 0
	for _, perm := range perms {
		m := NewMonitor()
		for _, k := range perm {
			m.Emit("op", 1, k)
		}
		got := m.Violations()
		want := reference(perm)
		if len(got) != len(want) {
			t.Errorf("perm %v: got %d violations %v, want %d %v",
				perm, len(got), got, len(want), want)
			continue
		}
		for i, w := range want {
			if got[i].Kind != w.kind || got[i].Seq != w.pos {
				t.Errorf("perm %v: violation %d = %v, want kind=%v seq=%d",
					perm, i, got[i], w.kind, w.pos)
			}
			if got[i].Op != "op" || got[i].Gen != 1 {
				t.Errorf("perm %v: violation %d has wrong op/gen: %v", perm, i, got[i])
			}
		}
		for _, v := range got {
			if v.Kind == ViolationConsumeAfterConfirm {
				barrierHits++
			}
		}
	}
	// The barrier breach must actually be exercised. A consume violates the
	// barrier whenever I<Cf<Cs appear in that relative order (the barrier
	// holds even if the confirm itself lacked a request): 5!/3! = 20
	// permutations.
	if barrierHits != 20 {
		t.Fatalf("barrier exercised in %d permutations, want 20", barrierHits)
	}
}

// TestNormalRacesHaveNoViolations covers the legitimate orderings: consume
// before cancel, consume while cancel pending, and late result generation
// after confirm (never consumed).
func TestNormalRacesHaveNoViolations(t *testing.T) {
	cases := []struct {
		name string
		seq  []EventKind
		note string // expected note on the consume/generate entry
	}{
		{"consume-before-cancel", []EventKind{
			EventInitiate, EventResultGenerated, EventResultConsumed,
			EventCancelRequest, EventCancelConfirm,
		}, "consumed-before-cancel-request"},
		{"consume-while-cancel-pending", []EventKind{
			EventInitiate, EventResultGenerated, EventCancelRequest,
			EventResultConsumed, EventCancelConfirm,
		}, "consumed-while-cancel-pending"},
		{"generate-after-confirm-never-consumed", []EventKind{
			EventInitiate, EventCancelRequest, EventCancelConfirm,
			EventResultGenerated,
		}, "generated-after-confirm"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMonitor()
			for _, k := range tc.seq {
				m.Emit("op", 1, k)
			}
			if vs := m.Violations(); len(vs) != 0 {
				t.Fatalf("normal race flagged: %v", vs)
			}
			log := m.Timeline("op", 1)
			var found string
			for _, e := range log {
				if e.Note != "" {
					found = e.Note
				}
			}
			if found != tc.note {
				t.Fatalf("timeline note = %q, want %q (log=%v)", found, tc.note, log)
			}
		})
	}
}

// TestBarrierBreachPosition verifies the exact offending position of the
// classic bug: cancel confirmed, then the success callback is consumed.
func TestBarrierBreachPosition(t *testing.T) {
	m := NewMonitor()
	seq := []EventKind{
		EventInitiate, EventCancelRequest, EventCancelConfirm,
		EventResultGenerated, EventResultConsumed,
	}
	for _, k := range seq {
		m.Emit("op", 1, k)
	}
	vs := m.Violations()
	if len(vs) != 1 {
		t.Fatalf("got %v, want exactly one violation", vs)
	}
	if vs[0].Kind != ViolationConsumeAfterConfirm || vs[0].Seq != 5 {
		t.Fatalf("violation = %v, want consume-after-confirm at seq 5", vs[0])
	}
}

// TestGenerationReuse verifies that an operation ID can be reused across
// generations: a barrier breach in one generation neither hides nor taints
// another, and stale events stay attributed to their own generation.
func TestGenerationReuse(t *testing.T) {
	m := NewMonitor()

	g1 := m.Begin("op") // gen 1: canceled and confirmed
	m.Emit("op", g1, EventCancelRequest)
	m.Emit("op", g1, EventCancelConfirm)

	g2 := m.Begin("op") // gen 2: same ID reused, clean lifecycle
	m.Emit("op", g2, EventResultGenerated)
	m.Emit("op", g2, EventResultConsumed)

	if g1 != 1 || g2 != 2 {
		t.Fatalf("generations = %d,%d, want 1,2", g1, g2)
	}
	if vs := m.Violations(); len(vs) != 0 {
		t.Fatalf("clean reuse flagged: %v", vs)
	}

	// A stale consume against the confirmed generation 1 must still be
	// caught and attributed to gen 1, leaving gen 2 unaffected.
	m.Emit("op", g1, EventResultConsumed)
	vs := m.Violations()
	if len(vs) != 1 || vs[0].Kind != ViolationConsumeAfterConfirm || vs[0].Gen != 1 {
		t.Fatalf("stale-gen violation = %v, want consume-after-confirm on gen 1", vs)
	}
}

// TestConcurrentRecording ensures the monitor is safe for concurrent hosts.
func TestConcurrentRecording(t *testing.T) {
	m := NewMonitor()
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			op := fmt.Sprintf("op-%d", id)
			gen := m.Begin(op)
			m.Emit(op, gen, EventResultGenerated)
			m.Emit(op, gen, EventCancelRequest)
			m.Emit(op, gen, EventResultConsumed)
			m.Emit(op, gen, EventCancelConfirm)
		}(worker)
	}
	wg.Wait()
	if vs := m.Violations(); len(vs) != 0 {
		t.Fatalf("concurrent normal races flagged: %v", vs)
	}
}
