package seccomp

// NewPolicy builds a Policy from rules.
func NewPolicy(rules ...Rule) *Policy { return &Policy{Rules: rules} }

// Verifier bundles a policy with reusable verification options.
type Verifier struct {
	Policy  *Policy
	Options []Option
}

// NewVerifier constructs a Verifier.
func NewVerifier(policy *Policy, options ...Option) *Verifier {
	return &Verifier{Policy: policy, Options: options}
}

// Verify checks one filter.
func (v *Verifier) Verify(insns []Instruction) (*Report, error) {
	return Verify(insns, v.Policy, v.Options...)
}

// VerifyStack checks a filter stack.
func (v *Verifier) VerifyStack(filters [][]Instruction) (*StackReport, error) {
	return VerifyStack(filters, v.Policy, v.Options...)
}

// ValidateStrict runs Validate with seccomp-strict options: aligned ABS
// loads and rejection of dynamic BPF_RET|BPF_A actions (whose action class
// cannot be proven legal statically).
func ValidateStrict(insns []Instruction) error {
	opts := DefaultValidateOptions()
	opts.RequireAlignedABS = true
	opts.RejectDynamicReturn = true
	return ValidateWith(insns, opts)
}

// Parse is an alias for Decode (real 8-byte sock_filter array).
func Parse(b []byte) ([]Instruction, error) { return Decode(b) }

// DecodeSockFilters is a descriptive alias for Decode.
func DecodeSockFilters(b []byte) ([]Instruction, error) { return Decode(b) }

// VerifyBytes decodes a concatenated 8-byte sock_filter array and verifies it.
func VerifyBytes(raw []byte, policy *Policy, options ...Option) (*Report, error) {
	insns, err := Decode(raw)
	if err != nil {
		return nil, err
	}
	return Verify(insns, policy, options...)
}

// Audit is a descriptive alias for Verify.
func Audit(insns []Instruction, policy *Policy, options ...Option) (*Report, error) {
	return Verify(insns, policy, options...)
}

// Check is a short alias for Verify.
func Check(insns []Instruction, policy *Policy, options ...Option) (*Report, error) {
	return Verify(insns, policy, options...)
}

// Bypass / Finding / Vulnerability are aliases for Counterexample.
type (
	Bypass        = Counterexample
	Finding       = Counterexample
	Vulnerability = Counterexample
	AuditReport   = Report
	ForbiddenRule = Rule
	SyscallRule   = Rule
)
