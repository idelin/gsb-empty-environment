package netsec

import "errors"

var (
	// ErrInvalidConfig reports an export specification that violates the
	// structural constraints (bad column counts, negative costs, unknown
	// record values, mismatched row widths).
	ErrInvalidConfig = errors.New("netsec: invalid anonymization configuration")

	// ErrNoFeasiblePublication reports that no level vector satisfies the
	// k-anonymity and suppression-budget constraints. The export must be
	// refused; Anonymize returns this sentinel together with a non-nil
	// Result carrying the refusal evidence.
	ErrNoFeasiblePublication = errors.New("netsec: no feasible anonymous publication")
)

// Record is one input row. Quasi holds one value per quasi-identifier column,
// each value a leaf/node identifier of that column's taxonomy.
type Record struct {
	ID    string
	Quasi []string
}

// Config is an export request.
//
// Taxonomies and LevelCosts are per column; LevelCosts[col][level] is the
// information-loss price of generalizing that column to level. Each slice must
// span levels 0..tree height and contain non-negative values.
// SuppressionCost is charged once per suppressed record. K is the minimum
// published group size; MaxSuppressed is the suppression budget s.
type Config struct {
	Taxonomies      []*Taxonomy
	LevelCosts      [][]int64
	SuppressionCost int64
	K               int
	MaxSuppressed   int
	Records         []Record
}

// Group is the equivalence class induced by one generalized tuple.
type Group struct {
	// Key is the generalized tuple, one node identifier per column.
	Key []string
	// RecordIDs lists the input records sharing Key, in input order.
	RecordIDs []string
	// Suppressed is true when the group holds fewer than K records and is
	// therefore withheld in full.
	Suppressed bool
}

// SearchStats records how the pruned search explored the level lattice.
type SearchStats struct {
	// LatticePoints is the total number of admissible level vectors.
	LatticePoints int
	// Evaluated is the number of points whose grouping was computed.
	Evaluated int
	// BoundPruned is the number of points skipped because every descendant
	// had a level cost strictly above the incumbent total cost.
	BoundPruned int
}

// CrossCheck captures the mandatory full-lattice enumeration used to verify
// the pruned search.
type CrossCheck struct {
	// Matched is true only when enumeration independently recovered the same
	// optimum cost and tie-broken level vector as the pruned search.
	Matched bool
	// Enumerated is the number of level points visited without pruning.
	Enumerated int
	// Detail explains a mismatch; empty when Matched is true.
	Detail string
}

// Evidence documents the optimality claim.
type Evidence struct {
	// BestLevel is the selected generalization level per column.
	BestLevel []int
	// LevelCost is sum of LevelCosts[col][BestLevel[col]].
	LevelCost int64
	// Suppressed is the number of withheld records.
	Suppressed int
	// SuppressionCost is Suppressed times Config.SuppressionCost.
	SuppressionCost int64
	// TotalCost is LevelCost + SuppressionCost.
	TotalCost int64
	// Stats describes the pruned lattice search.
	Stats SearchStats
	// CrossCheck is the independent full-enumeration verification.
	CrossCheck CrossCheck
}

// Result is an anonymization outcome. When Anonymize returns
// ErrNoFeasiblePublication, Published is false, Groups and Level are empty,
// and Evidence records why the export was refused.
type Result struct {
	// Published is false when the export is refused.
	Published bool
	// Level is the selected level vector (empty on refusal).
	Level []int
	// Groups covers every input record partitioned by generalized tuple,
	// sorted by generalized key; suppressed groups carry Suppressed=true.
	Groups []Group
	// PublishedGroups contains only the released equivalence classes.
	PublishedGroups []Group
	// SuppressedRecordIDs lists the records withheld from publication.
	SuppressedRecordIDs []string
	Evidence            Evidence
}
