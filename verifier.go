package stlf

// Verifier is a reusable host-facing wrapper around a fixed host and program.
type Verifier struct {
	Host Host
	Code []Instruction

	SearchOptions SearchOptions
	VerifyOptions VerifyOptions
}

// NewVerifier validates and binds a host/program pair.
func NewVerifier(host Host, code []Instruction) (*Verifier, error) {
	if err := Validate(host, code); err != nil {
		return nil, err
	}
	return &Verifier{Host: host, Code: code}, nil
}

// NewVerifierFromSource parses assembly and binds it to host.
func NewVerifierFromSource(host Host, src string) (*Verifier, error) {
	code, err := ParseProgram(src)
	if err != nil {
		return nil, err
	}
	return NewVerifier(host, code)
}

// FindWitness searches for a speculative STLF witness.
func (v *Verifier) FindWitness(opts ...SearchOptions) SearchResult {
	opt := v.SearchOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	return FindWitness(v.Host, v.Code, opt)
}

// Analyze is an alias for FindWitness.
func (v *Verifier) Analyze(opts ...SearchOptions) SearchResult {
	return v.FindWitness(opts...)
}

// Execute runs one input.
func (v *Verifier) Execute(in Input, opts ...RunOptions) Execution {
	return Execute(v.Host, v.Code, in, opts...)
}

// Reference returns the complete architectural reference state for one input.
func (v *Verifier) Reference(in Input, opts ...RunOptions) (MachineState, error) {
	return ReferenceRun(v.Host, v.Code, in, opts...)
}

// VerifyPatch checks a proposed barrier patch.
func (v *Verifier) VerifyPatch(patch Patch, opts ...VerifyOptions) PatchReport {
	opt := v.VerifyOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	return VerifyBarrierPatch(v.Host, v.Code, patch, opt)
}

// MinimalPatch finds and verifies a minimum-size barrier patch.
func (v *Verifier) MinimalPatch(opts ...PatchSearchOptions) (Patch, PatchReport) {
	opt := PatchSearchOptions{VerifyOptions: v.VerifyOptions}
	if len(opts) > 0 {
		opt = opts[0]
	}
	return FindMinimalBarrierPatch(v.Host, v.Code, opt)
}
