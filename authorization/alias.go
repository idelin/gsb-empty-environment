// Package authorization is a compatibility spelling for the authorization
// solver.  It re-exports authsolve verbatim.
package authorization

import (
	"crypto/ed25519"

	core "prescreen.local/ps-e39099b0dc/authsolve"
)

type (
	Digest                   = core.Digest
	Device                   = core.Device
	Credential               = core.Credential
	ClockConstraint          = core.ClockConstraint
	Window                   = core.Window
	PolicyNode               = core.PolicyNode
	Problem                  = core.Problem
	Witness                  = core.Witness
	Result                   = core.Result
	FailureKind              = core.FailureKind
	FailureAtom              = core.FailureAtom
	ProofEdge                = core.ProofEdge
	InfeasibilityCertificate = core.InfeasibilityCertificate
	Proof                    = core.Proof
	AuthorizationProblem     = core.AuthorizationProblem
	AuthorizationWitness     = core.AuthorizationWitness
	CoverageProof            = core.CoverageProof
	ClockBound               = core.ClockBound
	Bound                    = core.Bound
	ClockDelta               = core.ClockDelta
	Evidence                 = core.Evidence
	ReleaseWindow            = core.ReleaseWindow
	ThresholdNode            = core.ThresholdNode
	ThresholdTree            = core.ThresholdTree
	CredentialSet            = core.CredentialSet
)

const (
	FailureLeft  = core.FailureLeft
	FailureGap   = core.FailureGap
	FailureRight = core.FailureRight
	FailureEmpty = core.FailureEmpty
)

var (
	ErrInvalidInput       = core.ErrInvalidInput
	ErrClockInconsistent  = core.ErrClockInconsistent
	ErrWitnessNotRobust   = core.ErrWitnessNotRobust
	ErrNoWitness          = core.ErrNoWitness
	ErrSignature          = core.ErrSignature
	ErrSignedOverflow     = core.ErrSignedOverflow
	ErrClockContradiction = core.ErrClockContradiction
	ErrNotRobust          = core.ErrNotRobust
	ErrNonRobustWitness   = core.ErrNonRobustWitness
	ErrNoRobustWitness    = core.ErrNoRobustWitness
	ErrBadSignature       = core.ErrBadSignature
	ErrOverflow           = core.ErrOverflow
)

func Solve(p Problem) (Witness, error)              { return core.Solve(p) }
func SolveResult(p Problem) (Result, error)         { return core.SolveResult(p) }
func SolveAuthorization(p Problem) (Witness, error) { return core.SolveAuthorization(p) }
func Resolve(p Problem) (Witness, error)            { return core.Resolve(p) }
func Authorize(p Problem) (Witness, error)          { return core.Authorize(p) }
func Verify(p Problem, w Witness) error             { return core.Verify(p, w) }
func VerifyProof(p Problem, proof Proof) error      { return core.VerifyProof(p, proof) }
func CheckClockEvidence(p Problem) error            { return core.CheckClockEvidence(p) }
func CheckSignatures(p Problem) error               { return core.CheckSignatures(p) }
func CheckWitness(p Problem, w Witness) error       { return core.CheckWitness(p, w) }
func CheckCoverage(p Problem, proof Proof) error    { return core.CheckCoverage(p, proof) }
func Robust(p Problem, credentialIDs []string) (bool, error) {
	return core.Robust(p, credentialIDs)
}
func AtomCount(n int) int         { return core.AtomCount(n) }
func DigestOf(data []byte) Digest { return core.DigestOf(data) }
func SignCredential(priv ed25519.PrivateKey, c *Credential) error {
	return core.SignCredential(priv, c)
}
func SignCredentialMessage(priv ed25519.PrivateKey, c *Credential, message []byte) error {
	return core.SignCredentialMessage(priv, c, message)
}
