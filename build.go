package baselinepatch

// NewPatch builds and validates a patch from baseline, target and ops, filling
// in both digest fields. Use this when constructing edits by hand. The ops
// must apply cleanly or NewPatch returns an error.
func NewPatch(baseline, target map[string]any, ops []Op) (*Patch, error) {
	if baseline == nil {
		return nil, ErrMissingBaseline
	}
	if err := validateValue(baseline); err != nil {
		return nil, err
	}
	if err := validateValue(target); err != nil {
		return nil, err
	}
	bd, err := Digest(baseline)
	if err != nil {
		return nil, err
	}
	td, err := Digest(target)
	if err != nil {
		return nil, err
	}
	p := &Patch{BaselineDigest: bd, TargetDigest: td, Ops: ops}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	// Confirm the operations really connect the two documents.
	if _, err := Apply(baseline, p); err != nil {
		return nil, err
	}
	return p, nil
}
