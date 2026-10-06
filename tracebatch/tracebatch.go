// Package tracebatch tracks batches of material through mixing, splitting
// (repackaging), and scrapping operations using exact rational arithmetic,
// and propagates contamination marks along actual source contributions.
//
// Every batch carries a symbolic source vector: a map from origin batch ID
// to the exact rational mass of that origin it currently holds. Contamination
// queries fold these vectors over the batch DAG in topological (creation)
// order, so shared paths are counted exactly once. Zero contributions never
// create edges and never propagate marks. The graph is kept acyclic by
// construction (edges only ever point into freshly created batches) and is
// additionally guarded by explicit cycle checks.
package tracebatch

import (
	"errors"
	"math/big"
)

var (
	ErrEmptyID             = errors.New("tracebatch: empty batch id")
	ErrBatchExists         = errors.New("tracebatch: batch already exists")
	ErrUnknownBatch        = errors.New("tracebatch: unknown batch")
	ErrBatchScrapped       = errors.New("tracebatch: batch has been scrapped")
	ErrNonPositiveQuantity = errors.New("tracebatch: quantity must be positive")
	ErrInsufficientQty     = errors.New("tracebatch: insufficient remaining quantity")
	ErrDuplicateSource     = errors.New("tracebatch: duplicate source in operation")
	ErrNoInputs            = errors.New("tracebatch: operation needs at least one input")
	ErrCycle               = errors.New("tracebatch: operation would create a cycle")
)

// Contribution is one rational input of a mixing operation.
type Contribution struct {
	Source string
	Amount *big.Rat
}

// SplitPart is one rational output of a split (repackage) operation.
type SplitPart struct {
	ID     string
	Amount *big.Rat
}

// Affected reports the fraction of a downstream batch's mass that is
// attributable to a queried contamination source.
type Affected struct {
	BatchID    string
	Proportion *big.Rat
	Scrapped   bool
}

type inEdge struct {
	from string
	amt  *big.Rat
}

type outEdge struct {
	to  string
	amt *big.Rat
}

// Batch is a node in the traceability DAG.
type Batch struct {
	id      string
	qty     *big.Rat            // remaining quantity
	initQty *big.Rat            // quantity at creation
	comp    map[string]*big.Rat // origin id -> remaining mass from that origin
	live    bool                // false once scrapped
	inputs  []inEdge
}

// Ledger records all batch operations and answers contamination queries.
type Ledger struct {
	batches  map[string]*Batch
	order    []string // creation order; a valid topological order
	children map[string][]outEdge
	initial  map[string]*big.Rat // origin id -> registered quantity
	scrapped map[string]*big.Rat
}
