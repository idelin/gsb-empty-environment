// Package causal implements a causal delivery library.
//
// Each message carries the sequence numbers its sender had observed from
// every participant (a vector clock). A message is delivered only after all
// of its causal predecessors have been delivered:
//
//   - every earlier message from the same sender, and
//   - for every other participant t, all messages from t up to the sequence
//     number recorded in the message's vector.
//
// Pending messages are kept in a dependency index so that delivering one
// message wakes up exactly the messages that were waiting on it, without
// rescanning the whole pending set.
package causal

import "sort"

// Message is a unit of causal broadcast.
type Message struct {
	ID     string           // globally unique message identifier
	Sender string           // sending participant
	Seq    uint64           // per-sender sequence number, starting at 1
	Vector map[string]uint64 // observed sequence number per participant at send time
}

// Gap describes a contiguous range of missing predecessor messages from one
// sender: messages Sender#From .. Sender#To (inclusive) have not been
// delivered yet.
type Gap struct {
	Sender string
	From   uint64
	To     uint64
}

// depKey identifies a single message position: Sender#Seq.
type depKey struct {
	sender string
	seq    uint64
}

// Causal tracks delivered sequence numbers and indexes pending messages by
// the exact predecessor positions they wait on.
type Causal struct {
	delivered map[string]uint64 // highest contiguous delivered seq per sender
	seen      map[string]struct{} // IDs already delivered or pending (dedup)
	pending   map[string]Message  // msgID -> buffered message
	waiters   map[depKey]map[string]struct{} // dep position -> waiting msgIDs
	waitingOn map[string]map[depKey]struct{} // msgID -> remaining dep positions
}

// New returns an empty causal delivery tracker.
func New() *Causal {
	return &Causal{
		delivered: make(map[string]uint64),
		seen:      make(map[string]struct{}),
		pending:   make(map[string]Message),
		waiters:   make(map[depKey]map[string]struct{}),
		waitingOn: make(map[string]map[depKey]struct{}),
	}
}

// Submit ingests a message.
//
// If the message is a duplicate (same ID seen before, or its sender position
// already delivered), it returns duplicate=true and changes no state, so a
// duplicate never triggers spurious wakeups.
//
// If predecessors are missing, the message is buffered and the exact gaps
// are returned. Otherwise the message — and any pending messages it unblocks,
// recursively — are delivered in causal order.
func (c *Causal) Submit(m Message) (delivered []Message, gaps []Gap, duplicate bool) {
	if _, ok := c.seen[m.ID]; ok {
		return nil, nil, true
	}
	if m.Seq <= c.delivered[m.Sender] {
		// Position already covered by an earlier delivery.
		c.seen[m.ID] = struct{}{}
		return nil, nil, true
	}
	if gaps = c.gapsFor(m); len(gaps) > 0 {
		c.pend(m, gaps)
		return nil, gaps, false
	}
	c.deliver(m, &delivered)
	return delivered, nil, false
}

// Pending reports how many messages are currently buffered.
func (c *Causal) Pending() int { return len(c.pending) }

// Delivered returns the highest contiguous delivered sequence per sender.
func (c *Causal) Delivered() map[string]uint64 {
	out := make(map[string]uint64, len(c.delivered))
	for s, n := range c.delivered {
		out[s] = n
	}
	return out
}

// gapsFor computes the exact missing predecessor ranges for m. Because
// per-sender delivery is contiguous, each sender contributes at most one
// range, and waiting on the range's upper bound is sufficient.
func (c *Causal) gapsFor(m Message) []Gap {
	var gaps []Gap
	if next := c.delivered[m.Sender] + 1; m.Seq > next {
		gaps = append(gaps, Gap{Sender: m.Sender, From: next, To: m.Seq - 1})
	}
	for sender, seq := range m.Vector {
		if sender == m.Sender {
			continue
		}
		if seq > c.delivered[sender] {
			gaps = append(gaps, Gap{Sender: sender, From: c.delivered[sender] + 1, To: seq})
		}
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i].Sender < gaps[j].Sender })
	return gaps
}

// pend buffers m and registers it under each missing predecessor position.
// Only the upper bound of each gap is indexed: delivery per sender is
// contiguous, so reaching position To implies the whole range was delivered.
func (c *Causal) pend(m Message, gaps []Gap) {
	c.seen[m.ID] = struct{}{}
	c.pending[m.ID] = m
	deps := make(map[depKey]struct{}, len(gaps))
	for _, g := range gaps {
		k := depKey{sender: g.Sender, seq: g.To}
		deps[k] = struct{}{}
		set := c.waiters[k]
		if set == nil {
			set = make(map[string]struct{})
			c.waiters[k] = set
		}
		set[m.ID] = struct{}{}
	}
	c.waitingOn[m.ID] = deps
}

// deliver marks m delivered and wakes exactly the messages waiting on its
// position, cascading in causal order.
func (c *Causal) deliver(m Message, out *[]Message) {
	c.seen[m.ID] = struct{}{}
	c.delivered[m.Sender] = m.Seq
	*out = append(*out, m)

	key := depKey{sender: m.Sender, seq: m.Seq}
	for id := range c.waiters[key] {
		deps := c.waitingOn[id]
		delete(deps, key)
		if len(deps) == 0 {
			delete(c.waitingOn, id)
			pm := c.pending[id]
			delete(c.pending, id)
			c.deliver(pm, out)
		}
	}
	delete(c.waiters, key)
}
