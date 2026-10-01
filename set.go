package crset

import (
	"fmt"
	"sort"
)

// Config tunes when a container switches physical representation.
// The zero value selects DefaultConfig.
type Config struct {
	// SparseToBitmapCard: a sparse container upgrades to bitmap when its
	// cardinality rises above this value. Range [1, 65536].
	SparseToBitmapCard int
	// BitmapToSparseCard: a bitmap container downgrades to sparse when its
	// cardinality falls below this value. Must be < SparseToBitmapCard; the
	// gap is the hysteresis band that prevents flip-flopping on deletes.
	BitmapToSparseCard int
	// RunSavingsFactor: a container converts to RLE when
	// runBytes*RunSavingsFactor <= currentBytes, and leaves RLE only when
	// runBytes > altBytes. Must be 0 (disable RLE) or >= 2 (the factor is
	// the RLE hysteresis band).
	RunSavingsFactor int
}

// DefaultConfig mirrors classic roaring defaults: 4 KiB sparse limit with a
// 512-wide hysteresis band, and RLE when it is at least 2x smaller.
func DefaultConfig() Config {
	return Config{
		SparseToBitmapCard: 4096,
		BitmapToSparseCard: 3584,
		RunSavingsFactor:   2,
	}
}

func (c Config) withDefaults() Config {
	if c == (Config{}) {
		return DefaultConfig()
	}
	return c
}

func (c Config) validate() error {
	if c.SparseToBitmapCard < 1 || c.SparseToBitmapCard > 65536 {
		return fmt.Errorf("crset: SparseToBitmapCard %d out of range [1, 65536]", c.SparseToBitmapCard)
	}
	if c.BitmapToSparseCard < 0 || c.BitmapToSparseCard >= c.SparseToBitmapCard {
		return fmt.Errorf("crset: BitmapToSparseCard %d must be in [0, %d)", c.BitmapToSparseCard, c.SparseToBitmapCard)
	}
	if c.RunSavingsFactor == 1 || c.RunSavingsFactor < 0 {
		return fmt.Errorf("crset: RunSavingsFactor %d must be 0 (disabled) or >= 2", c.RunSavingsFactor)
	}
	return nil
}

// bucket pairs a high-16-bit key with its container. Buckets live in a
// sorted slice (binary search), not a map.
type bucket struct {
	high uint16
	c    *container
}

// Set is a compressed sorted set of uint32, roaring-style: values are split
// by their high 16 bits into per-bucket containers, each stored in the
// cheapest of sparse/bitmap/RLE encodings.
type Set struct {
	cfg         Config
	buckets     []bucket // sorted by high
	card        int
	conversions uint64
}

// New returns a Set with DefaultConfig.
func New() *Set {
	s, _ := NewSet(Config{})
	return s
}

// NewSet returns a Set with the given config (zero value = DefaultConfig).
func NewSet(cfg Config) (*Set, error) {
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Set{cfg: cfg}, nil
}

// Config returns the config the set was built with.
func (s *Set) Config() Config { return s.cfg }

func (s *Set) findBucket(high uint16) (int, bool) {
	i := sort.Search(len(s.buckets), func(i int) bool { return s.buckets[i].high >= high })
	if i < len(s.buckets) && s.buckets[i].high == high {
		return i, true
	}
	return i, false
}

// Add inserts x, reporting whether it was not already present.
func (s *Set) Add(x uint32) bool {
	high, low := uint16(x>>16), uint16(x)
	i, found := s.findBucket(high)
	if !found {
		c := &container{rep: repSparse}
		c.add(low)
		s.buckets = append(s.buckets, bucket{})
		copy(s.buckets[i+1:], s.buckets[i:])
		s.buckets[i] = bucket{high: high, c: c}
		s.card++
		return true
	}
	c := s.buckets[i].c
	if !c.add(low) {
		return false
	}
	s.card++
	if c.normalize(s.cfg) {
		s.conversions++
	}
	return true
}

// Remove deletes x, reporting whether it was present.
func (s *Set) Remove(x uint32) bool {
	high, low := uint16(x>>16), uint16(x)
	i, found := s.findBucket(high)
	if !found {
		return false
	}
	c := s.buckets[i].c
	if !c.remove(low) {
		return false
	}
	s.card--
	if c.card == 0 {
		s.buckets = append(s.buckets[:i], s.buckets[i+1:]...)
		return true
	}
	if c.normalize(s.cfg) {
		s.conversions++
	}
	return true
}

// Contains reports whether x is in the set.
func (s *Set) Contains(x uint32) bool {
	i, found := s.findBucket(uint16(x >> 16))
	return found && s.buckets[i].c.contains(uint16(x))
}

// Cardinality returns the number of elements.
func (s *Set) Cardinality() int { return s.card }

// Conversions returns how many representation conversions have happened.
func (s *Set) Conversions() uint64 { return s.conversions }

// Elements returns all values in ascending order.
func (s *Set) Elements() []uint32 {
	out := make([]uint32, 0, s.card)
	for i := range s.buckets {
		base := uint32(s.buckets[i].high) << 16
		it := s.buckets[i].c.iter()
		for {
			v, ok := it.next()
			if !ok {
				break
			}
			out = append(out, base|uint32(v))
		}
	}
	return out
}

// Optimize forces an RLE check on every container. Automatic RLE conversion
// of bitmap containers is amortized (checked every 64 mutations); Optimize
// applies it immediately everywhere.
func (s *Set) Optimize() {
	for i := range s.buckets {
		c := s.buckets[i].c
		if c.rep != repRun && c.maybeToRun(s.cfg) {
			s.conversions++
		}
	}
}
