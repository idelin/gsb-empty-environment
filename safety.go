package causal

import "fmt"

// VerifySafety checks the causal-delivery safety property over a delivery
// log: at every node, if message a happens-before message b (by vector
// clocks), then a was delivered before b. Returns the first violation.
func VerifySafety(log []DeliveryRecord) error {
	byNode := make(map[int][]DeliveryRecord)
	for _, r := range log {
		byNode[r.Node] = append(byNode[r.Node], r)
	}
	for node, recs := range byNode {
		for i := 0; i < len(recs); i++ {
			for j := i + 1; j < len(recs); j++ {
				if HappensBefore(recs[j].Msg.VC, recs[i].Msg.VC) {
					return fmt.Errorf(
						"safety violation at n%d: %s (order %d) delivered before its causal predecessor %s (order %d)",
						node, recs[j].Msg.ID, recs[j].Order, recs[i].Msg.ID, recs[i].Order)
				}
			}
		}
	}
	return nil
}

// VerifyExactlyOnce checks that within each node's log every message ID
// appears at most once (dedup evidence).
func VerifyExactlyOnce(log []DeliveryRecord) error {
	seen := make(map[int]map[MsgID]bool)
	for _, r := range log {
		if seen[r.Node] == nil {
			seen[r.Node] = make(map[MsgID]bool)
		}
		if seen[r.Node][r.Msg.ID] {
			return fmt.Errorf("duplicate delivery at n%d: %s", r.Node, r.Msg.ID)
		}
		seen[r.Node][r.Msg.ID] = true
	}
	return nil
}

// StableOrder deterministically sorts a set of messages into a total order
// that extends causality: if a happens-before b, a comes first; concurrent
// messages are ordered by (sender, seq). Kahn's algorithm with a
// deterministic tie-break, so every node computes the SAME order for the
// same set of messages regardless of local arrival/delivery order.
func StableOrder(msgs []Message) []Message {
	remaining := make([]Message, len(msgs))
	copy(remaining, msgs)
	var out []Message
	for len(remaining) > 0 {
		best := -1
		for i, cand := range remaining {
			if !minimal(cand, remaining) {
				continue
			}
			if best == -1 || lessTieBreak(cand, remaining[best]) {
				best = i
			}
		}
		if best == -1 {
			// Should be unreachable for valid vector clocks (no cycles).
			best = 0
		}
		out = append(out, remaining[best])
		remaining = append(remaining[:best], remaining[best+1:]...)
	}
	return out
}

// minimal reports whether no other message in set happens-before m.
func minimal(m Message, set []Message) bool {
	for _, other := range set {
		if other.ID == m.ID {
			continue
		}
		if HappensBefore(other.VC, m.VC) {
			return false
		}
	}
	return true
}
