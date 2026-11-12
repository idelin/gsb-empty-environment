// Package netsec implements an on-demand reader for a storage format whose
// blocks may reference previously declared dictionary blocks.
//
// A tiny reference graph can fan out into a very large logical stream (a
// decompression-bomb style attack). A Reader therefore defends its host with
// two independent, mutually reinforcing meters that are both enforced while
// bytes are produced:
//
//   - logical output bytes, capped by Limits.MaxOutputBytes, and
//   - reference expansions, capped by Limits.MaxExpansions.
//
// Before any payload byte is decoded, Open validates the reachable reference
// graph and rejects cycles, missing targets and over-long declarations.
// Decoded dictionary blocks may be cached and shared between references, but
// every emission is still charged against the output meter, so caching can
// never enlarge the logical output a caller is allowed to observe.
//
// Read operations are cancellable through ReadContext. A cancelled Read keeps
// every prefix byte already returned; a later Read/ReadContext resumes from
// the same position and re-charges only the work that remains.
package netsec

import (
	"context"
	"errors"
)

// Sentinel errors returned by the package. All concrete failure values wrap
// one of these, so errors.Is works against wrapped errors.
var (
	// ErrCycle means the reference graph reachable from a root contains a
	// directed cycle.
	ErrCycle = errors.New("netsec: cyclic block reference graph")

	// ErrUnknownBlock means a block id is referenced but not present in the
	// BlockStore.
	ErrUnknownBlock = errors.New("netsec: unknown referenced block")

	// ErrOverlongDecl means a block declares a logical size larger than the
	// size implied by its literals and expanded references.
	ErrOverlongDecl = errors.New("netsec: block declares more bytes than it produces")

	// ErrBadDecl means a declared size is negative, overflows or otherwise
	// fails structural validation.
	ErrBadDecl = errors.New("netsec: invalid block declaration")

	// ErrOutputLimit means the logical output byte budget was exhausted.
	ErrOutputLimit = errors.New("netsec: logical output limit exceeded")

	// ErrExpansionLimit means the reference-expansion budget was exhausted.
	ErrExpansionLimit = errors.New("netsec: reference expansion limit exceeded")

	// ErrDepthLimit means the reference nesting depth exceeded the limit.
	ErrDepthLimit = errors.New("netsec: reference depth limit exceeded")

	// ErrBlockLimit means too many distinct blocks were reached.
	ErrBlockLimit = errors.New("netsec: block count limit exceeded")

	// ErrCacheLimit means the cached decoded bytes exceed the cache budget.
	ErrCacheLimit = errors.New("netsec: cache size limit exceeded")
)

// Segment kinds.
const (
	segLiteral   = 1
	segReference = 2
)

// Segment is one ordered piece of a Block. A segment either contributes raw
// literal bytes or references the full logical output of another block.
type Segment struct {
	kind int
	data []byte
	ref  int64
}

// Literal returns a segment emitting data verbatim.
func Literal(data []byte) Segment {
	return Segment{kind: segLiteral, data: data}
}

// Reference returns a segment that expands to the logical output of the block
// identified by id.
func Reference(id int64) Segment {
	return Segment{kind: segReference, ref: id}
}

// IsLiteral reports whether the segment carries raw bytes.
func (s Segment) IsLiteral() bool { return s.kind == segLiteral }

// IsReference reports whether the segment expands another block.
func (s Segment) IsReference() bool { return s.kind == segReference }

// Data returns the literal payload. It returns nil for reference segments.
func (s Segment) Data() []byte { return s.data }

// Ref returns the referenced block id. The result is undefined for literal
// segments.
func (s Segment) Ref() int64 { return s.ref }

// Block is one stored unit. Declared is the exact number of logical bytes the
// block produces after every reference is expanded.
type Block struct {
	ID       int64
	Declared int64
	Segments []Segment
}

// BlockStore supplies blocks by id during validation and on-demand decoding.
// Implementations must tolerate the sequential access pattern of a Reader.
type BlockStore interface {
	// Get returns the block identified by id. A nil block with a nil error is
	// treated as an unknown block.
	Get(ctx context.Context, id int64) (*Block, error)
}

// MapStore is a BlockStore backed by an in-memory map.
type MapStore map[int64]*Block

// Get implements BlockStore.
func (m MapStore) Get(_ context.Context, id int64) (*Block, error) {
	return m[id], nil
}

// Limits bounds the resources a Reader may consume. Non-positive fields are
// replaced with package defaults at Open.
type Limits struct {
	// MaxOutputBytes bounds the total logical output a Reader may return.
	MaxOutputBytes int64

	// MaxExpansions bounds the total number of reference expansions, counting
	// each expansion even when its target is served from the cache.
	MaxExpansions int64

	// MaxDepth bounds reference nesting depth. A root block sits at depth 1.
	MaxDepth int

	// MaxBlocks bounds the number of distinct blocks reachable from a root.
	MaxBlocks int

	// MaxCacheBytes bounds the total size of decoded blocks kept in the cache.
	MaxCacheBytes int64
}

// Default resource limits applied when a Limits field is non-positive.
const (
	defaultMaxOutputBytes = 64 << 20 // 64 MiB of logical output
	defaultMaxExpansions  = 1 << 20  // about 1M reference expansions
	defaultMaxDepth       = 64
	defaultMaxBlocks      = 4096
	defaultMaxCacheBytes  = 16 << 20 // 16 MiB of cached decoded bytes
)

// Account is a snapshot of the resources charged to a Reader.
type Account struct {
	// OutputBytes is the number of logical bytes charged to the output meter,
	// including bytes retained across cancellation.
	OutputBytes int64

	// Expansions is the number of reference expansions charged, including
	// expansions served from the cache.
	Expansions int64

	// CachedBytes is the total size of decoded blocks currently cached.
	CachedBytes int64

	// CacheEntries is the number of distinct decoded blocks currently cached.
	CacheEntries int

	// CacheHits counts expansions whose target block was already cached.
	CacheHits int64
}

// frame is the decode cursor for one block on the active block stack.
type frame struct {
	block       *Block
	seg         int   // current segment index; -1 before the first advance
	off         int   // offset inside the current segment
	left        int64 // bytes remaining in the current segment
	charged     bool  // current reference segment has been counted as an expansion
	needAdvance bool  // next loop iteration must pick the next segment
}

// Reader is an on-demand, cancellable decoder for a block reference graph.
// A Reader is not safe for concurrent use.
type Reader struct {
	store BlockStore
	lim   Limits

	ctx   context.Context
	stack []*frame

	cache      map[int64][]byte
	cachedSize int64
	cacheHits  int64
	sizes      map[int64]int64
	inflight   map[int64]bool

	outputBytes int64
	expansions  int64

	err    error
	closed bool

	// testHook, when non-nil, runs immediately after an expansion is charged
	// and before its target is resolved. It is a test-only synchronization
	// seam and is never set by production callers.
	testHook func(refID int64)
}

// addCapped returns a+b or an error if the addition overflows int64.
func addCapped(a, b int64) (int64, error) {
	sum := a + b
	if sum < a || sum < b {
		return 0, errors.New("size overflow")
	}
	return sum, nil
}
