package oci

// Validator bundles a policy for repeated snapshot validation.
type Validator struct {
	Policy any
}

// NewValidator creates a validator. A nil policy selects DefaultPolicy.
func NewValidator(policy any) *Validator { return &Validator{Policy: policy} }

// Validate checks one merged rootfs or intermediate snapshot.
func (v *Validator) Validate(fs *FileSystem) (*Report, error) {
	if v == nil {
		return Validate(fs)
	}
	return Validate(fs, v.Policy)
}

// Check returns findings without an error return.
func (v *Validator) Check(fs *FileSystem) []Finding {
	report, err := v.Validate(fs)
	if err != nil {
		return nil
	}
	return report.Findings
}

// ValidateSnapshots checks all supplied intermediate snapshots.
func (v *Validator) ValidateSnapshots(snapshots []*FileSystem) (*Report, error) {
	if v == nil {
		return ValidateSnapshots(snapshots)
	}
	return ValidateSnapshots(snapshots, v.Policy)
}

// VerifyLayers merges and validates every post-layer snapshot.
func (v *Validator) VerifyLayers(layers []Layer) (*Report, *MergeResult, error) {
	if v == nil {
		return VerifyLayers(layers)
	}
	return VerifyLayers(layers, v.Policy)
}

// Scanner is the security-scanning facade alias for Validator.
type Scanner = Validator

// Analyzer is another facade alias for Validator.
type Analyzer = Validator

// NewScanner creates a Scanner.
func NewScanner(policy any) *Scanner { return NewValidator(policy) }

// NewAnalyzer creates an Analyzer.
func NewAnalyzer(policy any) *Analyzer { return NewValidator(policy) }

// Scan merges layers and validates every snapshot.
func (s *Scanner) Scan(layers []Layer) (*Report, *MergeResult, error) {
	return s.VerifyLayers(layers)
}

// ScanFileSystem validates an already merged file system.
func (s *Scanner) ScanFileSystem(fs *FileSystem) (*Report, error) { return s.Validate(fs) }
