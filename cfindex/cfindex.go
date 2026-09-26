package cfindex

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
)

const (
	magic           = "CFIX"
	version   uint8 = 1
	headerSize      = 44

	defaultSeed       uint64 = 0x9e3779b97f4a7c15
	defaultLoadFactor        = 0.70
	defaultMaxRetries        = 7
	defaultMaxDisplace       = 2047
	defaultBucketWidth       = 4
	defaultFingerprintWidth = 2
)

var (
	ErrInvalidLoadFactor    = errors.New("load factor must be greater than 0 and at most 1")
	ErrInvalidMaxRetries    = errors.New("max retries must be at least 0")
	ErrInvalidMaxDisplace   = errors.New("max displacements must be at least 0")
	ErrInvalidBucketWidth   = errors.New("bucket width must be positive")
	ErrInvalidFingerprint   = errors.New("fingerprint width must be 1, 2, 4, or 8")
	ErrTooManyKeys          = errors.New("key set is too large for the uint32 encoded layout")
	ErrCorruptIndex         = errors.New("corrupt conflict-free index")
)

type config struct {
	seed             uint64
	loadFactor       float64
	maxRetries       int
	maxDisplacements int
	bucketWidth      int
	fingerprintWidth int
}

type DuplicateKeyError struct {
	Key       []byte
	First     int
	Duplicate int
}

func (e *DuplicateKeyError) Error() string {
	return fmt.Sprintf("duplicate input key at position %d; first position %d; key %q", e.Duplicate, e.First, e.Key)
}

type Draft struct {
	keys             [][]byte
	seed             uint64
	retries          int
	maxRetries       int
	maxDisplacements int
	attemptedDisps   uint64
	reason           string
}

func (d *Draft) Keys() [][]byte {
	out := make([][]byte, len(d.keys))
	for i, key := range d.keys {
		out[i] = append([]byte(nil), key...)
	}
	return out
}

func (d *Draft) KeyCount() int            { return len(d.keys) }
func (d *Draft) Seed() uint64             { return d.seed }
func (d *Draft) Retries() int             { return d.retries }
func (d *Draft) MaxRetries() int          { return d.maxRetries }
func (d *Draft) AttemptedDisplacements() uint64 { return d.attemptedDisps }
func (d *Draft) Reason() string           { return d.reason }

type BuildFailure struct {
	Draft *Draft
}

func (e *BuildFailure) Error() string {
	return fmt.Sprintf("conflict-free index not completed after %d retries: %s", e.Draft.retries, e.Draft.reason)
}

type Index struct {
	raw              []byte
	n                int
	slots            int
	buckets          int
	fingerprintWidth int
	displacementWidth int
	seed             uint64
	retries          uint32
	dispOffset       uint64
	fpOffset         uint64
	slotOffset       uint64
	blobOffset       uint64
	blobLength       uint64
}

type Option func(*config)

func WithSeed(seed uint64) Option {
	return func(c *config) { c.seed = seed }
}

func WithLoadFactor(loadFactor float64) Option {
	return func(c *config) { c.loadFactor = loadFactor }
}

func WithMaxRetries(maxRetries int) Option {
	return func(c *config) { c.maxRetries = maxRetries }
}

func WithMaxDisplacements(maxDisplacements int) Option {
	return func(c *config) { c.maxDisplacements = maxDisplacements }
}

func WithBucketWidth(bucketWidth int) Option {
	return func(c *config) { c.bucketWidth = bucketWidth }
}

func WithFingerprintWidth(fingerprintWidth int) Option {
	return func(c *config) { c.fingerprintWidth = fingerprintWidth }
}

func Build(keys [][]byte, options ...Option) (*Index, error) {
	cloned := cloneKeys(keys)
	if i, first, ok := findDuplicate(cloned); ok {
		return nil, &DuplicateKeyError{Key: append([]byte(nil), cloned[i]...), First: first, Duplicate: i}
	}

	cfg := config{
		seed:             defaultSeed,
		loadFactor:       defaultLoadFactor,
		maxRetries:       defaultMaxRetries,
		maxDisplacements: defaultMaxDisplace,
		bucketWidth:      defaultBucketWidth,
		fingerprintWidth: defaultFingerprintWidth,
	}
	for _, option := range options {
		option(&cfg)
	}
	if cfg.loadFactor <= 0 || cfg.loadFactor > 1 {
		return nil, ErrInvalidLoadFactor
	}
	if cfg.maxRetries < 0 {
		return nil, ErrInvalidMaxRetries
	}
	if cfg.maxDisplacements < 0 {
		return nil, ErrInvalidMaxDisplace
	}
	if cfg.bucketWidth <= 0 {
		return nil, ErrInvalidBucketWidth
	}
	if cfg.fingerprintWidth != 1 && cfg.fingerprintWidth != 2 && cfg.fingerprintWidth != 4 && cfg.fingerprintWidth != 8 {
		return nil, ErrInvalidFingerprint
	}

	n := len(cloned)
	m, buckets, err := tableShape(n, cfg.loadFactor, cfg.bucketWidth)
	if err != nil {
		return nil, err
	}

	var lastDraft *Draft
	for retry := 0; retry <= cfg.maxRetries; retry++ {
		seed := candidateSeed(cfg.seed, uint64(retry))
		draft := &Draft{
			keys:             cloned,
			seed:             seed,
			retries:          retry,
			maxRetries:       cfg.maxRetries,
			maxDisplacements: cfg.maxDisplacements,
		}

		index, reason, attempted := tryBuild(cloned, seed, m, buckets, cfg.maxDisplacements, cfg.fingerprintWidth, retry)
		draft.attemptedDisps = attempted
		if index != nil {
			return index, nil
		}
		draft.reason = reason
		lastDraft = draft
		if retry == cfg.maxRetries {
			return nil, &BuildFailure{Draft: draft}
		}
	}
	return nil, &BuildFailure{Draft: lastDraft}
}

func cloneKeys(keys [][]byte) [][]byte {
	out := make([][]byte, len(keys))
	for i, key := range keys {
		out[i] = append([]byte(nil), key...)
	}
	return out
}

func findDuplicate(keys [][]byte) (int, int, bool) {
	order := make([]int, len(keys))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := keys[order[i]], keys[order[j]]
		if c := bytesCompare(a, b); c != 0 {
			return c < 0
		}
		return order[i] < order[j]
	})
	for i := 1; i < len(order); i++ {
		if bytesEqual(keys[order[i-1]], keys[order[i]]) {
			return order[i], order[i-1], true
		}
	}
	return 0, 0, false
}

func bytesCompare(a, b []byte) int {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for i := 0; i < limit; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

func bytesEqual(a, b []byte) bool { return bytesCompare(a, b) == 0 }

func tableShape(n int, loadFactor float64, bucketWidth int) (int, int, error) {
	if n == 0 {
		return 0, 0, nil
	}
	required := uint64(float64(n) / loadFactor)
	if required < uint64(n) {
		return 0, 0, ErrTooManyKeys
	}
	if required > (uint64(1)<<31) {
		return 0, 0, ErrTooManyKeys
	}
	m := nextPowerOfTwo(required)
	buckets := (n + bucketWidth - 1) / bucketWidth
	if uint64(buckets) >= uint64(m) {
		buckets = int(uint64(m) - 1)
	}
	if buckets <= 0 {
		buckets = 1
	}
	return int(m), buckets, nil
}

func nextPowerOfTwo(value uint64) uint64 {
	if value <= 1 {
		return 1
	}
	pow := uint64(1)
	for pow < value {
		pow <<= 1
	}
	return pow
}

func candidateSeed(initial uint64, retry uint64) uint64 {
	return splitMix64(initial + 0x9e3779b97f4a7c15 + retry*0xd1b54a32d192ed03)
}

func splitMix64(value uint64) uint64 {
	value += 0x9e3779b97f4a7c15
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	return value ^ (value >> 31)
}

type keyHash struct {
	h0 uint64
	h1 uint64
}

func hashKey(seed uint64, key []byte) keyHash {
	var prefix [8]byte
	binary.LittleEndian.PutUint64(prefix[:], seed)
	hasher := sha256.New()
	hasher.Write(prefix[:])
	hasher.Write(key)
	sum := hasher.Sum(nil)
	return keyHash{
		h0: binary.LittleEndian.Uint64(sum[0:8]),
		h1: binary.LittleEndian.Uint64(sum[8:16]),
	}
}

func tryBuild(keys [][]byte, seed uint64, slots, buckets, maxDisplace, fingerprintWidth, retry int) (*Index, string, uint64) {
	n := len(keys)
	if n == 0 {
		index, err := encode(keys, nil, nil, nil, seed, uint32(retry), slots, buckets, fingerprintWidth)
		if err != nil {
			return nil, err.Error(), 0
		}
		return index, "", 0
	}

	hashes := make([]keyHash, n)
	bucketCounts := make([]int, buckets)
	bucketIDs := make([]uint64, n)
	for i, key := range keys {
		hash := hashKey(seed, key)
		hashes[i] = hash
		bucket := hash.h0 % uint64(buckets)
		bucketIDs[i] = bucket
		bucketCounts[bucket]++
	}

	order := make([]int, buckets)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if bucketCounts[a] != bucketCounts[b] {
			return bucketCounts[a] > bucketCounts[b]]
		}
		return a < b
	})

	members := make([][]int, buckets)
	for i, bucket := range bucketIDs {
		members[bucket] = append(members[bucket], i)
	}
	for bucket := range members {
		sort.Ints(members[bucket])
	}

	owner := make([]int, slots)
	for i := range owner {
		owner[i] = -1
	}
	displacements := make([]uint32, buckets)
	slotOwner := make([]int, n)
	attempted := uint64(0)
	mask := uint64(slots - 1)

	for _, bucket := range order {
		member := members[bucket]
		if len(member) == 0 {
			displacements[bucket] = 0
			continue
		}
		found := false
	limit:
		for d := 0; d <= maxDisplace; d++ {
			attempted++
			candidateSlots := make([]uint64, 0, len(member))
			seen := make(map[uint64]struct{}, len(member))
			for _, keyIndex := range member {
				slot := (hashes[keyIndex].h1*uint64(d) + bucketIDs[keyIndex]) & mask
				if _, collision := seen[slot]; collision {
					continue limit
				}
				if owner[slot] != -1 {
					continue limit
				}
				seen[slot] = struct{}{}
				candidateSlots = append(candidateSlots, slot)
			}
			for position, keyIndex := range member {
				slot := candidateSlots[position]
				owner[slot] = keyIndex
				slotOwner[keyIndex] = int(slot)
			}
			displacements[bucket] = uint32(d)
			found = true
			break
		}
		if !found {
			return nil, fmt.Sprintf("bucket %d with %d keys had no free displacement up to %d", bucket, len(member), maxDisplace), attempted
		}
	}

	index, err := encode(keys, hashes, displacements, slotOwner, seed, uint32(retry), slots, buckets, fingerprintWidth)
	if err != nil {
		return nil, err.Error(), attempted
	}
	return index, "", attempted
}
