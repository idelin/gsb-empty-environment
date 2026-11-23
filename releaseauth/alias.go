// Package releaseauth is the release-host compatibility spelling for
// authsolve.
package releaseauth

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
)

const (
	FailureLeft  = core.FailureLeft
	FailureGap   = core.FailureGap
	FailureRight = core.FailureRight
	FailureEmpty = core.FailureEmpty
)

var (
	ErrInvalidInput      = core.ErrInvalidInput
	ErrClockInconsistent = core.ErrClockInconsistent
	ErrWitnessNotRobust  = core.ErrWitnessNotRobust
	ErrNoWitness         = core.ErrNoWitness
	ErrSignature         = core.ErrSignature
	ErrSignedOverflow    = core.ErrSignedOverflow
)

func Solve(p Problem) (Witness, error)              { return core.Solve(p) }
func SolveResult(p Problem) (Result, error)         { return core.SolveResult(p) }
func SolveAuthorization(p Problem) (Witness, error) { return core.SolveAuthorization(p) }
func Verify(p Problem, w Witness) error             { return core.Verify(p, w) }
func VerifyProof(p Problem, proof Proof) error      { return core.VerifyProof(p, proof) }
func SignCredential(priv ed25519.PrivateKey, c *Credential) error {
	return core.SignCredential(priv, c)
}
