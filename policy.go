package netsec

// Policy is the admission policy applied to an SLSA v1 statement.
//
// TrustedBuilders is an exact lookup table: the map key must equal
// builder.id byte-for-byte, and the map value is the only buildType accepted
// for that builder. Prefix matching is intentionally not supported.
//
// AllowedExternalParameters is the complete set of top-level keys permitted in
// buildDefinition.externalParameters. It is an allow-list, not a required
// list; an empty slice means that no external parameter keys are accepted.
//
// SourceCommit must appear as a value in the digest of at least one resolved
// dependency. If SourceRepository is non-empty, the matching dependency's URI
// must also identify that repository.
type Policy struct {
	TrustedBuilders           map[string]string
	AllowedExternalParameters []string
	SourceCommit              string
	SourceRepository          string

	// RequireStatementTypes additionally requires the exact in-toto statement
	// and SLSA provenance type values. When false, an omitted type is
	// tolerated but a present, incorrect type is always rejected.
	RequireStatementTypes bool
}

// NewPolicy constructs a policy and copies the trusted table and allow-list so
// later mutation by the caller cannot change admission decisions.
func NewPolicy(trustedBuilders map[string]string, allowedExternalParameters []string, sourceCommit string) Policy {
	return Policy{
		TrustedBuilders:           cloneStringMap(trustedBuilders),
		AllowedExternalParameters: append([]string(nil), allowedExternalParameters...),
		SourceCommit:              sourceCommit,
	}
}

// Verify checks a decoded statement against the policy.
func (p Policy) Verify(statement *Statement) error {
	return Verify(statement, p)
}

// VerifyJSON parses and checks a JSON statement against the policy.
func (p Policy) VerifyJSON(data []byte) error {
	return VerifyJSON(data, p)
}

// Verifier applies a fixed admission policy. It is safe for concurrent use
// because the policy is copied on construction and verification does not
// mutate it.
type Verifier struct {
	policy Policy
}

// NewVerifier returns a verifier for policy.
func NewVerifier(policy Policy) *Verifier {
	return &Verifier{policy: clonePolicy(policy)}
}

// Policy returns the verifier's policy.
func (v *Verifier) Policy() Policy {
	return clonePolicy(v.policy)
}

// Verify checks a decoded statement using the verifier's policy.
func (v *Verifier) Verify(statement *Statement) error {
	return Verify(statement, v.policy)
}

// VerifyJSON parses and checks a JSON statement using the verifier's policy.
func (v *Verifier) VerifyJSON(data []byte) error {
	return VerifyJSON(data, v.policy)
}

func clonePolicy(policy Policy) Policy {
	policy.TrustedBuilders = cloneStringMap(policy.TrustedBuilders)
	policy.AllowedExternalParameters = append([]string(nil), policy.AllowedExternalParameters...)
	return policy
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
