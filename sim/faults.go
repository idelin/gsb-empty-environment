package sim

import "math/rand"

// Faults models all network imperfections, which are fully under the
// experimenter's control in the single-process event simulator.
//
// Every Send is classified independently, so the same logical message
// resent after a timeout may be delivered, dropped or duplicated on
// different attempts. The network itself never deduplicates: retransmitted
// packets are treated as independent; duplicate suppression happens at the
// application layer keyed by the unique TransferID.
type Faults interface {
	// Delay is the virtual-time delivery delay of a message.
	Delay(m Message) int64
	// Drop reports whether a message copy is lost.
	Drop(m Message) bool
	// Duplicate reports whether an extra copy is injected.
	Duplicate(m Message) bool
}

// Perfect is a reliable, fixed-delay, duplicate-free network.
type Perfect struct{ D int64 }

func (p Perfect) Delay(Message) int64 {
	if p.D <= 0 {
		return 1
	}
	return p.D
}
func (p Perfect) Drop(Message) bool      { return false }
func (p Perfect) Duplicate(Message) bool { return false }

// RandomFaults independently drops / duplicates each message with the given
// probabilities and assigns a uniform random delay in [minD, maxD].
type RandomFaults struct {
	Rand                    *rand.Rand
	MinD, MaxD              int64
	DropProb, DuplicateProb float64
}

func (r RandomFaults) d() int64 {
	if r.MaxD <= r.MinD {
		if r.MinD <= 0 {
			return 1
		}
		return r.MinD
	}
	return r.MinD + r.Rand.Int63n(r.MaxD-r.MinD+1)
}
func (r RandomFaults) Delay(Message) int64 { return r.d() }
func (r RandomFaults) Drop(Message) bool {
	return r.DropProb > 0 && r.Rand.Float64() < r.DropProb
}
func (r RandomFaults) Duplicate(Message) bool {
	return r.DuplicateProb > 0 && r.Rand.Float64() < r.DuplicateProb
}

// ScriptedFaults forces chosen messages to drop / duplicate deterministically
// (keyed by message identity and send time, so retries differ) and applies
// random faults to everything else. It is used by the reproducible demo.
type ScriptedFaults struct {
	Rand                    *rand.Rand
	MinD, MaxD              int64
	DropProb, DuplicateProb float64
	ForceDrop               map[string]bool
	ForceDup                map[string]bool
}

// Key identifies one concrete send of a message.
func FaultKey(m Message) string {
	k := "t"
	if m.Kind == MsgAck {
		k = "a"
	}
	return itoa(m.Sender) + ">" + itoa(m.Target) + ":" + k + ":" + itoa(m.ID.Seq) + "@" + itoa(m.SentAt)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func (s ScriptedFaults) Delay(Message) int64 {
	if s.MaxD <= s.MinD {
		if s.MinD <= 0 {
			return 1
		}
		return s.MinD
	}
	return s.MinD + s.Rand.Int63n(s.MaxD-s.MinD+1)
}
func (s ScriptedFaults) Drop(m Message) bool {
	if s.ForceDrop[FaultKey(m)] {
		return true
	}
	return s.DropProb > 0 && s.Rand.Float64() < s.DropProb
}
func (s ScriptedFaults) Duplicate(m Message) bool {
	if s.ForceDup[FaultKey(m)] {
		return true
	}
	return s.DuplicateProb > 0 && s.Rand.Float64() < s.DuplicateProb
}
