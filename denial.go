package fieldauth

import (
	"fmt"
	"strings"
)

// Failure is a single failed condition of a triggered rule.
type Failure struct {
	Rule string // rule ID
	Cond string // description of the failed condition
	Src  Source // which record image the condition reads
}

func (f Failure) String() string {
	return fmt.Sprintf("rule %q: %s (%s condition)", f.Rule, f.Cond, f.Src)
}

// Denial explains why an atomic update was rejected. It is minimal: it
// lists exactly the failed conditions (nothing that passed), the guarded
// fields involved, and the smallest set of patch fields that would have
// to be reverted for the update to pass.
type Denial struct {
	// Failures holds every failed condition, deduplicated.
	Failures []Failure

	triggered map[string][]string // failed rule ID -> changed guarded fields
	fields    []string            // union of triggered fields, sorted
	minimal   []string            // patch fields to revert, sorted
}

// Fields returns the changed fields guarded by the failed rules
// (including derived fields that changed as a consequence of the patch).
func (d *Denial) Fields() []string {
	out := make([]string, len(d.fields))
	copy(out, d.fields)
	return out
}

// MinimalFields returns the minimal set of patch fields that must be
// reverted for the update to pass. Derived-field violations are mapped
// back to the patched base fields that caused them.
func (d *Denial) MinimalFields() []string {
	out := make([]string, len(d.minimal))
	copy(out, d.minimal)
	return out
}

func (d *Denial) Error() string {
	parts := make([]string, len(d.Failures))
	for i, f := range d.Failures {
		parts[i] = f.String()
	}
	return fmt.Sprintf("fieldauth: update rejected (%s); revert fields: %s",
		strings.Join(parts, "; "), strings.Join(d.minimal, ", "))
}

func (d *Denial) finalize(e *Engine, patch Patch) {
	fields := map[string]bool{}
	minimal := map[string]bool{}
	for _, trig := range d.triggered {
		for _, f := range trig {
			fields[f] = true
			e.baseCauses(f, patch, minimal)
		}
	}
	d.fields = sortedKeys(fields)
	d.minimal = sortedKeys(minimal)
}
