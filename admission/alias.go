// Package admission re-exports the root admission package for callers that
// prefer an explicit /admission import path.
package admission

import root "formal.local/h26-20dcb4071f"

type (
	ErrorCode             = root.ErrorCode
	AdmissionError        = root.AdmissionError
	Variable              = root.Variable
	Term                  = root.Term
	ReleaseRow            = root.ReleaseRow
	MappingConstraint     = root.MappingConstraint
	ModIntervalConstraint = root.ModIntervalConstraint
	BitSliceConstraint    = root.BitSliceConstraint
	ProtectedForm         = root.ProtectedForm
	CollusionPolicy       = root.CollusionPolicy
	Batch                 = root.Batch
	Config                = root.Config
	Gate                  = root.Gate
	State                 = root.State
	Record                = root.Record
	PolicyReport          = root.PolicyReport
	Report                = root.Report
	Certificate           = root.Certificate
	LinearCertificate     = root.LinearCertificate
	PrimePowerCertificate = root.PrimePowerCertificate
	ExponentEntry         = root.ExponentEntry
	RowExponent           = root.RowExponent
	PolicyCertificate     = root.PolicyCertificate
	ProjectionCertificate = root.ProjectionCertificate
	FormCertificate       = root.FormCertificate
	PrimeCountCertificate = root.PrimeCountCertificate
	AuxiliaryCertificate  = root.AuxiliaryCertificate
)

var (
	ErrInvalidInput      = root.ErrInvalidInput
	ErrNonWellDefined    = root.ErrNonWellDefined
	ErrInconsistent      = root.ErrInconsistent
	ErrCollusionExceeded = root.ErrCollusionExceeded
	ErrEnumerationLimit  = root.ErrEnumerationLimit
	ErrInternal          = root.ErrInternal

	CodeInvalidInput      = root.CodeInvalidInput
	CodeNonWellDefined    = root.CodeNonWellDefined
	CodeInconsistent      = root.CodeInconsistent
	CodeCollusionExceeded = root.CodeCollusionExceeded
	CodeEnumerationLimit  = root.CodeEnumerationLimit
	CodeInternal          = root.CodeInternal

	NewGate           = root.NewGate
	New               = root.New
	I                 = root.I
	MustInt           = root.MustInt
	Shrink            = root.Shrink
	VerifyCertificate = root.VerifyCertificate
)
