// Package wsample implements weighted random sampling without replacement
// over a mutable set of items whose weights are non-negative integers.
//
// Weights and weight sums are arbitrary-precision integers (math/big), so
// there is no overflow and no floating-point rounding in the distribution.
// Uniform values in [0, total) are produced by rejection sampling over whole
// bytes, never by modular reduction, so there is no modulo bias. Items with
// zero weight are never selected.
//
// The caller supplies the random byte stream through a Source. A draw is
// transactional: if the Source reports an error (for example a cancellation)
// partway through Draw, the item set is left untouched and every random byte
// consumed so far is buffered inside the Sampler. A later Draw call replays
// those buffered bytes before reading new ones, so the random stream resumes
// from exactly the position where the cancelled draw started. A draw never
// returns a partial result and never leaves the set half-modified.
package wsample

import (
	"errors"
	"io"
	"math/big"
)

// ErrTooFewPositiveItems is returned by Draw when k exceeds the number of
// items with positive weight.
var ErrTooFewPositiveItems = errors.New("wsample: k exceeds the number of positive-weight items")

// ErrNegativeWeight is returned when a negative weight is supplied.
var ErrNegativeWeight = errors.New("wsample: weight must be non-negative")

// Source supplies the random bytes that drive sampling.
//
// Fill must either fill buf completely and return nil, or return an error.
// If Fill returns an error, any bytes it may have written are treated as not
// consumed: the Sampler will not count them as part of the random stream.
// Returning an error cancels the in-progress draw; the draw can be retried
// later and will resume the stream from the same logical position.
type Source interface {
	Fill(buf []byte) error
}

// SourceFunc adapts a function to a Source.
type SourceFunc func(buf []byte) error

// Fill calls f(buf).
func (f SourceFunc) Fill(buf []byte) error { return f(buf) }

// ReaderSource adapts an io.Reader to a Source. Note that io.ReadFull may
// consume a partial buffer before reporting an error; for exact stream
// resume after cancellation prefer a Source with transactional Fill
// semantics.
type ReaderSource struct {
	R io.Reader
}

// Fill reads len(buf) bytes from the underlying reader.
func (s ReaderSource) Fill(buf []byte) error {
	_, err := io.ReadFull(s.R, buf)
	return err
}

// Sampler is a mutable weighted set plus the state of the random stream.
// It is not safe for concurrent use.
type Sampler struct {
	src     Source
	weights []*big.Int
	total   *big.Int
	// pending holds random bytes consumed by a draw that did not complete.
	// They are replayed at the start of the next Draw so the logical stream
	// position is preserved across cancellation.
	pending []byte
}

// New returns an empty Sampler driven by src. It panics if src is nil.
func New(src Source) *Sampler {
	if src == nil {
		panic("wsample: nil Source")
	}
	return &Sampler{src: src, total: new(big.Int)}
}

// Len returns the number of items in the set.
func (s *Sampler) Len() int { return len(s.weights) }

// Add appends an item with the given weight and returns its index.
func (s *Sampler) Add(weight *big.Int) (int, error) {
	w, err := checkedWeight(weight)
	if err != nil {
		return -1, err
	}
	s.weights = append(s.weights, w)
	s.total.Add(s.total, w)
	return len(s.weights) - 1, nil
}

// SetWeight replaces the weight of item i.
func (s *Sampler) SetWeight(i int, weight *big.Int) error {
	if i < 0 || i >= len(s.weights) {
		return errors.New("wsample: index out of range")
	}
	w, err := checkedWeight(weight)
	if err != nil {
		return err
	}
	s.total.Sub(s.total, s.weights[i])
	s.total.Add(s.total, w)
	s.weights[i] = w
	return nil
}

// Weight returns a copy of the weight of item i.
func (s *Sampler) Weight(i int) *big.Int {
	return new(big.Int).Set(s.weights[i])
}

// Total returns a copy of the current sum of all weights.
func (s *Sampler) Total() *big.Int {
	return new(big.Int).Set(s.total)
}

func checkedWeight(w *big.Int) (*big.Int, error) {
	if w == nil {
		return nil, errors.New("wsample: nil weight")
	}
	if w.Sign() < 0 {
		return nil, ErrNegativeWeight
	}
	return new(big.Int).Set(w), nil
}

// Draw selects k distinct items with probability proportional to their
// current weights (sequential "draw, remove, renormalize" semantics) and
// returns their indices in selection order.
//
// Draw never modifies the item set. If the Source returns an error the draw
// is abandoned: no indices are returned, the set is unchanged, and the
// random bytes already consumed are kept so that a subsequent Draw resumes
// the stream at the same position.
func (s *Sampler) Draw(k int) ([]int, error) {
	if k < 0 {
		return nil, errors.New("wsample: negative k")
	}
	if k == 0 {
		return []int{}, nil
	}
	positive := 0
	for _, w := range s.weights {
		if w.Sign() > 0 {
			positive++
		}
	}
	if k > positive {
		return nil, ErrTooFewPositiveItems
	}

	// Work on copies so the set is never half-modified.
	remaining := make([]*big.Int, len(s.weights))
	for i, w := range s.weights {
		remaining[i] = new(big.Int).Set(w)
	}
	total := new(big.Int).Set(s.total)

	// Bytes left over from a previously cancelled draw are replayed first;
	// bytes consumed by this draw are collected locally and only moved into
	// s.pending if the draw fails, so a successful draw commits the stream
	// position and a failed one restores it.
	cursor := 0
	var consumed []byte
	read := func(dst []byte) error {
		for len(dst) > 0 {
			if cursor < len(s.pending) {
				n := copy(dst, s.pending[cursor:])
				cursor += n
				dst = dst[n:]
				continue
			}
			if err := s.src.Fill(dst); err != nil {
				return err
			}
			consumed = append(consumed, dst...)
			dst = nil
		}
		return nil
	}

	out := make([]int, 0, k)
	acc := new(big.Int)
	for len(out) < k {
		r, err := uniformBelow(total, read)
		if err != nil {
			s.pending = append(s.pending, consumed...)
			return nil, err
		}
		acc.SetInt64(0)
		for i, w := range remaining {
			if w.Sign() == 0 {
				continue
			}
			acc.Add(acc, w)
			if r.Cmp(acc) < 0 {
				out = append(out, i)
				total.Sub(total, w)
				remaining[i].SetInt64(0)
				break
			}
		}
	}

	s.pending = s.pending[:0]
	return out, nil
}

// uniformBelow returns a uniform random value in [0, total) using rejection
// sampling over whole bytes: it reads ceil(bitlen(total)/8) bytes, masks off
// the spare high bits, and retries while the value is >= total. Acceptance
// probability is always >= 1/2 and there is no modulo bias.
func uniformBelow(total *big.Int, read func([]byte) error) (*big.Int, error) {
	bits := uint(total.BitLen())
	nbytes := int((bits + 7) / 8)
	mask := byte(0xFF >> (uint(nbytes)*8 - bits))
	buf := make([]byte, nbytes)
	for {
		if err := read(buf); err != nil {
			return nil, err
		}
		buf[0] &= mask
		if r := new(big.Int).SetBytes(buf); r.Cmp(total) < 0 {
			return r, nil
		}
	}
}
