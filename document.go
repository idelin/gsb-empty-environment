package netsec

import (
	"errors"
	"fmt"
	"sync"
)

// DefaultMaskByte is the fixed byte written over every hidden evidence byte.
const DefaultMaskByte byte = 0x2A // '*'

// Config configures a Document.
type Config struct {
	// MaskByte replaces every evidence byte the reader is not authorized to
	// see. The default is DefaultMaskByte.
	MaskByte byte

	// LogicalLength declares the immutable evidence span in bytes. It must be
	// at least len(origin); larger values model a huge sparse evidence object
	// whose tail offsets have no physical backing. Reads at offsets beyond
	// len(origin) return zero bytes before masking, while range validation uses
	// the logical length. It defaults to len(origin).
	LogicalLength int64
}

// Option customizes a Document at construction.
type Option func(*Config)

// WithMaskByte overrides the fixed mask byte.
func WithMaskByte(b byte) Option {
	return func(c *Config) { c.MaskByte = b }
}

// WithLogicalLength declares a logical evidence span larger than the physical
// backing to support huge sparse offset spaces without allocating them.
func WithLogicalLength(n int64) Option {
	return func(c *Config) { c.LogicalLength = n }
}

// Document is an immutable evidence buffer together with the versioned set of
// sensitive ranges guarding it. The evidence bytes never change and are never
// copied on updates; each range update produces a new immutable Version while
// earlier versions stay readable.
//
// All methods are safe for concurrent use.
type Document struct {
	mu       sync.RWMutex
	origin   []byte
	maskByte byte
	logical  int64

	versions []*Version
}

// Version is an immutable snapshot of the sensitive ranges at one point in
// time. Versions are safe for concurrent use and remain valid after further
// updates to their Document.
type Version struct {
	number   int64
	origin   []byte
	maskByte byte
	logical  int64
	segs     []segment
	ranges   []Range // sorted, deduplicated source ranges
}

// NewDocument stores origin as immutable evidence. The returned document owns
// version 1, which contains no sensitive ranges. The caller must not mutate the
// backing array of origin after handing it over.
func NewDocument(origin []byte, opts ...Option) *Document {
	cfg := Config{MaskByte: DefaultMaskByte}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.LogicalLength < int64(len(origin)) {
		cfg.LogicalLength = int64(len(origin))
	}
	return &Document{
		origin:   origin,
		maskByte: cfg.MaskByte,
		logical:  cfg.LogicalLength,
		versions: []*Version{{
			number:   1,
			origin:   origin,
			maskByte: cfg.MaskByte,
			logical:  cfg.LogicalLength,
		}},
	}
}

// ErrUnknownVersion is returned when a requested version number does not exist.
var ErrUnknownVersion = errors.New("netsec: unknown version")

// AddRanges appends ranges to the ranges of the current version and publishes
// the result as a new immutable version. Overlapping ranges are allowed.
func (d *Document) AddRanges(ranges ...Range) (*Version, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	latest := d.versions[len(d.versions)-1]
	merged := make([]Range, 0, len(latest.ranges)+len(ranges))
	merged = append(merged, latest.ranges...)
	merged = append(merged, ranges...)
	return d.publish(merged)
}

// ReplaceRanges publishes a new immutable version whose sensitive ranges are
// exactly the supplied ones; ranges of earlier versions are retained untouched.
func (d *Document) ReplaceRanges(ranges ...Range) (*Version, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	next := make([]Range, len(ranges))
	copy(next, ranges)
	return d.publish(next)
}

func (d *Document) publish(ranges []Range) (*Version, error) {
	n := d.logical
	for _, r := range ranges {
		if err := r.validate(n); err != nil {
			return nil, err
		}
	}
	segs, uniq := compileRanges(ranges)
	v := &Version{
		number:   int64(len(d.versions)) + 1,
		origin:   d.origin,
		maskByte: d.maskByte,
		logical:  d.logical,
		segs:     segs,
		ranges:   uniq,
	}
	d.versions = append(d.versions, v)
	return v, nil
}

// Version returns the immutable version with the given 1-based number.
func (d *Document) Version(number int64) (*Version, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if number < 1 || int(number) > len(d.versions) {
		return nil, fmt.Errorf("%w: %d", ErrUnknownVersion, number)
	}
	return d.versions[number-1], nil
}

// Latest returns the most recently published version.
func (d *Document) Latest() *Version {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.versions[len(d.versions)-1]
}

// Len is the logical evidence length in bytes.
func (d *Document) Len() int64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.logical
}

// MaskByte returns the fixed byte used to hide unauthorized bytes.
func (d *Document) MaskByte() byte { return d.maskByte }

// Number is the 1-based version number.
func (v *Version) Number() int64 { return v.number }

// Ranges returns the sorted, deduplicated sensitive ranges of this version.
func (v *Version) Ranges() []Range {
	out := make([]Range, len(v.ranges))
	copy(out, v.ranges)
	return out
}

// Reader pins a reader to this version with the given authorized labels. The
// returned reader never observes later updates.
func (v *Version) Reader(authorized ...string) *Reader {
	return newReader(v, authorized)
}

// MaskedRanges reports the minimal half-open byte ranges that a reader holding
// only authorized labels cannot see in this version.
func (v *Version) MaskedRanges(authorized ...string) []Range {
	return maskedRanges(v.segs, labelSet(authorized))
}
