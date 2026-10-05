package main

import "fmt"

// Round is one step of a distinguishing process: the attacker makes a
// strong move, the defender answers with a weak move (DefenderPath is the
// concrete tau*/label/tau* sequence). On the final round the defender may
// be Stuck, i.e. has no weak move at all.
type Round struct {
	Side         int // attacker side: 0 = A, 1 = B
	From, To     int
	Label        string
	DefenderFrom int
	DefenderTo   int
	DefenderPath []Step
	Stuck        bool
}

// Extract builds a replayable distinguishing process from the removal
// ranks/witnesses computed by Check. Ranks strictly decrease each round,
// so extraction always terminates with the defender stuck.
func Extract(a, b *LTS, res *Result) ([]Round, error) {
	if res.Equivalent {
		return nil, fmt.Errorf("systems are weakly bisimilar; no distinguisher exists")
	}
	cs, ct := a.Start, b.Start
	var rounds []Round
	for {
		p := Pair{cs, ct}
		r, removed := res.rank[p]
		if !removed {
			return nil, fmt.Errorf("internal error: pair (%d,%d) is in the relation", cs, ct)
		}
		w := res.witness[p]
		var def *LTS
		var defFrom int
		if w.Side == 0 {
			def, defFrom = b, ct
		} else {
			def, defFrom = a, cs
		}
		round := Round{
			Side: w.Side, From: w.From, To: w.To, Label: w.Label,
			DefenderFrom: defFrom,
		}
		targets := def.WeakTargets(defFrom, w.Label)
		if len(targets) == 0 {
			round.Stuck = true
			round.DefenderTo = defFrom
			rounds = append(rounds, round)
			return rounds, nil
		}
		best := -1
		bestRank := int(^uint(0) >> 1)
		for _, tp := range targets {
			var q Pair
			if w.Side == 0 {
				q = Pair{w.To, tp}
			} else {
				q = Pair{tp, w.To}
			}
			qr, ok := res.rank[q]
			if !ok {
				continue // cannot happen: would still be in the relation
			}
			if qr < bestRank {
				bestRank = qr
				best = tp
			}
		}
		if best < 0 || bestRank >= r {
			return nil, fmt.Errorf("internal error: cannot descend from pair (%d,%d)", cs, ct)
		}
		round.DefenderTo = best
		round.DefenderPath = def.FindWeakPath(defFrom, best, w.Label)
		rounds = append(rounds, round)
		if w.Side == 0 {
			cs, ct = w.To, best
		} else {
			cs, ct = best, w.To
		}
	}
}

// Replay re-validates a distinguishing process against both LTSs: every
// attacker step must be a real transition, every defender path must be a
// legal weak move of the matching kind, and the process must end with the
// defender genuinely stuck.
func Replay(a, b *LTS, rounds []Round) error {
	if len(rounds) == 0 {
		return fmt.Errorf("empty distinguisher")
	}
	cs, ct := a.Start, b.Start
	for i, r := range rounds {
		var att, def *LTS
		var attFrom, defFrom int
		if r.Side == 0 {
			att, def, attFrom, defFrom = a, b, cs, ct
		} else {
			att, def, attFrom, defFrom = b, a, ct, cs
		}
		if r.From != attFrom || r.DefenderFrom != defFrom {
			return fmt.Errorf("round %d: round does not continue the replay", i+1)
		}
		if !hasEdge(att, r.From, r.Label, r.To) {
			return fmt.Errorf("round %d: attacker step %d -%s-> %d is not a transition", i+1, r.From, r.Label, r.To)
		}
		if len(r.DefenderPath) == 0 {
			switch {
			case IsTau(r.Label):
				if r.Stuck {
					return fmt.Errorf("round %d: defender cannot be stuck on a tau move", i+1)
				}
				if r.DefenderTo != defFrom {
					return fmt.Errorf("round %d: empty defender path must stay at %d", i+1, defFrom)
				}
			default:
				if !r.Stuck {
					return fmt.Errorf("round %d: visible move requires a defender path", i+1)
				}
				if len(def.WeakTargets(defFrom, r.Label)) != 0 {
					return fmt.Errorf("round %d: defender is not actually stuck", i+1)
				}
				if i != len(rounds)-1 {
					return fmt.Errorf("round %d: defender stuck before the last round", i+1)
				}
			}
		} else if err := checkWeakPath(def, defFrom, r.DefenderTo, r.Label, r.DefenderPath); err != nil {
			return fmt.Errorf("round %d: %v", i+1, err)
		}
		if r.Side == 0 {
			cs, ct = r.To, r.DefenderTo
		} else {
			cs, ct = r.DefenderTo, r.To
		}
	}
	if !rounds[len(rounds)-1].Stuck {
		return fmt.Errorf("distinguisher does not end with the defender stuck")
	}
	return nil
}

func hasEdge(l *LTS, from int, label string, to int) bool {
	for _, e := range l.Out[from] {
		if e.Label == label && e.To == to {
			return true
		}
	}
	return false
}

// checkWeakPath verifies that path is a legal weak move from `from` to `to`
// of kind `label`: all steps tau, except exactly one `label` step when the
// label is visible.
func checkWeakPath(l *LTS, from, to int, label string, path []Step) error {
	cur := from
	visible := 0
	for _, st := range path {
		if st.From != cur {
			return fmt.Errorf("defender path broken at state %d", cur)
		}
		if !hasEdge(l, st.From, st.Label, st.To) {
			return fmt.Errorf("defender step %d -%s-> %d is not a transition", st.From, st.Label, st.To)
		}
		switch {
		case IsTau(st.Label):
		case !IsTau(label) && st.Label == label && visible == 0:
			visible++
		default:
			return fmt.Errorf("defender path uses unexpected label %q", st.Label)
		}
		cur = st.To
	}
	if cur != to {
		return fmt.Errorf("defender path ends at %d, want %d", cur, to)
	}
	if !IsTau(label) && visible != 1 {
		return fmt.Errorf("defender path must contain exactly one %q step", label)
	}
	return nil
}
