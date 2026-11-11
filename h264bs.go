// Package h264bs converts H.264 bitstreams between Annex B byte-stream
// format and AVCC (length-prefixed) format, parses sequence parameter
// sets and maintains an AVCDecoderConfigurationRecord (avcC).
package h264bs

import "errors"

// NAL unit types defined by ITU-T H.264 Table 7-1 (subset).
const (
	NALTypeNonIDRSlice uint8 = 1
	NALTypeDPA         uint8 = 2
	NALTypeDPB         uint8 = 3
	NALTypeDPC         uint8 = 4
	NALTypeIDRSlice    uint8 = 5
	NALTypeSEI         uint8 = 6
	NALTypeSPS         uint8 = 7
	NALTypePPS         uint8 = 8
	NALTypeAUD         uint8 = 9
	NALTypeEndSequence uint8 = 10
	NALTypeEndStream   uint8 = 11
	NALTypeFillerData  uint8 = 12
)

// Common errors returned by the package. Conversions wrap these with
// context using fmt.Errorf("... %w ...").
var (
	ErrEmptyNAL          = errors.New("h264bs: empty NAL unit")
	ErrInvalidLengthSize = errors.New("h264bs: length prefix size must be 1, 2 or 4 bytes")
	ErrNALTooLong        = errors.New("h264bs: NAL unit exceeds length-prefix capacity")
	ErrTruncated         = errors.New("h264bs: truncated bitstream")
	ErrInvalidRBSP       = errors.New("h264bs: invalid emulation prevention byte sequence")
	ErrInvalidConfig     = errors.New("h264bs: invalid AVCDecoderConfigurationRecord")
)

// NALType extracts nal_unit_type from the first byte of a NAL unit.
func NALType(nal []byte) (uint8, error) {
	if len(nal) == 0 {
		return 0, ErrEmptyNAL
	}
	return nal[0] & 0x1f, nil
}

// highProfileIDC reports whether profile_idc carries the High-profile
// extension fields (chroma format, bit depths, scaling matrices).
func highProfileIDC(profileIDC uint8) bool {
	switch profileIDC {
	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135:
		return true
	default:
		return false
	}
}
