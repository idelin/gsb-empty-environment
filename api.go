package netsec

import "example.org/netsec/q0332/descriptor"

// The root package re-exports the descriptor validation API. New code may
// import either example.org/netsec/q0332 or .../descriptor.
type (
	Address          = descriptor.Address
	Access           = descriptor.Access
	Region           = descriptor.Region
	RegionSet        = descriptor.RegionSet
	Authorizer       = descriptor.Authorizer
	AuthorizerFunc   = descriptor.AuthorizerFunc
	FieldKind        = descriptor.FieldKind
	AddressSemantics = descriptor.AddressSemantics
	AddressField     = descriptor.AddressField
	OpcodeField      = descriptor.OpcodeField
	Pattern          = descriptor.Pattern
	Format           = descriptor.Format
	CountMode        = descriptor.CountMode
	ScalarField      = descriptor.ScalarField
	CountSpec        = descriptor.CountSpec
	Table            = descriptor.Table
	Request          = descriptor.Request
	Descriptor       = descriptor.Request
	PageID           = descriptor.PageID
	PageStore        = descriptor.PageStore
	PageFreezer      = descriptor.PageFreezer
	Translator       = descriptor.Translator
	Limits           = descriptor.Limits
	Validator        = descriptor.Validator
	Operation        = descriptor.Operation
	Plan             = descriptor.Plan
	Submitter        = descriptor.Submitter
	Error            = descriptor.Error
)

const (
	AccessRead  = descriptor.AccessRead
	AccessWrite = descriptor.AccessWrite
	AccessAll   = descriptor.AccessAll

	FieldIgnored  = descriptor.FieldIgnored
	FieldRead     = descriptor.FieldRead
	FieldWrite    = descriptor.FieldWrite
	FieldIndirect = descriptor.FieldIndirect

	CountFixed      = descriptor.CountFixed
	CountField      = descriptor.CountField
	CountBytesField = descriptor.CountBytesField
)

var (
	ErrSchema        = descriptor.ErrSchema
	ErrBadDescriptor = descriptor.ErrBadDescriptor
	ErrUnknownOpcode = descriptor.ErrUnknownOpcode
	ErrTail          = descriptor.ErrTail
	ErrUnauthorized  = descriptor.ErrUnauthorized
	ErrCycle         = descriptor.ErrCycle
	ErrLimit         = descriptor.ErrLimit
	ErrPage          = descriptor.ErrPage
	ErrOverflow      = descriptor.ErrOverflow
	ErrNil           = descriptor.ErrNil
	ErrFreeze        = descriptor.ErrFreeze
)

func New(store PageStore, authorizer Authorizer) *Validator {
	return descriptor.New(store, authorizer)
}

func NewRegionSet(regions ...Region) (*RegionSet, error) {
	return descriptor.NewRegionSet(regions...)
}

func Validate(store PageStore, authorizer Authorizer, req Request) (*Plan, error) {
	return descriptor.Validate(store, authorizer, req)
}

func ValidateDescriptor(store PageStore, authorizer Authorizer, req Request) (*Plan, error) {
	return descriptor.Validate(store, authorizer, req)
}
