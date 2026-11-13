package netsec

// Verifier is a reusable verifier configuration for components that prefer a
// receiver over passing Options on every call.
type Verifier struct {
	Algorithm        HashAlgorithm
	AllowSparse      bool
	RequireAll       bool
	IgnoreIndexHints bool
}

// NewVerifier returns a verifier with the safe default policy.
func NewVerifier() *Verifier {
	return &Verifier{}
}

// NewVerifierWithAlgorithm returns a verifier using algorithm.
func NewVerifierWithAlgorithm(algorithm HashAlgorithm) *Verifier {
	return &Verifier{Algorithm: algorithm}
}

// VerifyArray verifies indexed disclosures.
func (v *Verifier) VerifyArray(commitment any, disclosures any) ([]any, error) {
	return VerifyArrayWithOptions(commitment, disclosures, v.options())
}

// Verify is an alias for VerifyArray.
func (v *Verifier) Verify(commitment any, disclosures any) ([]any, error) {
	return v.VerifyArray(commitment, disclosures)
}

// VerifyDisclosures verifies raw encoded disclosures without index claims.
func (v *Verifier) VerifyDisclosures(commitment any, disclosures any) ([]any, error) {
	options := v.options()
	options.IgnoreIndexHints = true
	return VerifyArrayWithOptions(commitment, disclosures, options)
}

func (v *Verifier) options() Options {
	if v == nil {
		return Options{}
	}
	return Options{
		Algorithm:        v.Algorithm,
		AllowSparse:      v.AllowSparse,
		RequireAll:       v.RequireAll,
		IgnoreIndexHints: v.IgnoreIndexHints,
	}
}
