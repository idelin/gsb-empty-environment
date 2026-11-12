// Package avp exposes the Diameter AVP decoder from the module root as a
// dedicated import path.
package avp

import base "example.org/netsec/q0260"

type AVP = base.AVP
type AVPKey = base.AVPKey
type AVPInfo = base.AVPInfo
type Decoder = base.Decoder
type DecodeError = base.DecodeError
type ProtocolError = base.ProtocolError
type AVPError = base.AVPError

const (
	FlagVendor    = base.FlagVendor
	FlagMandatory = base.FlagMandatory
	FlagProtected = base.FlagProtected

	AVPFlagVendor    = base.AVPFlagVendor
	AVPFlagMandatory = base.AVPFlagMandatory
	AVPFlagProtected = base.AVPFlagProtected

	AVPHeaderLen       = base.AVPHeaderLen
	AVPVendorHeaderLen = base.AVPVendorHeaderLen

	CodeFailedAVP   = base.CodeFailedAVP
	CodeResultCode  = base.CodeResultCode
	MaxGroupedDepth = base.MaxGroupedDepth

	ResultCodeAVPUnsupported    = base.ResultCodeAVPUnsupported
	ResultCodeInvalidAVPValue   = base.ResultCodeInvalidAVPValue
	ResultCodeInvalidAVPLength  = base.ResultCodeInvalidAVPLength
	ResultCodeMissingAVP        = base.ResultCodeMissingAVP
	ResultCodeResourcesExceeded = base.ResultCodeResourcesExceeded

	DIAMETER_AVP_UNSUPPORTED    = base.DIAMETER_AVP_UNSUPPORTED
	DIAMETER_INVALID_AVP_VALUE  = base.DIAMETER_INVALID_AVP_VALUE
	DIAMETER_INVALID_AVP_LENGTH = base.DIAMETER_INVALID_AVP_LENGTH
)

var (
	ErrInvalidAVPLength = base.ErrInvalidAVPLength
	ErrInvalidVendorID  = base.ErrInvalidVendorID
	ErrGroupedTooDeep   = base.ErrGroupedTooDeep
	ErrInvalidAVP       = base.ErrInvalidAVP
	ErrUnsupportedAVP   = base.ErrUnsupportedAVP
)

func NewDecoder() *Decoder {
	return base.NewDecoder()
}

func NewDecoderWithKnown(known map[AVPKey]AVPInfo) *Decoder {
	return base.NewDecoderWithKnown(known)
}

func Decode(data []byte) ([]*AVP, error) {
	return base.Decode(data)
}

func DecodeAVPs(data []byte) ([]*AVP, error) {
	return base.DecodeAVPs(data)
}

func Parse(data []byte) ([]*AVP, error) {
	return base.Parse(data)
}

func DecodeAVP(data []byte) (*AVP, error) {
	return base.DecodeAVP(data)
}
