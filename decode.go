package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Diameter Result-Code values used by the decoder.
const (
	ResultCodeAVPUnsupported    uint32 = 5001
	ResultCodeInvalidAVPValue   uint32 = 5004
	ResultCodeInvalidAVPLength  uint32 = 5014
	ResultCodeMissingAVP        uint32 = 5005
	ResultCodeResourcesExceeded uint32 = 5006

	// DIAMETER_AVP_UNSUPPORTED is the RFC name for Result-Code 5001.
	DIAMETER_AVP_UNSUPPORTED    = ResultCodeAVPUnsupported
	DIAMETER_INVALID_AVP_VALUE  = ResultCodeInvalidAVPValue
	DIAMETER_INVALID_AVP_LENGTH = ResultCodeInvalidAVPLength
)

// MaxGroupedDepth is the default maximum number of nested Grouped AVP layers,
// counting the outermost Grouped AVP as level one.
const MaxGroupedDepth = 8

var (
	// ErrUnsupportedAVP is the cause carried for an unknown AVP with the M bit set.
	ErrUnsupportedAVP = errors.New("diameter: unsupported mandatory AVP")
)

// DecodeError describes a Diameter AVPs decoding failure. For an unsupported
// mandatory AVP, ResultCode is 5001 and FailedAVP is the Grouped Failed-AVP
// (AVP code 279) that contains the offending AVP.
type DecodeError struct {
	ResultCode uint32

	// Code is an alias for ResultCode, provided for callers that use a shorter
	// field name. It is set to the same value as ResultCode.
	Code uint32

	// FailedAVP is the Failed-AVP Grouped AVP to place in an error answer.
	FailedAVP *AVP

	// OffendingAVP is the AVP that caused the error, when it could be decoded.
	OffendingAVP *AVP

	// Reason is a human-readable explanation.
	Reason string

	cause error
}

// Error implements error.
func (e *DecodeError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.FailedAVP != nil {
		return fmt.Sprintf("diameter: %s (Result-Code %d, Failed-AVP %d)",
			e.Reason, e.ResultCode, e.FailedAVP.Code)
	}
	return fmt.Sprintf("diameter: %s (Result-Code %d)", e.Reason, e.ResultCode)
}

// Unwrap returns the underlying sentinel error, if any.
func (e *DecodeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// FailedAVPBytes returns the encoded Failed-AVP, including padding, or nil
// when the error has no Failed-AVP.
func (e *DecodeError) FailedAVPBytes() []byte {
	if e == nil || e.FailedAVP == nil {
		return nil
	}
	b, err := e.FailedAVP.MarshalBinary()
	if err != nil {
		return nil
	}
	return b
}

// ResultCodeAVP returns the Result-Code AVP (code 268) for this error.
func (e *DecodeError) ResultCodeAVP() *AVP {
	if e == nil {
		return nil
	}
	return uint32AVP(CodeResultCode, e.ResultCode)
}

// ResponseAVPs returns the AVPs needed to build an error answer: Result-Code
// followed by Failed-AVP when present.
func (e *DecodeError) ResponseAVPs() []*AVP {
	if e == nil {
		return nil
	}
	out := []*AVP{e.ResultCodeAVP()}
	if e.FailedAVP != nil {
		out = append(out, e.FailedAVP)
	}
	return out
}

// ProtocolError and AVPError are aliases for DecodeError.
type ProtocolError = DecodeError
type AVPError = DecodeError

// Decoder parses AVP sequences and checks the rules that a forwarding proxy
// needs to enforce.
type Decoder struct {
	// Known is the AVP dictionary. A missing entry means that the AVP is
	// unknown to the proxy.
	Known map[AVPKey]AVPInfo

	// KnownCodes marks IETF AVP codes as known when callers do not need
	// per-code metadata. Known (if non-nil) takes precedence.
	KnownCodes map[uint32]bool

	// MaxGroupedDepth limits nested Grouped AVPs. Zero selects MaxDepth or
	// the default of MaxGroupedDepth.
	MaxGroupedDepth int

	// MaxDepth is an alias for MaxGroupedDepth.
	MaxDepth int
}

// NewDecoder returns a decoder using the built-in base Diameter dictionary.
func NewDecoder() *Decoder {
	return &Decoder{
		Known:           cloneDictionary(builtinDictionary),
		MaxGroupedDepth: MaxGroupedDepth,
	}
}

// NewDecoderWithKnown returns a decoder using the supplied dictionary. The
// map is copied so later changes to it do not affect the decoder.
func NewDecoderWithKnown(known map[AVPKey]AVPInfo) *Decoder {
	d := &Decoder{
		Known:           make(map[AVPKey]AVPInfo, len(known)),
		MaxGroupedDepth: MaxGroupedDepth,
	}
	for k, v := range known {
		d.Known[k] = v
	}
	return d
}

var builtinDictionary = defaultDictionary()

var defaultDecoder = &Decoder{
	Known:           builtinDictionary,
	MaxGroupedDepth: MaxGroupedDepth,
}

// Decode parses data as a sequence of complete, padded AVPs.
func (d *Decoder) Decode(data []byte) ([]*AVP, error) {
	if d == nil {
		d = NewDecoder()
	}
	return d.decodeList(data, 1)
}

// Decode parses data using the built-in base Diameter dictionary.
func Decode(data []byte) ([]*AVP, error) {
	return defaultDecoder.Decode(data)
}

// DecodeAVPs is an alias for Decode.
func DecodeAVPs(data []byte) ([]*AVP, error) {
	return Decode(data)
}

// Parse is an alias for Decode.
func Parse(data []byte) ([]*AVP, error) {
	return Decode(data)
}

// DecodeAVP parses exactly one AVP.
func DecodeAVP(data []byte) (*AVP, error) {
	avps, err := Decode(data)
	if err != nil {
		return nil, err
	}
	if len(avps) != 1 {
		return nil, &DecodeError{
			ResultCode: ResultCodeInvalidAVPValue,
			Code:       ResultCodeInvalidAVPValue,
			Reason:     fmt.Sprintf("expected exactly one AVP, got %d", len(avps)),
			cause:      ErrInvalidAVP,
		}
	}
	return avps[0], nil
}

func (d *Decoder) decodeList(data []byte, depth int) ([]*AVP, error) {
	if len(data) == 0 {
		return nil, nil
	}

	avps := make([]*AVP, 0, 4)
	offset := 0
	for offset < len(data) {
		remaining := data[offset:]
		avp, consumed, err := d.decodeOne(remaining, depth)
		if err != nil {
			return nil, err
		}
		avps = append(avps, avp)
		offset += consumed
	}
	return avps, nil
}

func (d *Decoder) decodeOne(data []byte, depth int) (*AVP, int, error) {
	if len(data) < AVPHeaderLen {
		return nil, 0, d.invalidLength(
			"AVP is shorter than the minimum header",
			offendingAVP(data),
		)
	}

	code := binary.BigEndian.Uint32(data[0:4])
	flags := data[4]
	length := getUint24(data[5:8])
	headerLen := AVPHeaderLen
	if flags&FlagVendor != 0 {
		headerLen = AVPVendorHeaderLen
	}

	if length < uint32(headerLen) {
		return nil, 0, d.invalidLength(
			fmt.Sprintf("AVP %d length %d is shorter than header %d", code, length, headerLen),
			offendingAVP(data),
		)
	}
	if uint64(length) > uint64(len(data)) {
		return nil, 0, d.invalidLength(
			fmt.Sprintf("AVP %d length %d exceeds remaining %d bytes", code, length, len(data)),
			offendingAVP(data),
		)
	}

	paddedLen := pad4(int(length))
	if paddedLen > len(data) {
		return nil, 0, d.invalidLength(
			fmt.Sprintf("AVP %d padded length %d exceeds remaining %d bytes", code, paddedLen, len(data)),
			offendingAVP(data),
		)
	}

	var vendorID uint32
	if flags&FlagVendor != 0 {
		vendorID = binary.BigEndian.Uint32(data[8:12])
		if vendorID == 0 {
			return nil, 0, d.decodeError(
				ResultCodeInvalidAVPValue,
				ErrInvalidVendorID,
				fmt.Sprintf("AVP %d has the V bit set with Vendor-Id 0", code),
				offendingAVP(data),
			)
		}
	}

	valueStart := headerLen
	avp := &AVP{
		Code:     code,
		Flags:    flags,
		VendorID: vendorID,
		Value:    data[valueStart:int(length)],
		Raw:      data[:paddedLen],
	}

	info, known := d.lookup(code, vendorID)
	mandatory := flags&FlagMandatory != 0
	if !known && mandatory {
		return nil, 0, d.decodeError(
			ResultCodeAVPUnsupported,
			ErrUnsupportedAVP,
			fmt.Sprintf("AVP %d (Vendor-Id %d) is unknown and mandatory", code, vendorID),
			avp,
		)
	}

	if known && info.Grouped {
		if depth > d.maxGroupedDepth() {
			return nil, 0, d.decodeError(
				ResultCodeInvalidAVPValue,
				ErrGroupedTooDeep,
				fmt.Sprintf("Grouped AVP %d exceeds maximum nesting depth %d", code, d.maxGroupedDepth()),
				avp,
			)
		}
		children, err := d.decodeList(avp.Value, depth+1)
		if err != nil {
			return nil, 0, err
		}
		avp.Grouped = true
		avp.Children = children
	}

	return avp, paddedLen, nil
}

func (d *Decoder) lookup(code, vendorID uint32) (AVPInfo, bool) {
	key := AVPKey{Code: code, VendorID: vendorID}
	known := d.Known
	if known == nil {
		known = builtinDictionary
	}
	if known != nil {
		if info, ok := known[key]; ok {
			return info, true
		}
	}
	if vendorID == 0 && d.KnownCodes != nil && d.KnownCodes[code] {
		return AVPInfo{}, true
	}
	return AVPInfo{}, false
}

func cloneDictionary(src map[AVPKey]AVPInfo) map[AVPKey]AVPInfo {
	dst := make(map[AVPKey]AVPInfo, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func (d *Decoder) maxGroupedDepth() int {
	if d.MaxGroupedDepth > 0 {
		return d.MaxGroupedDepth
	}
	if d.MaxDepth > 0 {
		return d.MaxDepth
	}
	return MaxGroupedDepth
}

func (d *Decoder) invalidLength(reason string, offending *AVP) *DecodeError {
	return d.decodeError(ResultCodeInvalidAVPLength, ErrInvalidAVPLength, reason, offending)
}

func (d *Decoder) decodeError(resultCode uint32, cause error, reason string, offending *AVP) *DecodeError {
	err := &DecodeError{
		ResultCode:   resultCode,
		Code:         resultCode,
		OffendingAVP: offending,
		Reason:       reason,
		cause:        cause,
	}
	if offending != nil {
		err.FailedAVP = failedAVP(offending)
	}
	return err
}

// failedAVP builds the Grouped Failed-AVP (code 279) containing offending.
func failedAVP(offending *AVP) *AVP {
	if offending == nil {
		return nil
	}
	return &AVP{
		Code:     CodeFailedAVP,
		Flags:    FlagMandatory,
		Grouped:  true,
		Children: []*AVP{offending},
		Value:    offending.Bytes(),
	}
}

// offendingAVP builds as much of an offending AVP as can be recovered. It
// intentionally ignores the declared length so a malformed AVP can still be
// represented in a Failed-AVP.
func offendingAVP(raw []byte) *AVP {
	if len(raw) == 0 {
		return nil
	}
	a := &AVP{}
	if len(raw) >= 4 {
		a.Code = binary.BigEndian.Uint32(raw[0:4])
	}
	if len(raw) >= 5 {
		a.Flags = raw[4]
	}
	headerLen := AVPHeaderLen
	if a.Flags&FlagVendor != 0 {
		headerLen = AVPVendorHeaderLen
	}
	if len(raw) >= 8 {
		declared := int(getUint24(raw[5:8]))
		if declared >= headerLen && declared <= len(raw) {
			a.Value = raw[headerLen:declared]
		} else if len(raw) > headerLen {
			a.Value = raw[headerLen:]
		}
	} else if len(raw) > headerLen {
		a.Value = raw[headerLen:]
	}
	if len(raw) >= AVPVendorHeaderLen && a.Flags&FlagVendor != 0 {
		a.VendorID = binary.BigEndian.Uint32(raw[8:12])
	}
	a.Raw = raw
	return a
}

func uint32AVP(code uint32, value uint32) *AVP {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, value)
	return &AVP{Code: code, Flags: FlagMandatory, Value: b}
}
