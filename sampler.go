// Package wsample implements streaming weighted random sampling without
// replacement. Items carry positive integer weights and can be added and
// removed over time; Sample returns k distinct items where each draw
// picks an item with probability proportional to its weight relative to
// the total weight of the items still remaining (successive sampling).
package wsample

import (
	"errors"
	"fmt"
	"sort"
)

// Op identifies the kind of a stream Event.
type Op int

const (
	// OpAdd inserts an item with a positive integer weight.
	OpAdd Op = iota
	// OpRemove revokes a previously added item, removing its full weight.
	OpRemove
)

// Event is a single mutation in the item stream.
type Event struct {
	Op     Op
	ID     string
	Weight int64
}

var (
	ErrNonPositiveWeight = errors.New("wsample: weight must be a positive integer")
	ErrDuplicateID       = errors.New("wsample: duplicate id")
	ErrUnknownID         = errors.New("wsample: unknown id")
	ErrNegativeK         = errors.New("wsample: k must be non-negative")
	ErrTooManyRequested  = errors.New("wsample: k exceeds number of items")
)

// Sampler maintains a finite set of weighted items and draws samples
// without replacement using an injectable random Source.
type Sampler struct {
	src     Source
	weights map[string]int64
	total   int64
}

// New returns a Sampler drawing randomness from src. If src is nil a
// Splitmix64 source seeded with 0 is used.
func New(src Source) *Sampler {
	if src == nil {
		src = NewSplitmix64(0)
	}
	return &Sampler{src: src, weights: make(map[string]int64)}
}

// Apply applies a single stream event to the sampler.
func (s *Sampler) Apply(ev Event) error {
	switch ev.Op {
	case OpAdd:
		return s.Add(ev.ID, ev.Weight)
	case OpRemove:
		return s.Remove(ev.ID)
	default:
		return fmt.Errorf("wsample: unknown op %d", int(ev.Op))
	}
}

// Add inserts id with the given positive integer weight.
func (s *Sampler) Add(id string, weight int64) error {
	if weight <= 0 {
		return ErrNonPositiveWeight
	}
	if _, ok := s.weights[id]; ok {
		return ErrDuplicateID
	}
	s.weights[id] = weight
	s.total += weight
	return nil
}

// Remove revokes id, removing its full weight from the sampler so no
// residual weight is left behind.
func (s *Sampler) Remove(id string) error {
	w, ok := s.weights[id]
	if !ok {
		return ErrUnknownID
	}
	delete(s.weights, id)
	s.total -= w
	return nil
}

// Len returns the number of items currently in the sampler.
func (s *Sampler) Len() int {
	return len(s.weights)
}

// Total returns the sum of the weights of all current items.
func (s *Sampler) Total() int64 {
	return s.total
}

// Weight returns the weight of id and whether it is present.
func (s *Sampler) Weight(id string) (int64, bool) {
	w, ok := s.weights[id]
	return w, ok
}

// Sample draws k distinct items. At each step an item is chosen with
// probability w_i / W where W is the total weight of the items not yet
// chosen in this call. The sampler's item set is not modified.
func (s *Sampler) Sample(k int) ([]string, error) {
	if k < 0 {
		return nil, ErrNegativeK
	}
	if k > len(s.weights) {
		return nil, ErrTooManyRequested
	}
	// Snapshot in sorted order so the outcome depends only on the
	// random source, never on Go's map iteration order.
	ids := make([]string, 0, len(s.weights))
	for id := range s.weights {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	ws := make([]int64, len(ids))
	for i, id := range ids {
		ws[i] = s.weights[id]
	}

	total := s.total
	out := make([]string, 0, k)
	for len(out) < k {
		r := s.src.Intn(total)
		idx := 0
		var acc int64
		for i, w := range ws {
			acc += w
			if r < acc {
				idx = i
				break
			}
		}
		out = append(out, ids[idx])
		total -= ws[idx]
		last := len(ids) - 1
		ids[idx], ws[idx] = ids[last], ws[last]
		ids, ws = ids[:last], ws[:last]
	}
	return out, nil
}
