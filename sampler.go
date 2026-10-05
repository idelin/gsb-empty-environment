// Package wsample implements weighted sampling without replacement over a set
// whose weights are mutable non-negative integers.
//
// Design guarantees:
//   - Random bytes are supplied by the caller through a Stream, so runs are
//     replayable and the stream position is observable.
//   - Uniform values below the current weight total are produced by rejection
//     sampling; no modulo is ever applied, so there is no modulo bias.
//   - Items with weight zero are never drawn.
//   - Weight totals are accumulated in big.Int, so sums of large weights
//     cannot overflow.
//   - A draw can be cancelled between items; every drawn item is committed
//     atomically (removed from the set, stream advanced) and is never rolled
//     back. Snapshot/Restore persists the set state together with the exact
//     random-stream position so an interrupted draw can be resumed later.
package wsample

import (
	"errors"
	"fmt"
	"math/big"
)

var (
	// ErrCancelled is returned by DrawK when the cancel function reports
	// cancellation. Items already drawn stay drawn.
	ErrCancelled = errors.New("wsample: draw cancelled")
	// ErrNoPositiveWeight is returned when no remaining item has positive weight.
	ErrNoPositiveWeight = errors.New("wsample: no item with positive weight")
	// ErrNotEnoughItems is returned by DrawK when fewer than k items with
	// positive weight remain. It is reported before anything is drawn.
	ErrNotEnoughItems = errors.New("wsample: not enough items with positive weight")
	// ErrItemRemoved is returned when mutating an item already drawn/removed.
	ErrItemRemoved = errors.New("wsample: item already removed")
	// ErrStreamPosition is returned by Restore when the supplied stream is not
	// positioned at the snapshot's recorded offset.
	ErrStreamPosition = errors.New("wsample: stream position does not match snapshot")
)

// Sampler holds the mutable weighted set and the random stream.
type Sampler struct {
	weights  []uint64
	alive    []bool
	total    big.Int // sum of weights of alive items; never overflows
	positive int     // number of alive items with weight > 0
	stream   Stream
}

// New creates a Sampler over len(weights) items. Item i initially has weight
// weights[i]; every item starts alive. The stream is consumed lazily as draws
// happen.
func New(weights []uint64, stream Stream) *Sampler {
	s := &Sampler{
		weights: append([]uint64(nil), weights...),
		alive:   make([]bool, len(weights)),
		stream:  stream,
	}
	for i, w := range weights {
		s.alive[i] = true
		if w > 0 {
			s.positive++
		}
		s.total.Add(&s.total, new(big.Int).SetUint64(w))
	}
	return s
}

// Len returns the number of items in the set (alive or removed).
func (s *Sampler) Len() int { return len(s.weights) }

// Total returns the current sum of weights of alive items.
func (s *Sampler) Total() *big.Int { return new(big.Int).Set(&s.total) }

// Remaining returns the number of alive items with positive weight, i.e. the
// maximum number of items that can still be drawn.
func (s *Sampler) Remaining() int { return s.positive }

// Weight returns the current weight of item i.
func (s *Sampler) Weight(i int) uint64 { return s.weights[i] }

// Alive reports whether item i is still in the set.
func (s *Sampler) Alive(i int) bool { return s.alive[i] }

// SetWeight changes the weight of an alive item. Zero is allowed; a
// zero-weight item is never drawn. Weights of removed items cannot be
// changed.
func (s *Sampler) SetWeight(i int, w uint64) error {
	if i < 0 || i >= len(s.weights) {
		return fmt.Errorf("wsample: item index %d out of range [0, %d)", i, len(s.weights))
	}
	if !s.alive[i] {
		return ErrItemRemoved
	}
	old := s.weights[i]
	if old > 0 {
		s.positive--
	}
	if w > 0 {
		s.positive++
	}
	s.weights[i] = w
	s.total.Sub(&s.total, new(big.Int).SetUint64(old))
	s.total.Add(&s.total, new(big.Int).SetUint64(w))
	return nil
}

// DrawOne draws a single item with probability proportional to its weight,
// removes it from the set, and returns its index. The draw is atomic: on
// success the item is removed and the stream has advanced; on error nothing
// changes. Items with weight zero are never selected.
func (s *Sampler) DrawOne() (int, error) {
	if s.total.Sign() == 0 {
		return -1, ErrNoPositiveWeight
	}
	v, err := uniformBelow(s.stream, &s.total)
	if err != nil {
		return -1, err
	}
	acc := new(big.Int)
	for i := range s.weights {
		if !s.alive[i] || s.weights[i] == 0 {
			continue
		}
		acc.Add(acc, new(big.Int).SetUint64(s.weights[i]))
		if v.Cmp(acc) < 0 {
			s.alive[i] = false
			s.positive--
			s.total.Sub(&s.total, new(big.Int).SetUint64(s.weights[i]))
			return i, nil
		}
	}
	// Unreachable: v < total guarantees a hit above.
	return -1, errors.New("wsample: internal error: draw fell through cumulative weights")
}

// DrawK draws k items without replacement. If cancel is non-nil it is
// consulted before each individual draw; when it reports true, DrawK returns
// the items drawn so far together with ErrCancelled.
//
// There is no half-drawn, half-rolled-back state: each item is committed the
// moment it is drawn, so after a cancellation the sampler (weights, removed
// set, stream position) is exactly the state from which a later DrawK call —
// possibly on a Sampler rebuilt via Snapshot/Restore — continues.
//
// If fewer than k items with positive weight remain, ErrNotEnoughItems is
// returned before anything is drawn.
func (s *Sampler) DrawK(k int, cancel func() bool) ([]int, error) {
	if k < 0 {
		return nil, fmt.Errorf("wsample: negative k %d", k)
	}
	if k > s.positive {
		return nil, ErrNotEnoughItems
	}
	out := make([]int, 0, k)
	for len(out) < k {
		if cancel != nil && cancel() {
			return out, ErrCancelled
		}
		i, err := s.DrawOne()
		if err != nil {
			return out, err
		}
		out = append(out, i)
	}
	return out, nil
}

// Snapshot captures the full sampler state: item weights, the alive/removed
// set, and the exact position of the random stream. It contains everything
// needed to resume later; it is safe to serialize (all fields are exported
// value data).
type Snapshot struct {
	Weights   []uint64
	Alive     []bool
	StreamPos uint64
}

// Snapshot returns a snapshot of the current state.
func (s *Sampler) Snapshot() Snapshot {
	return Snapshot{
		Weights:   append([]uint64(nil), s.weights...),
		Alive:     append([]bool(nil), s.alive...),
		StreamPos: s.stream.Pos(),
	}
}

// Restore rebuilds a Sampler from a snapshot. The supplied stream must be
// positioned exactly at the snapshot's recorded offset (for *BytesStream use
// Seek); otherwise ErrStreamPosition is returned. Feeding the restored
// sampler the same subsequent bytes as the original run reproduces the
// original draws exactly.
func Restore(snap Snapshot, stream Stream) (*Sampler, error) {
	if len(snap.Weights) != len(snap.Alive) {
		return nil, fmt.Errorf("wsample: corrupt snapshot: %d weights vs %d alive flags",
			len(snap.Weights), len(snap.Alive))
	}
	if stream.Pos() != snap.StreamPos {
		return nil, fmt.Errorf("%w: stream at %d, snapshot recorded %d",
			ErrStreamPosition, stream.Pos(), snap.StreamPos)
	}
	s := &Sampler{
		weights: append([]uint64(nil), snap.Weights...),
		alive:   append([]bool(nil), snap.Alive...),
		stream:  stream,
	}
	for i, w := range s.weights {
		if s.alive[i] {
			if w > 0 {
				s.positive++
			}
			s.total.Add(&s.total, new(big.Int).SetUint64(w))
		}
	}
	return s, nil
}
