package capgate

import (
	"io"

	inner "formal.local/h26-25236a386a/capgate"
)

// Phase aliases and lifecycle constants.
type Phase = inner.Phase

const (
	PhaseFetch       = inner.PhaseFetch
	PhasePreinstall  = inner.PhasePreinstall
	PhaseBuild       = inner.PhaseBuild
	PhasePostinstall = inner.PhasePostinstall
	PhasePublish     = inner.PhasePublish
)

// Cache modes.
type CacheMode = inner.CacheMode

const (
	CacheModeNone      = inner.CacheModeNone
	CacheModeRead      = inner.CacheModeRead
	CacheModeWrite     = inner.CacheModeWrite
	CacheModeReadWrite = inner.CacheModeReadWrite
)

// Core input and output types.
type (
	Assignment            = inner.Assignment
	Expr                  = inner.Expr
	HalfRef               = inner.HalfRef
	Grant                 = inner.Grant
	Edge                  = inner.Edge
	Graph                 = inner.Graph
	InstanceKey           = inner.InstanceKey
	PrivilegeUnion        = inner.PrivilegeUnion
	SignatureVerifier     = inner.SignatureVerifier
	SignatureVerifierFunc = inner.SignatureVerifierFunc
	Policy                = inner.Policy
	Literal               = inner.Literal
	Cube                  = inner.Cube
	ConditionBlock        = inner.ConditionBlock
	ChainStep             = inner.ChainStep
	ChainBranch           = inner.ChainBranch
	GrantChain            = inner.GrantChain
	ReplayResult          = inner.ReplayResult
	AssignmentCoverage    = inner.AssignmentCoverage
	CoverageProof         = inner.CoverageProof
	DiffReport            = inner.DiffReport
	AddedInstance         = inner.AddedInstance
	PrimeImplicantSet     = inner.PrimeImplicantSet
	DiffStats             = inner.DiffStats
	GateFailure           = inner.GateFailure
)

// DefaultPolicy forwards capgate.DefaultPolicy.
func DefaultPolicy() Policy { return inner.DefaultPolicy() }

// ParseExpr forwards capgate.ParseExpr.
func ParseExpr(input string) (Expr, error) { return inner.ParseExpr(input) }

// OneHot forwards capgate.OneHot.
func OneHot(features ...string) string { return inner.OneHot(features...) }

// Compare forwards capgate.Compare.
func Compare(oldGraph, newGraph *Graph, policy Policy) (*DiffReport, error) {
	return inner.Compare(oldGraph, newGraph, policy)
}

// Acceptance forwards capgate.Acceptance.
func Acceptance(oldGraph, newGraph *Graph, policy Policy) (*DiffReport, error) {
	return inner.Acceptance(oldGraph, newGraph, policy)
}

// DecodeGraph forwards capgate.DecodeGraph.
func DecodeGraph(r io.Reader) (*Graph, error) { return inner.DecodeGraph(r) }

// CompareJSON forwards capgate.CompareJSON.
func CompareJSON(oldJSON, newJSON []byte, policy Policy) (*DiffReport, error) {
	return inner.CompareJSON(oldJSON, newJSON, policy)
}

// ErrInvalidSignature forwards the sentinel error.
var ErrInvalidSignature = inner.ErrInvalidSignature
