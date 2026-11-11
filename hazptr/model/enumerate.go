package model

import "fmt"

// Program is one thread's ordered sequence of explicit events.
type Program []Op

// Programs maps every thread id to its event sequence.
type Programs map[int]Program

// Witness is one concrete interleaving that violates the protocol.
type Witness struct {
	Kind      ViolationKind
	Violation Violation
	Trace     Trace
}

// Report summarizes an exhaustive small-state run.
type Report struct {
	// StatesExplored counts distinct machine states reached (memoized).
	StatesExplored int
	// Transitions counts every scheduled, accepted event.
	Transitions int
	// FeasibleBranches counts branches pruned as impossible.
	InfeasibleBranches int
	// Completed counts terminal schedules (all programs consumed).
	Completed int
	// Witnesses holds one representative trace per violation kind.
	Witnesses map[ViolationKind]Witness
	// ViolationCounts counts occurrences per violation kind.
	ViolationCounts map[ViolationKind]int
}

func newReport() *Report {
	return &Report{
		Witnesses:       map[ViolationKind]Witness{},
		ViolationCounts: map[ViolationKind]int{},
	}
}

// Safe reports whether no protocol violation was witnessed.
func (r *Report) Safe() bool { return len(r.Witnesses) == 0 }

type enumState struct {
	pc map[int]int
}

// Enumerate exhaustively interleaves two (or more) small per-thread programs.
// Per-thread program order is preserved; every legal choice of which thread
// advances next is explored, including every position of the explicit flush
// events, which is what exposes late-publication interleavings.
//
// memoKey keeps machine state plus per-thread program counters so equivalent
// continuations are explored only once, while violations remain reachable
// because a violating step always terminates that branch before memoization.
func Enumerate(threadIDs []int, initialRoots map[string]string, cfg Config, progs Programs) (*Report, error) {
	for _, id := range threadIDs {
		if _, ok := progs[id]; !ok {
			return nil, fmt.Errorf("missing program for thread T%d", id)
		}
	}
	report := newReport()
	seen := map[string]bool{}

	var dfs func(m *Machine, pc map[int]int)
	dfs = func(m *Machine, pc map[int]int) {
		key := fingerprint(m, pc)
		if seen[key] {
			return
		}
		seen[key] = true
		report.StatesExplored++

		progress := false
		for _, id := range threadIDs {
			prog := progs[id]
			if pc[id] >= len(prog) || m.Aborted(id) {
				continue
			}
			progress = true
			op := prog[pc[id]]

			next := cloneMachine(m)
			v := next.Step(op)
			switch v.Kind {
			case ViolationNone:
				report.Transitions++
				nextPC := clonePC(pc)
				nextPC[id]++
				dfs(next, nextPC)
			case ErrInfeasible:
				// An impossible branch under these program operands;
				// skip it rather than treat it as a protocol violation.
				report.InfeasibleBranches++
			default:
				report.ViolationCounts[v.Kind]++
				if _, have := report.Witnesses[v.Kind]; !have {
					report.Witnesses[v.Kind] = Witness{
						Kind:      v.Kind,
						Violation: v,
						Trace:     append(Trace{}, next.Trace()...),
					}
				}
			}
		}

		if !progress {
			report.Completed++
		}
	}

	dfs(NewMachine(threadIDs, initialRoots, cfg), zeroPC(threadIDs))
	return report, nil
}

func zeroPC(ids []int) map[int]int {
	pc := map[int]int{}
	for _, id := range ids {
		pc[id] = 0
	}
	return pc
}

func clonePC(pc map[int]int) map[int]int {
	out := make(map[int]int, len(pc))
	for k, v := range pc {
		out[k] = v
	}
	return out
}
