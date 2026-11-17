package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"math"
)

// Sentinel errors returned by the expansion budget.
var (
	ErrBudgetExceeded = errors.New("netsec: expansion budget exceeded")
	ErrDepthExceeded  = errors.New("netsec: maximum path depth exceeded")
	ErrLeavesExceeded = errors.New("netsec: maximum leaf count exceeded")
	ErrLiveExceeded   = errors.New("netsec: maximum live buffer exceeded")
	ErrCycle          = errors.New("netsec: cyclic container tree")
	ErrInvalidTree    = errors.New("netsec: invalid container tree")
	ErrInvalidConfig  = errors.New("netsec: invalid expansion configuration")
	ErrWriterClosed   = errors.New("netsec: decode writer is closed")
)

// ErrLiveBufferExceeded is an alias for ErrLiveExceeded.
var ErrLiveBufferExceeded = ErrLiveExceeded

func saturatingAdd(a, b int64) int64 {
	if b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// Dimension identifies the budget that rejected an operation.
type Dimension string

const (
	DimensionDecodedBytes Dimension = "decoded_bytes"
	DimensionDepth        Dimension = "depth"
	DimensionLeaves       Dimension = "leaves"
	DimensionLiveBuffer   Dimension = "live_buffer"
)

// BudgetError describes a rejected budget reservation. Every BudgetError
// matches ErrBudgetExceeded; dimension-specific sentinel errors also match.
type BudgetError struct {
	Dimension Dimension
	Limit     int64
	Attempted int64
	Node      string
}

func (e *BudgetError) Error() string {
	name := ""
	if e.Node != "" {
		name = fmt.Sprintf(" at node %q", e.Node)
	}
	return fmt.Sprintf("%s: %s limit %d, attempted %d%s",
		ErrBudgetExceeded, e.Dimension, e.Limit, e.Attempted, name)
}

// Unwrap makes every budget failure match ErrBudgetExceeded.
func (e *BudgetError) Unwrap() error { return ErrBudgetExceeded }

// Is additionally makes the failure match its dimension-specific sentinel.
func (e *BudgetError) Is(target error) bool {
	switch target {
	case ErrDepthExceeded:
		return e.Dimension == DimensionDepth
	case ErrLeavesExceeded:
		return e.Dimension == DimensionLeaves
	case ErrLiveExceeded:
		return e.Dimension == DimensionLiveBuffer
	default:
		return false
	}
}

// Budget bounds every resource consumed by an expansion.
//
// MaxDepth counts nodes on a root-to-leaf path, with a root at depth 1. A
// value of 0 permits no nodes. MaxLeaves counts terminal-node references,
// including empty containers and repeated references to shared nodes.
type Budget struct {
	MaxDecodedBytes int64
	MaxDepth        int
	MaxLeaves       int64
	MaxLiveBuffer   int64
}

func (b Budget) validate() error {
	switch {
	case b.MaxDecodedBytes < 0:
		return fmt.Errorf("%w: MaxDecodedBytes must not be negative", ErrInvalidConfig)
	case b.MaxDepth < 0:
		return fmt.Errorf("%w: MaxDepth must not be negative", ErrInvalidConfig)
	case b.MaxLeaves < 0:
		return fmt.Errorf("%w: MaxLeaves must not be negative", ErrInvalidConfig)
	case b.MaxLiveBuffer < 0:
		return fmt.Errorf("%w: MaxLiveBuffer must not be negative", ErrInvalidConfig)
	default:
		return nil
	}
}

// Stage is the staging buffer used for one stream leaf. Implementations are
// supplied only for controlled testing or specialized storage. The expansion
// engine calls Reset after any failed expansion and takes ownership of the
// slice returned by Bytes after a successful one.
//
// A Stage must implement the normal io.Writer contract. Returning a short
// write is treated as an expansion failure.
type Stage interface {
	Write(p []byte) (int, error)
	Bytes() []byte
	Reset()
}

// StageFactory creates a fresh staging buffer for a leaf. label is the node
// name and is intended for diagnostics.
type StageFactory func(label string) Stage

type bufferStage struct {
	buf bytes.Buffer
}

func (b *bufferStage) Write(p []byte) (int, error) { return b.buf.Write(p) }
func (b *bufferStage) Bytes() []byte               { return b.buf.Bytes() }
func (b *bufferStage) Reset()                      { b.buf.Reset() }

func defaultStageFactory(string) Stage { return &bufferStage{} }
