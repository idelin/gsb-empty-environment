// Package displaycommitment re-exports the root displaycommit API under its
// full descriptive name.
package display

import (
	dc "prescreen.local/ps-89dba9148c"
)

type (
	Address            = dc.Address
	Amount             = dc.Amount
	AmountRange        = dc.AmountRange
	AmountSet          = dc.AmountSet
	BindingInterval    = dc.BindingInterval
	CheckResult        = dc.CheckResult
	Counterexample     = dc.Counterexample
	Difference         = dc.Difference
	Device             = dc.Device
	Display            = dc.Display
	DisplayCommitment  = dc.DisplayCommitment
	DisplayRule        = dc.DisplayRule
	Field              = dc.Field
	FieldPath          = dc.FieldPath
	FieldRule          = dc.FieldRule
	FieldSpec          = dc.FieldSpec
	Hash               = dc.Hash
	Interval           = dc.Interval
	Kind               = dc.Kind
	Leaf               = dc.Leaf
	Line               = dc.Line
	MergeReport        = dc.MergeReport
	Message            = dc.Message
	MessageSpec        = dc.MessageSpec
	MessageTree        = dc.MessageTree
	Page               = dc.Page
	PagePacket         = dc.PagePacket
	PageProof          = dc.PageProof
	PageReceipt        = dc.PageReceipt
	PageRef            = dc.PageRef
	PageRule           = dc.PageRule
	Path               = dc.Path
	PathStep           = dc.PathStep
	PersistentState    = dc.PersistentState
	Report             = dc.Report
	RequestBinding     = dc.RequestBinding
	Result             = dc.Result
	RoundingMode       = dc.RoundingMode
	RoundingRule       = dc.RoundingRule
	Role               = dc.Role
	Screen             = dc.Screen
	SigningRequest     = dc.SigningRequest
	SignedMessage      = dc.SignedMessage
	SmallDomain        = dc.SmallDomain
	TextPolicy         = dc.TextPolicy
	TextReport         = dc.TextReport
	TypeTable          = dc.TypeTable
	Value              = dc.Value
	Verdict            = dc.Verdict
	VerificationResult = dc.VerificationResult
	Verifier           = dc.Verifier
	VerifyOptions      = dc.VerifyOptions
)

var (
	AddressValue             = dc.AddressValue
	Analyze                  = dc.Analyze
	AnalyzeRules             = dc.AnalyzeRules
	AsBigInt                 = dc.AsBigInt
	BoolValue                = dc.BoolValue
	BytesValue               = dc.BytesValue
	BuildLeafProof           = dc.BuildLeafProof
	BuildPageProof           = dc.BuildPageProof
	BuildRequest             = dc.BuildRequest
	CanonicalMessage         = dc.CanonicalMessage
	CheckDisplayRule         = dc.CheckDisplayRule
	CheckRule                = dc.CheckRule
	CheckSmallDomain         = dc.CheckSmallDomain
	ClosedInterval           = dc.ClosedInterval
	ComparePair              = dc.ComparePair
	CommitRaw                = dc.CommitRaw
	ConfusableSkeleton       = dc.ConfusableSkeleton
	CreateSigningRequest     = dc.CreateSigningRequest
	DefaultTextPolicy        = dc.DefaultTextPolicy
	DomainHash               = dc.DomainHash
	EmptyInterval            = dc.EmptyInterval
	FormatDecimal            = dc.FormatDecimal
	InspectRestrictedText    = dc.InspectRestrictedText
	IntervalOfValues         = dc.IntervalOfValues
	IntValue                 = dc.IntValue
	IsConfusableRune         = dc.IsConfusableRune
	IsInvisibleRune          = dc.IsInvisibleRune
	IsUnknown                = dc.IsUnknown
	ListValue                = dc.ListValue
	MessageValue             = dc.MessageValue
	MustRenderRestrictedText = dc.MustRenderRestrictedText
	Near256Bit               = dc.Near256Bit
	NewAddress               = dc.NewAddress
	NewAmountRange           = dc.NewAmountRange
	NewAmountSet             = dc.NewAmountSet
	NewDevice                = dc.NewDevice
	NewDisplayCommitment     = dc.NewDisplayCommitment
	NewSigningRequest        = dc.NewSigningRequest
	NewSmallDomain           = dc.NewSmallDomain
	NewVerifier              = dc.NewVerifier
	NormalizePath            = dc.NormalizePath
	NormalizeWire            = dc.NormalizeWire
	P                        = dc.P
	PI                       = dc.PI
	ParseHash                = dc.ParseHash
	ParsePath                = dc.ParsePath
	PointInterval            = dc.PointInterval
	RandomSessionNonce       = dc.RandomSessionNonce
	Render                   = dc.Render
	RenderRestrictedText     = dc.RenderRestrictedText
	RestoreDevice            = dc.RestoreDevice
	RoundingInterval         = dc.RoundingInterval
	RoundBigInt              = dc.RoundBigInt
	SameSafeScreen           = dc.SameSafeScreen
	SignMessage              = dc.SignMessage
	Sum256                   = dc.Sum256
	UnionInterval            = dc.UnionInterval
	ValidateRule             = dc.ValidateRule
	Verify                   = dc.Verify
	VerifyDisplayRule        = dc.VerifyDisplayRule
	VerifyProof              = dc.VerifyProof
	VerifyReceipt            = dc.VerifyReceipt
	VerifyRequestSignature   = dc.VerifyRequestSignature
	VerifyRule               = dc.VerifyRule
	VerifyRules              = dc.VerifyRules
	VerifySample             = dc.VerifySample
	VerifySmallDomain        = dc.VerifySmallDomain
	VisualSkeleton           = dc.VisualSkeleton
	HashTypeTable            = dc.HashTypeTable
	ValidateRestrictedText   = dc.ValidateRestrictedText
	IsValidRestrictedText    = dc.IsValidRestrictedText
)

const (
	KindNull    = dc.KindNull
	KindInt     = dc.KindInt
	KindBool    = dc.KindBool
	KindText    = dc.KindText
	KindAddress = dc.KindAddress
	KindBytes   = dc.KindBytes
	KindMessage = dc.KindMessage
	KindList    = dc.KindList

	RolePayee      = dc.RolePayee
	RoleRecipient  = dc.RoleRecipient
	RoleSubject    = dc.RoleSubject
	RoleAsset      = dc.RoleAsset
	RoleAmount     = dc.RoleAmount
	RoleDomain     = dc.RoleDomain
	RoleSignDomain = dc.RoleSignDomain
	RoleDeadline   = dc.RoleDeadline
	RoleExpiry     = dc.RoleExpiry
	RoleNonAuth    = dc.RoleNonAuth
	RoleMalleable  = dc.RoleMalleable
	RoleObjectID   = dc.RoleObjectID

	RoundHalfUp   = dc.RoundHalfUp
	RoundHalfEven = dc.RoundHalfEven
	RoundDown     = dc.RoundDown
	RoundFloor    = dc.RoundFloor
	RoundCeil     = dc.RoundCeil

	VerdictUnknown    = dc.VerdictUnknown
	VerdictSafe       = dc.VerdictSafe
	VerdictUnsafe     = dc.VerdictUnsafe
	VerdictProven     = dc.VerdictProven
	VerdictUnprovable = dc.VerdictUnprovable
	VerdictViolation  = dc.VerdictViolation
	StatusUnknown     = dc.StatusUnknown
	StatusSafe        = dc.StatusSafe
	StatusUnsafe      = dc.StatusUnsafe
	StatusProven      = dc.StatusProven
	StatusCannotProve = dc.StatusCannotProve
	StatusViolation   = dc.StatusViolation
	ResultUnknown     = dc.ResultUnknown
	ResultSafe        = dc.ResultSafe
	ResultUnsafe      = dc.ResultUnsafe
	UnknownVerdict    = dc.UnknownVerdict

	WireVarint          = dc.WireVarint
	WireTypeVarint      = dc.WireTypeVarint
	WireFixed64         = dc.WireFixed64
	WireTypeFixed64     = dc.WireTypeFixed64
	WireFixed32         = dc.WireFixed32
	WireTypeFixed32     = dc.WireTypeFixed32
	WireBytes           = dc.WireBytes
	WireTypeBytes       = dc.WireTypeBytes
	WireLengthDelimited = dc.WireLengthDelimited
	WireString          = dc.WireString
	WireTypeString      = dc.WireTypeString
	WireText            = dc.WireText
	WireTypeText        = dc.WireTypeText
	WireAddress         = dc.WireAddress
	WireTypeAddress     = dc.WireTypeAddress
	WireBigInt          = dc.WireBigInt
	WireTypeBigInt      = dc.WireTypeBigInt
	WireMessage         = dc.WireMessage
	WireTypeMessage     = dc.WireTypeMessage
	WireList            = dc.WireList
	WireTypeList        = dc.WireTypeList

	ProtobufWireVarint  = dc.ProtobufWireVarint
	ProtobufWireFixed64 = dc.ProtobufWireFixed64
	ProtobufWireBytes   = dc.ProtobufWireBytes
	ProtobufWireFixed32 = dc.ProtobufWireFixed32
)
