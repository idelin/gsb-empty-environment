package oci

// ValidateLayer merges exactly one layer and validates its resulting snapshot.
func ValidateLayer(layer Layer, policies ...any) (*Report, *FileSystem, error) {
	fs, err := Merge(layer)
	if err != nil {
		return nil, nil, err
	}
	if len(policies) == 0 {
		policies = []any{DefaultPolicy()}
	}
	report, err := Validate(fs, policies...)
	return report, fs, err
}

// CheckLayer is the slice-free helper for one layer.
func CheckLayer(layer Layer, policy any) ([]Finding, *FileSystem, error) {
	report, fs, err := ValidateLayer(layer, policy)
	if err != nil {
		return nil, fs, err
	}
	return report.Findings, fs, nil
}
