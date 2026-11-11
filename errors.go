package wafdecision

import "fmt"

// Sentinel errors returned during compilation or verification.
var (
	// ErrNilFormula is returned when a formula or a required sub-formula is nil.
	ErrNilFormula = fmt.Errorf("wafdecision: nil formula")

	// ErrNoLeaves is returned when a compiled rule references no detector leaf.
	ErrNoLeaves = fmt.Errorf("wafdecision: formula references no leaves")

	// ErrTooManyLeaves is returned when a rule references more than MaxLeaves
	// distinct detector leaves.
	ErrTooManyLeaves = fmt.Errorf("wafdecision: more than 12 leaves")
)

// LeafError describes a problem with a LeafSpec entry.
type LeafError struct {
	LeafID int
	Reason string
}

func (e *LeafError) Error() string {
	return fmt.Sprintf("wafdecision: leaf %d: %s", e.LeafID, e.Reason)
}

// FormulaError describes a malformed formula node.
type FormulaError struct {
	Reason string
}

func (e *FormulaError) Error() string {
	return "wafdecision: " + e.Reason
}

// UnsupportedFormulaError is returned when a Formula value is not one of
// *Leaf, *Not, *And or *Or.
type UnsupportedFormulaError struct {
	Type string
}

func (e *UnsupportedFormulaError) Error() string {
	return "wafdecision: unsupported formula type " + e.Type
}

// VerifyError explains a failed exhaustive verification.
type VerifyError struct {
	Reason string
}

func (e *VerifyError) Error() string {
	return "wafdecision: verify: " + e.Reason
}

func typeName(f Formula) string {
	if f == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%T", f)
}
