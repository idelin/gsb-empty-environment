package causal

import "fmt"

// VerifyLog checks a delivery log for the causal safety invariant:
// every message is delivered exactly once, per-sender sequence numbers
// are contiguous, and every causal predecessor appears earlier in the
// log. It returns nil when the log is a valid causal linearization.
func VerifyLog(log []Message) error {
	delivered := make(VectorClock)
	seen := make(map[MsgID]struct{})
	for i, m := range log {
		if _, ok := seen[m.ID]; ok {
			return fmt.Errorf("position %d: duplicate delivery of %v", i, m.ID)
		}
		if m.Clock[m.ID.Sender] != delivered[m.ID.Sender]+1 {
			return fmt.Errorf("position %d: non-contiguous delivery of %v", i, m.ID)
		}
		for k, v := range m.Clock {
			if k == m.ID.Sender {
				continue
			}
			if delivered[k] < v {
				return fmt.Errorf("position %d: %v delivered before causal predecessor %s#%d", i, m.ID, k, v)
			}
		}
		seen[m.ID] = struct{}{}
		delivered[m.ID.Sender] = m.ID.Seq
	}
	return nil
}

// CanonicalOrder produces the unique deterministic linearization of a
// causally-closed set of messages: a topological sort of the causal
// partial order with concurrent messages ordered by (Sender, Seq).
// Two nodes holding the same messages always compute the same order,
// which is what makes concurrent messages stably sortable. It returns
// an error if the set is not causally closed.
func CanonicalOrder(msgs []Message) ([]Message, error) {
	remaining := make([]Message, len(msgs))
	copy(remaining, msgs)
	emitted := make(VectorClock)
	var out []Message
	for len(remaining) > 0 {
		best := -1
		for i, m := range remaining {
			ready := true
			for k, v := range m.Clock {
				need := v
				if k == m.ID.Sender {
					need = v - 1 // own entry is the message itself, not a predecessor
				}
				if emitted[k] < need {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			if best == -1 || lessStable(m, remaining[best]) {
				best = i
			}
		}
		if best == -1 {
			return nil, fmt.Errorf("causal cycle or missing predecessor among %d messages", len(remaining))
		}
		m := remaining[best]
		remaining = append(remaining[:best], remaining[best+1:]...)
		out = append(out, m)
		emitted[m.ID.Sender] = m.ID.Seq
	}
	return out, nil
}

// SameOrder reports whether two logs contain the same messages in the
// same order.
func SameOrder(a, b []Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			return false
		}
	}
	return true
}
