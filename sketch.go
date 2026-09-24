// Package freqsketch implements a decaying frequency sketch (count-min
// style) with an admission-controlled heavy-hitter table and an
// event-processing pipeline with injectable dependencies.
//
// Estimates are upper bounds: hash collisions can only inflate a count,
// never deflate it. Counts saturate at a configured maximum instead of
// overflowing, and decay (per-epoch halving) is applied lazily so it can
// never drive a count negative.
package freqsketch

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"sync"
	"sync/atomic"
)

// DefaultSeed is the fixed hash seed. It is a compile-time constant so
// sketches are deterministic across runs and processes.
const DefaultSeed uint64 = 0x9e3779b97f4a7c15

// Hasher maps a key to a bucket index in [0, buckets). It is injectable
// so tests can force collisions.
type Hasher func(key string, seed uint64, buckets int) int

// DefaultHasher is FNV-1a over seed||key.
func DefaultHasher(key string, seed uint64, buckets int) int {
	var sb [8]byte
	binary.LittleEndian.PutUint64(sb[:], seed)
	h := fnv.New64a()
	_, _ = h.Write(sb[:])
	_, _ = h.Write([]byte(key))
	return int(h.Sum64() % uint64(buckets))
}

// normalize clamps an injected hasher's output into [0, buckets) so a
// sloppy hasher cannot panic the sketch.
func normalize(h, buckets int) int {
	h %= buckets
	if h < 0 {
		h += buckets
	}
	return h
}

// Cell is one saturating counter plus the epoch it was last decayed to.
type Cell struct {
	Value uint64
	Epoch uint64
}

type shard struct {
	mu    sync.Mutex
	cells [][]Cell // [row][bucket]
}

// Sketch is a sharded count-min sketch with lazy periodic decay.
//
// Logical order contract: decay happens exactly at AdvanceEpoch calls.
// Adds and estimates between two AdvanceEpoch calls observe the same
// counts regardless of physical interleaving, because decay is applied
// lazily per cell based on the epoch counter.
type Sketch struct {
	shards   []shard
	rows     int
	buckets  int
	seed     uint64
	maxCount uint64
	hasher   Hasher
	epoch    atomic.Uint64
}

// SketchConfig configures a Sketch. Zero fields get defaults.
type SketchConfig struct {
	Shards          int    // default 16
	Rows            int    // hash rows per shard, default 4
	BucketsPerShard int    // default 1024
	MaxCount        uint64 // saturation cap, default math.MaxUint64
	Seed            uint64 // default DefaultSeed
	Hasher          Hasher // default DefaultHasher (injectable for tests)
}

func NewSketch(cfg SketchConfig) *Sketch {
	if cfg.Shards <= 0 {
		cfg.Shards = 16
	}
	if cfg.Rows <= 0 {
		cfg.Rows = 4
	}
	if cfg.BucketsPerShard <= 0 {
		cfg.BucketsPerShard = 1024
	}
	if cfg.MaxCount == 0 {
		cfg.MaxCount = math.MaxUint64
	}
	if cfg.Seed == 0 {
		cfg.Seed = DefaultSeed
	}
	if cfg.Hasher == nil {
		cfg.Hasher = DefaultHasher
	}
	s := &Sketch{
		shards:   make([]shard, cfg.Shards),
		rows:     cfg.Rows,
		buckets:  cfg.BucketsPerShard,
		seed:     cfg.Seed,
		maxCount: cfg.MaxCount,
		hasher:   cfg.Hasher,
	}
	for i := range s.shards {
		rows := make([][]Cell, cfg.Rows)
		for r := range rows {
			rows[r] = make([]Cell, cfg.BucketsPerShard)
		}
		s.shards[i].cells = rows
	}
	return s
}

// Epoch returns the current decay epoch.
func (s *Sketch) Epoch() uint64 { return s.epoch.Load() }

// AdvanceEpoch declares a decay boundary: every count is logically
// halved (integer shift) at this point. The halving is applied lazily
// when a cell is next touched, so interleaving with concurrent updates
// cannot change the declared logical order.
func (s *Sketch) AdvanceEpoch() { s.epoch.Add(1) }

func (s *Sketch) shardOf(key string) *shard {
	return &s.shards[normalize(s.hasher(key, s.seed^0x5bd1e9955bd1e995, len(s.shards)), len(s.shards))]
}

func (s *Sketch) bucketOf(key string, row int) int {
	return normalize(s.hasher(key, s.seed+uint64(row)*0x9e3779b1, s.buckets), s.buckets)
}

// touch lazily decays a cell to the current epoch. Caller holds the
// shard lock. Right shift on unsigned values can never go negative.
func (s *Sketch) touch(c *Cell) {
	cur := s.epoch.Load()
	if c.Epoch >= cur {
		return
	}
	shift := cur - c.Epoch
	if shift >= 64 {
		c.Value = 0
	} else {
		c.Value >>= shift
	}
	c.Epoch = cur
}

// Add increments the count for key by delta (saturating at MaxCount)
// and returns the resulting estimate.
func (s *Sketch) Add(key string, delta uint64) uint64 {
	sh := s.shardOf(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	var best uint64 = math.MaxUint64
	for r := 0; r < s.rows; r++ {
		c := &sh.cells[r][s.bucketOf(key, r)]
		s.touch(c)
		if delta > s.maxCount-c.Value {
			c.Value = s.maxCount
		} else {
			c.Value += delta
		}
		if c.Value < best {
			best = c.Value
		}
	}
	return best
}

// Estimate returns an upper-bound frequency estimate for key. It never
// underestimates the true (decayed) count; collisions can only inflate
// it. The value is an estimate, not an exact frequency.
func (s *Sketch) Estimate(key string) uint64 {
	sh := s.shardOf(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	best := uint64(math.MaxUint64)
	for r := 0; r < s.rows; r++ {
		c := &sh.cells[r][s.bucketOf(key, r)]
		s.touch(c)
		if c.Value < best {
			best = c.Value
		}
	}
	return best
}

// SketchState is a checkpoint of a Sketch.
type SketchState struct {
	Epoch uint64
	Cells [][][]Cell // [shard][row][bucket]
}

// Snapshot captures the full sketch state.
func (s *Sketch) Snapshot() SketchState {
	st := SketchState{Epoch: s.epoch.Load(), Cells: make([][][]Cell, len(s.shards))}
	for i := range s.shards {
		sh := &s.shards[i]
		sh.mu.Lock()
		rows := make([][]Cell, len(sh.cells))
		for r := range sh.cells {
			cp := make([]Cell, len(sh.cells[r]))
			copy(cp, sh.cells[r])
			rows[r] = cp
		}
		sh.mu.Unlock()
		st.Cells[i] = rows
	}
	return st
}

// Restore replaces the sketch state with a previous snapshot. The
// snapshot must come from a sketch with identical dimensions.
func (s *Sketch) Restore(st SketchState) {
	s.epoch.Store(st.Epoch)
	for i := range s.shards {
		sh := &s.shards[i]
		sh.mu.Lock()
		for r := range sh.cells {
			copy(sh.cells[r], st.Cells[i][r])
		}
		sh.mu.Unlock()
	}
}
