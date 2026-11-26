package stlf

// Common aliases to keep the package easy to embed in verification hosts.
type (
	// Program is an alias for an instruction slice.
	Program = []Instruction
	// LeakWitness is an alias for Witness.
	LeakWitness = Witness
	// SearchReport is an alias for SearchResult.
	SearchReport = SearchResult
	// BarrierPatch is an alias for Patch.
	BarrierPatch = Patch
	// VerificationReport is an alias for PatchReport.
	VerificationReport = PatchReport
)

const (
	// MaxDepth is an alias for MaxMispredictDepth.
	MaxDepth = MaxMispredictDepth
)

// DefaultHost is an alias for ExampleHost.
func DefaultHost() Host { return ExampleHost() }

// DefaultProgram is an alias for ExampleProgram.
func DefaultProgram() []Instruction { return ExampleProgram() }

// DefaultScenario is an alias for ExampleScenario.
func DefaultScenario() Scenario { return ExampleScenario() }

// Detect is an alias for FindWitness.
func Detect(host Host, code []Instruction, opts ...SearchOptions) SearchResult {
	return FindWitness(host, code, opts...)
}

// CheckPatch is an alias for VerifyBarrierPatch.
func CheckPatch(host Host, code []Instruction, patch Patch, opts ...VerifyOptions) PatchReport {
	return VerifyBarrierPatch(host, code, patch, opts...)
}

// MinimalBarrierPatch is an alias for FindMinimalBarrierPatch.
func MinimalBarrierPatch(host Host, code []Instruction, opts ...PatchSearchOptions) (Patch, PatchReport) {
	return FindMinimalBarrierPatch(host, code, opts...)
}

// Assemble is an alias for ParseProgram.
func Assemble(src string) ([]Instruction, error) { return ParseProgram(src) }

// MustAssemble is an alias for MustParseProgram.
func MustAssemble(src string) []Instruction { return MustParseProgram(src) }

// RunReference is an alias for ReferenceRun.
func RunReference(host Host, code []Instruction, in Input, opts ...RunOptions) (MachineState, error) {
	return ReferenceRun(host, code, in, opts...)
}

// FindWitness searches this scenario.
func (s Scenario) FindWitness(opts ...SearchOptions) SearchResult {
	return FindWitness(s.Host, s.Code, opts...)
}

// VerifyPatch checks a barrier patch for this scenario.
func (s Scenario) VerifyPatch(patch Patch, opts ...VerifyOptions) PatchReport {
	return VerifyBarrierPatch(s.Host, s.Code, patch, opts...)
}

// MinimalPatch finds a minimal barrier patch for this scenario.
func (s Scenario) MinimalPatch(opts ...PatchSearchOptions) (Patch, PatchReport) {
	return FindMinimalBarrierPatch(s.Host, s.Code, opts...)
}
