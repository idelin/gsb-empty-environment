package netsec

import "errors"

// Sentinel errors returned while validating a graph or completing a bundle.
// Callers should use errors.Is to match them; the typed errors below carry
// the offending step or digest as additional context.
var (
	ErrEmptyGraph        = errors.New("witness: step graph is empty")
	ErrEmptyStepID       = errors.New("witness: step id is empty")
	ErrGraphCycle        = errors.New("witness: step graph contains a cycle")
	ErrEmptyBundle       = errors.New("witness: bundle contains no proofs")
	ErrNilVerifier       = errors.New("witness: verifier is nil")
	ErrInvalidProof      = errors.New("witness: proof failed validation")
	ErrBadSignature      = errors.New("witness: signature verification failed")
	ErrOpMismatch        = errors.New("witness: proofs bind different operation digests")
	ErrSessionMismatch   = errors.New("witness: proofs bind different sessions")
	ErrReplayedProof     = errors.New("witness: proof digest was already consumed")
	ErrMultipleRoots     = errors.New("witness: bundle has more than one root proof")
	ErrMissingRoot       = errors.New("witness: bundle has no root proof")
	ErrBrokenChain       = errors.New("witness: parent digest does not resolve to a bundle proof")
	ErrForkedChain       = errors.New("witness: a proof has more than one successor, chains cannot be merged")
	ErrOrphanProof       = errors.New("witness: proof is not reachable from the root")
	ErrInvalidWindow     = errors.New("witness: proof has an invalid validity window")
	ErrNoWindowOverlap   = errors.New("witness: proof validity windows have no common intersection")
	ErrNotEffective      = errors.New("witness: bundle is not effective at the given time")
	ErrUnknownStep       = errors.New("witness: proof references a step not in the graph")
	ErrDuplicateStep     = errors.New("witness: step is witnessed more than once")
	ErrMissingStep       = errors.New("witness: required step is not covered")
	ErrEdgeOrderViolated = errors.New("witness: prerequisite step appears after its successor")
)

type stepError struct {
	cause error
	step  string
}

func (e *stepError) Error() string { return e.cause.Error() + ": " + e.step }
func (e *stepError) Unwrap() error { return e.cause }

type edgeError struct {
	cause              error
	step, prerequisite string
}

func (e *edgeError) Error() string {
	return e.cause.Error() + ": " + e.step + " requires " + e.prerequisite
}
func (e *edgeError) Unwrap() error { return e.cause }

type digestError struct {
	cause  error
	digest Digest
}

func (e *digestError) Error() string { return e.cause.Error() + ": " + e.digest.Hex() }
func (e *digestError) Unwrap() error { return e.cause }

func duplicateStepError(step string) error { return &stepError{cause: ErrDuplicateStep, step: step} }
func unknownStepError(step string) error   { return &stepError{cause: ErrUnknownStep, step: step} }
func missingStepError(step string) error   { return &stepError{cause: ErrMissingStep, step: step} }
func unknownEdgeStepError(step string) error {
	return &stepError{cause: ErrUnknownStep, step: step}
}
func unknownPrereqError(step, prereq string) error {
	return &edgeError{cause: ErrUnknownStep, step: step, prerequisite: prereq}
}
func selfLoopError(step string) error {
	return &stepError{cause: ErrGraphCycle, step: step}
}
func edgeOrderError(step, prereq string) error {
	return &edgeError{cause: ErrEdgeOrderViolated, step: step, prerequisite: prereq}
}
func digestContextError(cause error, digest Digest) error {
	return &digestError{cause: cause, digest: digest}
}
