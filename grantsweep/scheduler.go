package grantsweep

import "fmt"

// MicroStep is one schedulable step in a concurrent client program.
type MicroStep struct {
	Client string
	Name   string
	run    func(w *World)
}

// ScheduleFinding records an interleaving where the weak model allows a call
// the reference rejects.
type ScheduleFinding struct {
	Policy     Policy
	Interleave []string
}

func retireProgram() []MicroStep {
	return []MicroStep{
		{Client: "retirer", Name: "BeginRetire(g1)", run: func(w *World) {
			_ = w.BeginRetire("carol", "g1")
		}},
		{Client: "retirer", Name: "FinishRetire", run: func(w *World) {
			_ = w.FinishRetire()
		}},
	}
}

// SearchRetireRace enumerates every interleaving of a two-step Retire and a
// grantee Use. Under the non-atomic policy the Use can land between status
// flip and generation bump; the reference model denies it in every schedule.
func SearchRetireRace(weak Policy) (*ScheduleFinding, int) {
	base := seededActiveWorld()
	useSpec := func(w *World) UseSpec {
		return UseSpec{
			Caller: "bob", Operation: OpDecrypt, KeyID: "k1", KeyVersion: 1,
			Context: Context{"env": "prod"}, Token: w.tokenFor("g1"),
		}
	}
	snapshots := map[*World]*UseSnapshot{}
	useProgram := []MicroStep{
		{Client: "grantee", Name: "SnapshotUse", run: func(w *World) {
			snapshots[w] = w.SnapshotUse(useSpec(w))
		}},
		{Client: "grantee", Name: "DecideUse", run: func(w *World) {
		}},
	}
	programs := [][]MicroStep{retireProgram(), useProgram}

	interleavings := enumerateInterleavings(programs)
	for _, order := range interleavings {
		refW := base.Clone()
		weakW := base.CloneWithPolicy(weak)
		var refAllowed, weakAllowed bool
		labels := make([]string, 0, len(order))
		for _, idx := range order {
			step := programs[idx.program][idx.idx]
			labels = append(labels, step.Client+":"+step.Name)
			if step.Name == "DecideUse" {
				refAllowed = refW.DecideUse(snapshots[refW])
				weakAllowed = weakW.DecideUse(snapshots[weakW])
			} else {
				step.run(refW)
				step.run(weakW)
			}
		}
		if weakAllowed && !refAllowed {
			return &ScheduleFinding{Policy: weak, Interleave: labels}, len(interleavings)
		}
	}
	return nil, len(interleavings)
}

type stepIndex struct{ program, idx int }

// enumerateInterleavings returns all valid global orders that preserve each
// program's internal step order. For two 2-step programs this is the four
// choose(4,2) interleavings.
func enumerateInterleavings(programs [][]MicroStep) [][]stepIndex {
	cursors := make([]int, len(programs))
	var out [][]stepIndex
	var current []stepIndex

	var recurse func()
	recurse = func() {
		advanced := false
		for p := range programs {
			if cursors[p] < len(programs[p]) {
				advanced = true
				cursors[p]++
				current = append(current, stepIndex{program: p, idx: cursors[p] - 1})
				recurse()
				current = current[:len(current)-1]
				cursors[p]--
			}
		}
		if !advanced {
			out = append(out, append([]stepIndex(nil), current...))
		}
	}
	recurse()
	return out
}

func seededActiveWorld() *World {
	w := NewWorld(Policy{})
	_, _ = w.BeginCreate(CreateSpec{
		GrantID: "g1", RequestID: "r1", Creator: "alice",
		KeyID: "k1", KeyVersion: 1, GranteePrincipal: "bob",
		RetiringPrincipal: "carol", Operations: []Operation{OpDecrypt},
		Constraints: Context{"env": "prod"}, AllowPending: true,
	})
	_, _ = w.Activate("alice", "r1")
	return w
}

// RenderSchedule formats a schedule finding for reports.
func RenderSchedule(f *ScheduleFinding) string {
	var b []byte
	b = append(b, fmt.Sprintf("weakness=%s\n", f.Policy.Name())...)
	for i, label := range f.Interleave {
		b = append(b, fmt.Sprintf("  %d. %s\n", i+1, label)...)
	}
	return string(b)
}
