package netsec

import "sync"

// Bus is the broadcast abstraction used by the simulated parties. A test can
// supply its own Bus to drop messages, duplicate them, or fork values.
type Bus interface {
	Publish(SignedMessage) error
	Messages(round RoundID, pair TriplePair, label string) []SignedMessage
}

// LoopbackBus is the honest in-process broadcast channel.
type LoopbackBus struct {
	mu   sync.Mutex
	msgs map[busKey][]SignedMessage
}

type busKey struct {
	round RoundID
	pair  TriplePair
	label string
}

// NewLoopbackBus constructs an empty in-process bus.
func NewLoopbackBus() *LoopbackBus {
	return &LoopbackBus{msgs: make(map[busKey][]SignedMessage)}
}

// Publish stores a copy of a broadcast.
func (b *LoopbackBus) Publish(m SignedMessage) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	k := busKey{round: m.Round, pair: m.Pair, label: m.Label}
	b.msgs[k] = append(b.msgs[k], cloneMessage(m))
	return nil
}

// Messages returns copies of all broadcasts in one context.
func (b *LoopbackBus) Messages(round RoundID, pair TriplePair, label string) []SignedMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	src := b.msgs[busKey{round: round, pair: pair, label: label}]
	out := make([]SignedMessage, len(src))
	for i, m := range src {
		out[i] = cloneMessage(m)
	}
	return out
}

// All returns a copy of every message, useful for auditing tests.
func (b *LoopbackBus) All() []SignedMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []SignedMessage
	for _, msgs := range b.msgs {
		for _, m := range msgs {
			out = append(out, cloneMessage(m))
		}
	}
	return out
}
