// Package freq implements order-0 and order-1 (conditional) symbol
// frequency models with incremental add/retract, explicit add-k
// smoothing, entropy estimation, and MDL-based structure selection.
//
// Conventions used across the package:
//
//   - Entropy() is the maximum-likelihood (plug-in) estimate in bits.
//     Zero-frequency symbols are skipped explicitly: they contribute 0
//     to the sum and log2(0) is never evaluated.
//   - Prob() applies add-k smoothing, so every symbol in the declared
//     alphabet has a strictly positive probability. This is the
//     explicit escape strategy for zero-frequency / unknown symbols:
//     they are coded with probability K/(total+K*alphabet).
//   - Entropy and smoothed code lengths are floating point ESTIMATES.
//     They are never used as a substitute for real encoded byte
//     lengths (see package codec for the actual byte format).
package freq

import (
	"errors"
	"math"
	"sort"
)

// ErrNotPresent is returned when retracting a sample that was never
// added (or that has already been fully retracted).
var ErrNotPresent = errors.New("freq: cannot retract a sample that is not present")

// DefaultK is the default add-k smoothing constant
// (Krichevsky-Trofimov rule, k = 1/2). Laplace smoothing is k = 1.
// The smoothing rule is explicit: p(s) = (count(s)+K) / (total+K*alpha).
const DefaultK = 0.5

// Model is an order-0 symbol frequency model with incremental
// add/retract support.
type Model struct {
	counts map[byte]uint64
	total  uint64
	// K is the add-k smoothing constant. Must be > 0 so that unseen
	// symbols keep a nonzero probability (explicit escape strategy).
	K float64
	// Alpha is the declared alphabet size used in the smoothing
	// denominator. Symbols >= Alpha are rejected by the codec.
	Alpha int
}

// NewModel returns an empty order-0 model for an alphabet of the
// given size, using DefaultK smoothing.
func NewModel(alphabetSize int) *Model {
	if alphabetSize < 1 {
		alphabetSize = 1
	}
	if alphabetSize > 256 {
		alphabetSize = 256
	}
	return &Model{counts: make(map[byte]uint64), K: DefaultK, Alpha: alphabetSize}
}

// Add records one occurrence of sym.
func (m *Model) Add(sym byte) {
	m.counts[sym]++
	m.total++
}

// Retract removes one occurrence of sym. Retracting a symbol whose
// count is zero is an error (ErrNotPresent): retractions must match
// prior additions one-for-one.
func (m *Model) Retract(sym byte) error {
	c := m.counts[sym]
	if c == 0 {
		return ErrNotPresent
	}
	if c == 1 {
		delete(m.counts, sym)
	} else {
		m.counts[sym] = c - 1
	}
	m.total--
	return nil
}

// Count returns the current count of sym.
func (m *Model) Count(sym byte) uint64 { return m.counts[sym] }

// Total returns the total number of samples currently held. It always
// equals adds minus retracts (conservation of input records).
func (m *Model) Total() uint64 { return m.total }

// Symbols returns the symbols with nonzero count, sorted.
func (m *Model) Symbols() []byte {
	out := make([]byte, 0, len(m.counts))
	for s := range m.counts {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Entropy returns the plug-in entropy estimate H = -sum p*log2(p) in
// bits per symbol. Zero-frequency symbols are skipped (no log0).
// A deterministic sequence (one distinct symbol) has entropy 0.
func (m *Model) Entropy() float64 {
	if m.total == 0 {
		return 0
	}
	t := float64(m.total)
	h := 0.0
	// Iterate in sorted symbol order so the floating point summation is
	// deterministic and reproducible across runs.
	for _, s := range m.Symbols() {
		c := m.counts[s]
		if c == 0 {
			continue // explicit: zero-frequency symbols contribute 0
		}
		p := float64(c) / t
		h -= p * math.Log2(p)
	}
	return h
}

// Prob returns the add-k smoothed probability of sym. It is strictly
// positive for every symbol in the declared alphabet, including
// zero-frequency (unknown) symbols.
func (m *Model) Prob(sym byte) float64 {
	return (float64(m.counts[sym]) + m.K) / (float64(m.total) + m.K*float64(m.Alpha))
}

// CodeLength returns the smoothed ideal code length of the observed
// sequence in bits. This is a floating point estimate only; the real
// encoded byte length is produced by package codec.
func (m *Model) CodeLength() float64 {
	bits := 0.0
	for s, c := range m.counts {
		if c == 0 {
			continue
		}
		bits += float64(c) * -math.Log2(m.Prob(s))
	}
	return bits
}

// Clone returns a deep copy of the model.
func (m *Model) Clone() *Model {
	cp := &Model{counts: make(map[byte]uint64, len(m.counts)), total: m.total, K: m.K, Alpha: m.Alpha}
	for s, c := range m.counts {
		cp.counts[s] = c
	}
	return cp
}
