// Package router implements topic-prefix message routing with atomic
// batch subscription updates.
//
// Subscribers register interest in topic prefixes. A published message
// is delivered to every subscriber whose prefix matches the message
// topic, but at most once per subscriber per message, even if several
// of that subscriber's prefixes match or the same prefix was
// subscribed more than once.
//
// Subscription changes are applied in batches. Each batch produces a
// new immutable snapshot of the subscription table, identified by a
// monotonically increasing commit sequence number. A published message
// is always routed by exactly one snapshot (old or new), never by a
// mixture of two.
package router

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Message is a single published message.
type Message struct {
	Topic string
	Body  []byte
}

// Delivery is a message routed to a subscriber. Seq is the commit
// sequence number of the subscription-table snapshot that routed it.
type Delivery struct {
	Message Message
	Seq     uint64
}

// Subscriber receives routed messages on C.
type Subscriber struct {
	ID string
	C  <-chan Delivery

	ch chan Delivery
}

// Op is a single subscription change inside a batch.
type Op struct {
	Sub       string
	Prefix    string
	Subscribe bool // false means unsubscribe
}

// SubscribeOp returns an Op subscribing sub to prefix.
func SubscribeOp(sub, prefix string) Op { return Op{Sub: sub, Prefix: prefix, Subscribe: true} }

// UnsubscribeOp returns an Op unsubscribing sub from prefix.
func UnsubscribeOp(sub, prefix string) Op { return Op{Sub: sub, Prefix: prefix, Subscribe: false} }

// entry is one (prefix, subscriber) pair in a snapshot.
type entry struct {
	prefix string
	sub    string
}

// table is an immutable subscription snapshot.
type table struct {
	seq     uint64
	bySub   map[string]map[string]bool // subscriber -> set of prefixes
	entries []entry                    // flattened, sorted by (prefix, sub)
}

func newTable() *table {
	return &table{bySub: make(map[string]map[string]bool)}
}

func (t *table) clone() *table {
	nt := &table{bySub: make(map[string]map[string]bool, len(t.bySub))}
	for sub, prefixes := range t.bySub {
		ps := make(map[string]bool, len(prefixes))
		for p := range prefixes {
			ps[p] = true
		}
		nt.bySub[sub] = ps
	}
	return nt
}

// apply mutates the (already cloned) table with a batch of ops.
// Subscribing an already-present prefix is a no-op, so duplicate
// subscriptions collapse to a single entry.
func (t *table) apply(ops []Op) {
	for _, op := range ops {
		prefixes, ok := t.bySub[op.Sub]
		if op.Subscribe {
			if !ok {
				prefixes = make(map[string]bool)
				t.bySub[op.Sub] = prefixes
			}
			prefixes[op.Prefix] = true
			continue
		}
		if ok {
			delete(prefixes, op.Prefix)
			if len(prefixes) == 0 {
				delete(t.bySub, op.Sub)
			}
		}
	}
}

func (t *table) rebuild() {
	t.entries = t.entries[:0]
	for sub, prefixes := range t.bySub {
		for p := range prefixes {
			t.entries = append(t.entries, entry{prefix: p, sub: sub})
		}
	}
	sort.Slice(t.entries, func(i, j int) bool {
		if t.entries[i].prefix != t.entries[j].prefix {
			return t.entries[i].prefix < t.entries[j].prefix
		}
		return t.entries[i].sub < t.entries[j].sub
	})
}

// match returns the deduplicated, sorted IDs of subscribers with at
// least one prefix matching topic.
func (t *table) match(topic string) []string {
	var ids []string
	seen := make(map[string]bool)
	for _, e := range t.entries {
		if !seen[e.sub] && strings.HasPrefix(topic, e.prefix) {
			seen[e.sub] = true
			ids = append(ids, e.sub)
		}
	}
	sort.Strings(ids)
	return ids
}

// Router routes published messages to subscribers by topic prefix.
type Router struct {
	mu   sync.Mutex // serializes Apply/RemoveSubscriber
	seq  uint64     // last committed sequence number; atomic access
	snap atomic.Value

	subsMu sync.RWMutex
	subs   map[string]*Subscriber
}

// New returns an empty Router.
func New() *Router {
	r := &Router{subs: make(map[string]*Subscriber)}
	r.snap.Store(newTable())
	return r
}

// Seq returns the commit sequence number of the current snapshot.
func (r *Router) Seq() uint64 { return atomic.LoadUint64(&r.seq) }

// AddSubscriber registers a subscriber whose deliveries are buffered
// up to buf messages. Adding an existing ID returns the existing
// subscriber.
func (r *Router) AddSubscriber(id string, buf int) *Subscriber {
	r.subsMu.Lock()
	defer r.subsMu.Unlock()
	if s, ok := r.subs[id]; ok {
		return s
	}
	ch := make(chan Delivery, buf)
	s := &Subscriber{ID: id, C: ch, ch: ch}
	r.subs[id] = s
	return s
}

// RemoveSubscriber unregisters a subscriber and atomically purges all
// of its subscriptions. It returns the commit sequence number of the
// purge, or the current sequence if the subscriber was unknown.
func (r *Router) RemoveSubscriber(id string) uint64 {
	r.subsMu.Lock()
	delete(r.subs, id)
	r.subsMu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	old := r.snap.Load().(*table)
	prefixes, ok := old.bySub[id]
	if !ok {
		return old.seq
	}
	ops := make([]Op, 0, len(prefixes))
	for p := range prefixes {
		ops = append(ops, UnsubscribeOp(id, p))
	}
	return r.applyLocked(ops)
}

// Apply commits a batch of subscription ops atomically and returns the
// new commit sequence number. Messages published after Apply returns
// are routed by the new snapshot; messages routed concurrently use
// exactly one of the old or new snapshots in full.
func (r *Router) Apply(ops ...Op) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.applyLocked(ops)
}

func (r *Router) applyLocked(ops []Op) uint64 {
	if len(ops) == 0 {
		return atomic.LoadUint64(&r.seq)
	}
	nt := r.snap.Load().(*table).clone()
	nt.apply(ops)
	nt.rebuild()
	nt.seq = atomic.AddUint64(&r.seq, 1)
	r.snap.Store(nt)
	return nt.seq
}

// Publish routes one message to every matching subscriber, at most
// once each. The whole message is routed by the single snapshot loaded
// at publish time; its commit sequence number is returned and also
// stamped on every resulting Delivery.
func (r *Router) Publish(topic string, body []byte) uint64 {
	t := r.snap.Load().(*table)
	msg := Message{Topic: topic, Body: body}
	for _, id := range t.match(topic) {
		r.subsMu.RLock()
		sub := r.subs[id]
		r.subsMu.RUnlock()
		if sub != nil {
			sub.ch <- Delivery{Message: msg, Seq: t.seq}
		}
	}
	return t.seq
}
