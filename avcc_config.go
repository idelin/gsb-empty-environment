package h264bs

import (
	"encoding/binary"
	"fmt"
)

// DecoderConfig mirrors an AVCDecoderConfigurationRecord (ISO/IEC
// 14496-15 5.3.3.1): the "avcC" box carried alongside length-prefixed
// NAL units in MP4-style containers.
type DecoderConfig struct {
	LengthSize           int // 1, 2 or 4
	ProfileIDC           uint8
	ConstraintFlags      uint8
	LevelIDC             uint8
	HighProfileExtended  bool
	ChromaFormatIDC      uint8
	BitDepthLumaMinus8   uint8
	BitDepthChromaMinus8 uint8
	SPS                  [][]byte
	PPS                  [][]byte
}

// BuildDecoderConfig builds an AVCDecoderConfigurationRecord from
// parameter-set NAL units. At least one SPS is required; its profile
// bytes are used for the configuration record header.
func BuildDecoderConfig(spsNALs, ppsNALs [][]byte, lengthSize int) ([]byte, error) {
	if lengthSize != 1 && lengthSize != 2 && lengthSize != 4 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidLengthSize, lengthSize)
	}
	if len(spsNALs) == 0 {
		return nil, fmt.Errorf("h264bs: %w: at least one SPS is required", ErrInvalidConfig)
	}

	sps, err := ParseSPS(spsNALs[0])
	if err != nil {
		return nil, err
	}

	constraintFlags := uint8(0)
	flags := []bool{
		sps.ConstraintSet0Flag, sps.ConstraintSet1Flag, sps.ConstraintSet2Flag,
		sps.ConstraintSet3Flag, sps.ConstraintSet4Flag, sps.ConstraintSet5Flag,
	}
	for i, set := range flags {
		if set {
			constraintFlags |= 1 << (7 - uint(i))
		}
	}

	cfg := make([]byte, 0, 64+len(spsNALs)*32+len(ppsNALs)*32)
	cfg = append(cfg,
		1, // configurationVersion
		sps.ProfileIDC,
		constraintFlags,
		sps.LevelIDC,
		0xfc|byte(lengthSize-1), // lengthSizeMinusOne, reserved bits set
		0xe0|byte(len(spsNALs)), // numOfSequenceParameterSets, reserved bits set
	)
	for _, nal := range spsNALs {
		if len(nal) > 0xffff {
			return nil, fmt.Errorf("%w: SPS longer than 65535 bytes", ErrNALTooLong)
		}
		cfg = binary.BigEndian.AppendUint16(cfg, uint16(len(nal)))
		cfg = append(cfg, nal...)
	}

	cfg = append(cfg, byte(len(ppsNALs))) // numOfPictureParameterSets
	for _, nal := range ppsNALs {
		if len(nal) > 0xffff {
			return nil, fmt.Errorf("%w: PPS longer than 65535 bytes", ErrNALTooLong)
		}
		cfg = binary.BigEndian.AppendUint16(cfg, uint16(len(nal)))
		cfg = append(cfg, nal...)
	}

	if highProfileIDC(sps.ProfileIDC) {
		cfg = append(cfg,
			0xfc|byte(sps.ChromaFormatIDC),
			0xf8|byte(sps.BitDepthLumaMinus8),
			0xf8|byte(sps.BitDepthChromaMinus8),
			0, // numOfSequenceParameterSetExt
		)
	}
	return cfg, nil
}

// ParseDecoderConfig parses an AVCDecoderConfigurationRecord.
func ParseDecoderConfig(data []byte) (*DecoderConfig, error) {
	if len(data) < 7 {
		return nil, fmt.Errorf("h264bs: %w: record too short", ErrInvalidConfig)
	}
	if data[0] != 1 {
		return nil, fmt.Errorf("h264bs: %w: unsupported configurationVersion %d",
			ErrInvalidConfig, data[0])
	}

	cfg := &DecoderConfig{
		ProfileIDC:      data[1],
		ConstraintFlags: data[2],
		LevelIDC:        data[3],
		LengthSize:      int(data[4]&0x03) + 1,
	}
	pos := 5

	numSPS := int(data[pos] & 0x1f)
	pos++
	for i := 0; i < numSPS; i++ {
		if pos+2 > len(data) {
			return nil, fmt.Errorf("h264bs: %w: SPS length", ErrInvalidConfig)
		}
		n := int(binary.BigEndian.Uint16(data[pos:]))
		pos += 2
		if pos+n > len(data) {
			return nil, fmt.Errorf("h264bs: %w: SPS payload", ErrInvalidConfig)
		}
		cfg.SPS = append(cfg.SPS, append([]byte(nil), data[pos:pos+n]...))
		pos += n
	}

	if pos >= len(data) {
		return nil, fmt.Errorf("h264bs: %w: missing PPS count", ErrInvalidConfig)
	}
	numPPS := int(data[pos])
	pos++
	for i := 0; i < numPPS; i++ {
		if pos+2 > len(data) {
			return nil, fmt.Errorf("h264bs: %w: PPS length", ErrInvalidConfig)
		}
		n := int(binary.BigEndian.Uint16(data[pos:]))
		pos += 2
		if pos+n > len(data) {
			return nil, fmt.Errorf("h264bs: %w: PPS payload", ErrInvalidConfig)
		}
		cfg.PPS = append(cfg.PPS, append([]byte(nil), data[pos:pos+n]...))
		pos += n
	}

	if highProfileIDC(cfg.ProfileIDC) && pos+4 <= len(data) {
		cfg.HighProfileExtended = true
		cfg.ChromaFormatIDC = data[pos] & 0x03
		cfg.BitDepthLumaMinus8 = data[pos+1] & 0x07
		cfg.BitDepthChromaMinus8 = data[pos+2] & 0x07
		pos += 4 // also skips numOfSequenceParameterSetExt (assumed zero)
	}

	return cfg, nil
}
