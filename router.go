// Package router delivers messages to subscribers by topic prefix.
//
// Subscription changes are committed as atomic batches. Each commit
// produces an immutable snapshot identified by a monotonically
// increasing sequence number. A message is always routed against
// exactly one snapshot, selected by its commit sequence, so a batch
// update is never observed half-applied.
package router

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

// Subscription binds a subscriber to a topic prefix.
type Subscription struct {
	ID         uint64
	Subscriber string
	Prefix     string
}

// Update is a batch of subscription changes committed atomically.
// Removals are applied before additions, so a remove plus an add of
// the same ID acts as a replace.
type Update struct {
	Add    []Subscription
	Remove []uint64
}

// Delivery is one copy of a message addressed to one subscriber.
type Delivery struct {
	Seq        uint64
	Subscriber string
	Topic      string
	Message    interface{}
}

// ErrUnknownSeq is returned when routing against a commit sequence
// whose snapshot has been discarded.
var ErrUnknownSeq = errors.New("router: unknown commit sequence")

// snapshot is an immutable routing table for one commit sequence.
type snapshot struct {
	subs  map[uint64]Subscription
	index map[string][]uint64 // prefix -> subscription IDs
}

// match returns the deduplicated, sorted set of subscribers whose
// prefixes match topic. A subscriber holding several matching
// subscriptions (duplicate or overlapping prefixes) appears once.
func (s *snapshot) match(topic string) []string {
	seen := make(map[string]struct{})
	var out []string
	for prefix, ids := range s.index {
		if !strings.HasPrefix(topic, prefix) {
			continue
		}
		for _, id := range ids {
			name := s.subs[id].Subscriber
			if _, dup := seen[name]; !dup {
				seen[name] = struct{}{}
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Router commits batch updates and routes messages against snapshots.
type Router struct {
	mu    sync.RWMutex
	seq   uint64
	snaps map[uint64]*snapshot
}

// New returns a Router with an empty table committed at sequence 0.
func New() *Router {
	return &Router{
		snaps: map[uint64]*snapshot{
			0: {subs: map[uint64]Subscription{}, index: map[string][]uint64{}},
		},
	}
}

// Apply commits a batch update atomically and returns the new commit
// sequence. The previous snapshot is left untouched.
func (r *Router) Apply(u Update) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	prev := r.snaps[r.seq]
	subs := make(map[uint64]Subscription, len(prev.subs)+len(u.Add))
	for id, s := range prev.subs {
		subs[id] = s
	}
	for _, id := range u.Remove {
		delete(subs, id)
	}
	for _, s := range u.Add {
		subs[s.ID] = s
	}
	index := make(map[string][]uint64, len(subs))
	for id, s := range subs {
		index[s.Prefix] = append(index[s.Prefix], id)
	}

	r.seq++
	r.snaps[r.seq] = &snapshot{subs: subs, index: index}
	return r.seq
}

// Seq returns the latest commit sequence.
func (r *Router) Seq() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.seq
}

// Publish routes msg to subscribers using the snapshot committed at
// seq. The whole recipient set comes from that single snapshot, so a
// message never sees a mix of old and new table entries.
func (r *Router) Publish(seq uint64, topic string, msg interface{}) ([]Delivery, error) {
	r.mu.RLock()
	snap, ok := r.snaps[seq]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrUnknownSeq
	}
	subs := snap.match(topic)
	out := make([]Delivery, len(subs))
	for i, name := range subs {
		out[i] = Delivery{Seq: seq, Subscriber: name, Topic: topic, Message: msg}
	}
	return out, nil
}

// PublishNow routes msg using the latest committed snapshot.
func (r *Router) PublishNow(topic string, msg interface{}) []Delivery {
	out, _ := r.Publish(r.Seq(), topic, msg)
	return out
}

// Retain discards snapshots with sequence below minSeq, except the
// latest snapshot which is always kept.
func (r *Router) Retain(minSeq uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for seq := range r.snaps {
		if seq < minSeq && seq != r.seq {
			delete(r.snaps, seq)
		}
	}
}
