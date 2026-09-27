package clocksync

// CorrectedTS maps a local timestamp to reference-node time using the
// solved offset for that node.
func CorrectedTS(node string, ts float64, offsets map[string]float64) float64 {
	return ts - offsets[node]
}

// Violation is a causal edge that is reversed after correction.
type Violation struct {
	Msg   Message
	Delta float64 // corrected recv - corrected send; negative
}

// CheckCausal verifies that no send->recv edge is reversed after clock
// correction. Any feasible offset assignment from Solve keeps every edge
// non-reversed, because theta[to] - theta[from] <= recv - send holds for
// every message by construction.
func CheckCausal(msgs []Message, offsets map[string]float64) []Violation {
	const eps = 1e-9
	var out []Violation
	for _, m := range msgs {
		delta := CorrectedTS(m.To, m.RecvTS, offsets) - CorrectedTS(m.From, m.SendTS, offsets)
		if delta < -eps {
			out = append(out, Violation{Msg: m, Delta: delta})
		}
	}
	return out
}
