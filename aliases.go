package impact

// Analyzer is an alias for Engine, matching the common "analyzer" naming used
// by build hosts.
type Analyzer = Engine

// Impact is an alias for Analysis.
type Impact = Analysis

// ImpactResult is an alias for Result.
type ImpactResult = Result

// CallSummary is an alias for Summary.
type CallSummary = Summary

// Revocation is an alias for ObjectID.
type Revocation = ObjectID

// NewAnalyzer is an alias for NewEngine.
func NewAnalyzer(snapshot *Snapshot, objects ...ObjectID) (*Analyzer, error) {
	return NewEngine(snapshot, objects...)
}

// NewAnalyzerWithRevocations is an alias for NewEngineWithRevocations.
func NewAnalyzerWithRevocations(snapshot *Snapshot, objects ...ObjectID) (*Analyzer, error) {
	return NewEngineWithRevocations(snapshot, objects...)
}

// AnalyzeImpact is an alias for Analyze.
func AnalyzeImpact(snapshot *Snapshot, revoked []ObjectID) (*Analysis, error) {
	return Analyze(snapshot, revoked)
}

// RevokeSet is an alias for MustRevoke.
func (r Result) RevokeSet() BDD { return r.MustRevoke() }
