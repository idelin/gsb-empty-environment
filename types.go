package taskpkg

import (
	"errors"
	"fmt"
)

// Block is one node in a content block tree.
//
// Role is the stable block type (for example "heading", "paragraph" or
// "section"). Text is the block's visible text. ID is optional, but setting a
// stable ID on both versions makes parent/move detection unambiguous.
//
// Ignore marks the whole subtree as caller-declared noise. Ignored blocks are
// never deleted, inserted or matched, and their text never contributes to an
// edit operation. This is the mechanism a crawler should use for rotating ads,
// timestamps and other regions that must not turn into body changes.
type Block struct {
	ID       string
	Role     string
	Text     string
	Children []*Block
	Ignore   bool
}

// Node is an alias for Block.
type Node = Block

// TextSpan is a half-open [start, end) range in a document's plain text.
//
// Both rune and byte offsets are returned. Document offsets are measured over
// the concatenation of every block's Text in preorder, including ignored
// blocks, with no separator. This keeps intervals stable even when a caller
// removes an ignored ad region from the comparison set.
type TextSpan struct {
	RuneStart int
	RuneEnd   int
	ByteStart int
	ByteEnd   int
}

// Empty reports whether the span has zero width.
func (s TextSpan) Empty() bool {
	return s.RuneStart == s.RuneEnd
}

// Runes returns the half-open rune range.
func (s TextSpan) Runes() (start, end int) {
	return s.RuneStart, s.RuneEnd
}

// Bytes returns the half-open byte range.
func (s TextSpan) Bytes() (start, end int) {
	return s.ByteStart, s.ByteEnd
}

// Options controls the structural diff cost model and search budget.
//
// DeleteCost and InsertCost are charged per unmatched old/new block.
// RolePenalty is added when two matched blocks have different roles. A matched
// pair also pays the rune Levenshtein distance between their texts. MoveCost is
// added when the matched pair's parent relationship changed; such a pair is
// emitted as OpMove rather than as a delete plus an insert.
//
// MaxBlocks defaults to (and may not exceed) 40. Budget is the maximum number
// of branch-and-bound states; zero selects the default. Budget exhaustion is
// not an error: the best plan found is returned with Optimal == false.
type Options struct {
	DeleteCost  int
	InsertCost  int
	RolePenalty int
	MoveCost    int
	Budget      int
	MaxBlocks   int
}

// DefaultOptions returns a general-purpose cost model. Callers may override
// any field; zero values in a manually constructed Options are meaningful
// costs, not defaults.
func DefaultOptions() Options {
	return Options{
		DeleteCost:  10,
		InsertCost:  10,
		RolePenalty: 2,
		MoveCost:    4,
		Budget:      2_000_000,
		MaxBlocks:   MaxBlocks,
	}
}

// MaxBlocks is the hard input limit from the pipeline contract.
const MaxBlocks = 40

const defaultBudget int = 2_000_000

// OpKind identifies one step in an edit plan.
type OpKind string

const (
	OpMatch  OpKind = "match"
	OpMove   OpKind = "move"
	OpDelete OpKind = "delete"
	OpInsert OpKind = "insert"
)

// Operation is one entry in a Plan.
//
// OldOrder and NewOrder are preorder indices among non-ignored blocks; they are
// -1 when the operation has no block on that side.
type Operation struct {
	Kind OpKind

	OldOrder int
	NewOrder int

	OldID   string
	NewID   string
	OldRole string
	NewRole string
	OldText string
	NewText string

	OldParentID string
	NewParentID string
	// Parent orders are preorder indices among non-ignored blocks, or -1 for a
	// root. They identify the parent relationship even when callers do not use
	// stable block IDs.
	OldParentOrder int
	NewParentOrder int

	Cost         int
	TextDistance int
	RolePenalty  int
	MovePenalty  int
	Moved        bool

	// OldSpan and NewSpan cover the entire old/new block text in their
	// respective document coordinate systems.
	OldSpan TextSpan
	NewSpan TextSpan

	// OriginalIntervals lists the ranges of the original document removed or
	// replaced by this operation. A pure text insertion into a matched block
	// yields one zero-width interval at the insertion point. Deletes report the
	// deleted block's full span.
	OriginalIntervals []TextSpan

	// NewIntervals is the analogous set of ranges in the new document.
	NewIntervals []TextSpan
}

// Stats summarizes the selected matching.
type Stats struct {
	OldBlocks int
	NewBlocks int
	// Ignored counts ignored nodes across both input trees.
	Ignored int
	// Matched counts all matched pairs, including the Moved subset.
	Matched  int
	Moved    int
	Deleted  int
	Inserted int
}

// Plan is the lowest-cost edit plan found, plus its optimality status.
type Plan struct {
	Operations []Operation
	TotalCost  int

	// Optimal is true only when the branch-and-bound search completed within
	// Budget. A false value with BudgetExhausted true means the returned plan is
	// valid but not proven minimal.
	Optimal         bool
	BudgetExhausted bool
	States          int
	Reason          string

	Stats Stats
}

var (
	// ErrTooManyBlocks is returned when a tree exceeds the 40-block contract.
	ErrTooManyBlocks = errors.New("taskpkg: too many blocks")
	// ErrInvalidTree is returned for nil children, shared nodes or cycles.
	ErrInvalidTree = errors.New("taskpkg: invalid block tree")
	// ErrInvalidOptions is returned for negative costs or budgets.
	ErrInvalidOptions = errors.New("taskpkg: invalid options")
)

func validateOptions(opts Options) (Options, error) {
	if opts.DeleteCost < 0 || opts.InsertCost < 0 ||
		opts.RolePenalty < 0 || opts.MoveCost < 0 {
		return opts, fmt.Errorf("%w: costs must be non-negative", ErrInvalidOptions)
	}
	if opts.Budget < 0 {
		return opts, fmt.Errorf("%w: budget must be non-negative", ErrInvalidOptions)
	}
	if opts.Budget == 0 {
		opts.Budget = defaultBudget
	}
	if opts.MaxBlocks <= 0 || opts.MaxBlocks > MaxBlocks {
		opts.MaxBlocks = MaxBlocks
	}
	return opts, nil
}
