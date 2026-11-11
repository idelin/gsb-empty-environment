// Package filtermerge implements mergeable counting Bloom filter summaries
// for a security gateway that folds many tenants' per-hour shards into a
// single queryable aggregate.
//
// A Summary only accepts shards whose tenant ID, salt generation and hash
// parameters match exactly. Counters saturate at a configurable width and
// saturated counters become sticky: revoking a shard never decrements a
// saturated counter, so revocation can introduce false positives but never
// false negatives.
package filtermerge

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
)

var (
	// ErrIncompatible is returned when a shard's tenant, salt generation or
	// hash parameters differ from the summary's.
	ErrIncompatible = errors.New("filtermerge: incompatible shard parameters")
	// ErrMemoryLimit is returned when a merge would exceed the summary's
	// configured memory budget; the summary is left unchanged.
	ErrMemoryLimit = errors.New("filtermerge: memory limit exceeded")
)

// Params describes the compatibility contract of a summary. Two filters may
// only be merged when every field is identical.
type Params struct {
	TenantID    string // owning tenant
	SaltGen     uint64 // salt generation, rotated on re-keying
	NumCounters uint32 // number of counters (m)
	NumHashes   uint8  // hash functions per key (k)
	CounterBits uint8  // counter width in bits, 1..8
}

func (p Params) validate() error {
	switch {
	case p.TenantID == "":
		return errors.New("filtermerge: empty tenant ID")
	case p.NumCounters == 0:
		return errors.New("filtermerge: NumCounters must be > 0")
	case p.NumHashes == 0:
		return errors.New("filtermerge: NumHashes must be > 0")
	case p.CounterBits == 0 || p.CounterBits > 8:
		return errors.New("filtermerge: CounterBits must be in 1..8")
	}
	return nil
}

// maxCounter is the saturation point of a single counter.
func (p Params) maxCounter() uint8 {
	return 1<<p.CounterBits - 1
}

// HashFunc maps a key and seed to a 64-bit hash. The default is FNV-1a with
// the seed prepended. Tests may substitute a weaker function to force
// collisions; all filters that are merged together must share one function.
type HashFunc func(key []byte, seed uint64) uint64

func defaultHash(key []byte, seed uint64) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], seed)
	h.Write(buf[:])
	h.Write(key)
	return h.Sum64()
}

const (
	saltMix1 = 0x9E3779B97F4A7C15
	saltMix2 = 0xC2B2AE3D27D4EB4F
)

// positions returns the k counter indexes for key via salted double hashing.
func positions(hf HashFunc, key []byte, p Params) []uint32 {
	salt := p.SaltGen * saltMix1
	h1 := hf(key, salt)
	h2 := hf(key, salt^saltMix2)
	pos := make([]uint32, p.NumHashes)
	for i := range pos {
		pos[i] = uint32((h1 + uint64(i)*uint64(h2)) % uint64(p.NumCounters))
	}
	return pos
}

type config struct {
	hash     HashFunc
	maxBytes int
}

// Option customizes shard or summary construction.
type Option func(*config)

// WithHashFunc overrides the hash function. It must be applied consistently
// to every shard and summary that participates in a merge.
func WithHashFunc(h HashFunc) Option {
	return func(c *config) { c.hash = h }
}

// WithMaxBytes caps the summary's counter storage (aggregate plus retained
// per-shard copies). Merges that would exceed the cap fail with
// ErrMemoryLimit. Zero means unlimited.
func WithMaxBytes(n int) Option {
	return func(c *config) { c.maxBytes = n }
}

func buildConfig(opts []Option) config {
	c := config{hash: defaultHash}
	for _, o := range opts {
		o(&c)
	}
	return c
}

// Shard is a single tenant's counting Bloom filter for one delivery window.
type Shard struct {
	id       string
	params   Params
	hash     HashFunc
	counters []uint8
}

// NewShard creates an empty shard. The id must be unique per delivery; it is
// used for idempotent merge and revocation.
func NewShard(id string, p Params, opts ...Option) (*Shard, error) {
	if id == "" {
		return nil, errors.New("filtermerge: empty shard ID")
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	c := buildConfig(opts)
	return &Shard{
		id:       id,
		params:   p,
		hash:     c.hash,
		counters: make([]uint8, p.NumCounters),
	}, nil
}

// ID returns the shard's delivery identifier.
func (s *Shard) ID() string { return s.id }

// Params returns the shard's compatibility parameters.
func (s *Shard) Params() Params { return s.params }

// Add records key, saturating each touched counter at the configured width.
func (s *Shard) Add(key []byte) {
	max := s.params.maxCounter()
	for _, pos := range positions(s.hash, key, s.params) {
		if s.counters[pos] < max {
			s.counters[pos]++
		}
	}
}

// Contains reports whether key may be present in this shard.
func (s *Shard) Contains(key []byte) bool {
	for _, pos := range positions(s.hash, key, s.params) {
		if s.counters[pos] == 0 {
			return false
		}
	}
	return true
}

func (s *Shard) clone() *Shard {
	c := *s
	c.counters = make([]uint8, len(s.counters))
	copy(c.counters, s.counters)
	return &c
}

// Summary is the merged aggregate for one tenant, salt generation and hash
// parameter set. It is safe for concurrent use.
type Summary struct {
	mu       sync.RWMutex
	params   Params
	hash     HashFunc
	maxBytes int
	counters []uint8
	shards   map[string]*Shard
}

// NewSummary creates an empty aggregate for the given parameters.
func NewSummary(p Params, opts ...Option) (*Summary, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	c := buildConfig(opts)
	return &Summary{
		params:   p,
		hash:     c.hash,
		maxBytes: c.maxBytes,
		counters: make([]uint8, p.NumCounters),
		shards:   make(map[string]*Shard),
	}, nil
}

// Params returns the summary's compatibility parameters.
func (m *Summary) Params() Params { return m.params }

// Merge folds a shard into the aggregate. It is a no-op if a shard with the
// same ID was already merged, which makes duplicate hourly deliveries safe.
// Mismatched tenant, salt generation or hash parameters are rejected, as are
// merges that would exceed the memory budget; both leave the summary
// unchanged.
func (m *Summary) Merge(s *Shard) error {
	if s == nil {
		return errors.New("filtermerge: nil shard")
	}
	if s.params != m.params {
		return fmt.Errorf("%w: shard tenant=%q saltgen=%d", ErrIncompatible, s.params.TenantID, s.params.SaltGen)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.shards[s.id]; dup {
		return nil
	}
	if m.maxBytes > 0 && m.bytesLocked()+len(s.counters) > m.maxBytes {
		return fmt.Errorf("%w: budget %d bytes", ErrMemoryLimit, m.maxBytes)
	}
	max := m.params.maxCounter()
	for i, c := range s.counters {
		if c == 0 {
			continue
		}
		if sum := uint16(m.counters[i]) + uint16(c); sum > uint16(max) {
			m.counters[i] = max
		} else {
			m.counters[i] = uint8(sum)
		}
	}
	m.shards[s.id] = s.clone()
	return nil
}

// Revoke subtracts a previously merged shard. Counters that saturated during
// merging are sticky and never decremented, so revocation cannot produce
// false negatives for the remaining shards. Revoking an unknown or already
// revoked ID is an idempotent no-op reporting false.
func (m *Summary) Revoke(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.shards[id]
	if !ok {
		return false
	}
	max := m.params.maxCounter()
	for i, c := range s.counters {
		if c == 0 || m.counters[i] == max {
			continue
		}
		// Unsaturated counters equal the exact sum of live shards, so the
		// subtraction can never underflow.
		m.counters[i] -= c
	}
	delete(m.shards, id)
	return true
}

// Len returns the number of live shards in the aggregate.
func (m *Summary) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.shards)
}

// Bytes returns the counter storage currently used: the aggregate plus one
// retained copy per live shard.
func (m *Summary) Bytes() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.bytesLocked()
}

func (m *Summary) bytesLocked() int {
	return len(m.counters) * (1 + len(m.shards))
}

// Snapshot captures an immutable point-in-time view of the aggregate.
func (m *Summary) Snapshot() *Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c := make([]uint8, len(m.counters))
	copy(c, m.counters)
	return &Snapshot{params: m.params, hash: m.hash, counters: c}
}

// Snapshot is an immutable, queryable view of a Summary.
type Snapshot struct {
	params   Params
	hash     HashFunc
	counters []uint8
}

// Contains reports whether key may be present. It never returns a false
// negative for keys added to shards that are still merged, regardless of
// counter saturation or revocations of other shards.
func (s *Snapshot) Contains(key []byte) bool {
	for _, pos := range positions(s.hash, key, s.params) {
		if s.counters[pos] == 0 {
			return false
		}
	}
	return true
}

// Saturated returns the number of counters pinned at the saturation point.
func (s *Snapshot) Saturated() int {
	max := s.params.maxCounter()
	n := 0
	for _, c := range s.counters {
		if c == max {
			n++
		}
	}
	return n
}
