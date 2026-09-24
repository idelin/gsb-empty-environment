// Package decayfreq implements a decaying frequency sketch (Count-Min style)
// with sharded updates, lazy epoch-based decay, saturating counters, and an
// admission cache with a byte quota. It depends only on the Go standard
// library. All estimates are probabilistic upper bounds, never exact counts.
package decayfreq

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"sync"
	"sync/atomic"
)

// DefaultSeed is the fixed hash seed. It is a constant so results are
// deterministic across runs and processes.
const DefaultSeed uint64 = 0x9e3779b97f4a7c15

// MaxCount is the saturation ceiling for a single counter. Adds beyond it
// saturate instead of overflowing.
const MaxCount = math.MaxUint32

// Hasher maps (row, key) to a 64-bit hash. It is injectable so tests can
// force collisions.
type Hasher func(row int, key string) uint64

// Option customizes a Sketch.
type Option func(*Sketch)

// WithSeed overrides the hash seed (default DefaultSeed).
func WithSeed(seed uint64) Option {
	return func(s *Sketch) { s.seed = seed }
}

// WithHasher overrides the hash function, e.g. to force collisions in tests.
func WithHasher(h Hasher) Option {
	return func(s *Sketch) { s.hasher = h }
}

// rowShard is one independently locked row of counters.
type rowShard struct {
	mu       sync.Mutex
	counters []uint32
	epochs   []uint32 // per-counter last-applied decay epoch
}

// Sketch is a Count-Min style frequency sketch with lazy decay.
//
// Declared logical order: Decay() halves every counter at the moment of the
// call; Add calls between two decays accumulate into the current epoch. The
// lazy per-counter implementation is exactly equivalent to eagerly halving
// all counters at each Decay() call, including rounding, regardless of how
// adds and decays interleave across shards.
type Sketch struct {
	rows   []rowShard
	mask   uint64
	seed   uint64
	hasher Hasher
	epoch  atomic.Uint32
}

// NewSketch builds a sketch with the given number of rows (shards) and a row
// width rounded up to a power of two.
func NewSketch(rows, width int, opts ...Option) *Sketch {
	if rows < 1 {
		rows = 1
	}
	w := 1
	for w < width {
		w <<= 1
	}
	s := &Sketch{
		rows: make([]rowShard, rows),
		mask: uint64(w - 1),
		seed: DefaultSeed,
	}
	for i := range s.rows {
		s.rows[i].counters = make([]uint32, w)
		s.rows[i].epochs = make([]uint32, w)
	}
	for _, o := range opts {
		o(s)
	}
	if s.hasher == nil {
		s.hasher = defaultHasher(s.seed)
	}
	return s
}

func defaultHasher(seed uint64) Hasher {
	return func(row int, key string) uint64 {
		h := fnv.New64a()
		var buf [16]byte
		binary.LittleEndian.PutUint64(buf[0:8], seed)
		binary.LittleEndian.PutUint64(buf[8:16], uint64(row))
		_, _ = h.Write(buf[:])
		_, _ = h.Write([]byte(key))
		return h.Sum64()
	}
}

func (s *Sketch) index(row int, key string) uint64 {
	return s.hasher(row, key) & s.mask
}

// touchLocked lazily applies pending decays to one counter. It never makes a
// counter negative: shifts saturate at zero.
func (s *Sketch) touchLocked(shard *rowShard, idx uint64) {
	g := s.epoch.Load()
	e := shard.epochs[idx]
	if g == e {
		return
	}
	d := g - e
	if d >= 32 {
		shard.counters[idx] = 0
	} else {
		shard.counters[idx] >>= d
	}
	shard.epochs[idx] = g
}

// Add increments the estimate for key by n, saturating at MaxCount instead of
// overflowing. Each row shard is locked independently.
func (s *Sketch) Add(key string, n uint32) {
	if n == 0 {
		return
	}
	for i := range s.rows {
		shard := &s.rows[i]
		idx := s.index(i, key)
		shard.mu.Lock()
		s.touchLocked(shard, idx)
		c := shard.counters[idx]
		if c > MaxCount-n {
			shard.counters[idx] = MaxCount
		} else {
			shard.counters[idx] = c + n
		}
		shard.mu.Unlock()
	}
}

// Estimate returns an upper-bound estimate of the (decayed) count for key.
// It never underestimates the true decayed count and never goes negative.
func (s *Sketch) Estimate(key string) uint32 {
	min := uint32(MaxCount)
	for i := range s.rows {
		shard := &s.rows[i]
		idx := s.index(i, key)
		shard.mu.Lock()
		s.touchLocked(shard, idx)
		c := shard.counters[idx]
		shard.mu.Unlock()
		if c < min {
			min = c
		}
	}
	return min
}

// Decay advances the decay epoch, logically halving every counter. It is O(1);
// the halving is applied lazily per counter on next access.
func (s *Sketch) Decay() {
	s.epoch.Add(1)
}

// Epoch returns the current decay epoch.
func (s *Sketch) Epoch() uint32 { return s.epoch.Load() }

// SketchState is a serializable snapshot of a Sketch.
type SketchState struct {
	Epoch    uint32
	Width    int
	Counters [][]uint32
	Epochs   [][]uint32
}

// Snapshot captures the full counter state. Pending lazy decays are applied
// to the copy, so the snapshot is self-consistent at the current epoch.
func (s *Sketch) Snapshot() SketchState {
	st := SketchState{
		Epoch:    s.epoch.Load(),
		Width:    len(s.rows[0].counters),
		Counters: make([][]uint32, len(s.rows)),
		Epochs:   make([][]uint32, len(s.rows)),
	}
	for i := range s.rows {
		shard := &s.rows[i]
		shard.mu.Lock()
		counters := make([]uint32, len(shard.counters))
		epochs := make([]uint32, len(shard.epochs))
		for j := range counters {
			s.touchLocked(shard, uint64(j))
			counters[j] = shard.counters[j]
			epochs[j] = shard.epochs[j]
		}
		st.Counters[i] = counters
		st.Epochs[i] = epochs
		shard.mu.Unlock()
	}
	return st
}

// RestoreSketch rebuilds a Sketch from a snapshot. Options (seed, hasher)
// must match the original for estimates to be meaningful.
func RestoreSketch(st SketchState, opts ...Option) *Sketch {
	s := NewSketch(len(st.Counters), st.Width, opts...)
	s.epoch.Store(st.Epoch)
	for i := range s.rows {
		copy(s.rows[i].counters, st.Counters[i])
		copy(s.rows[i].epochs, st.Epochs[i])
	}
	return s
}
