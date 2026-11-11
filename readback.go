package fmp4mux

import (
	"encoding/binary"
	"errors"
)

// ErrInvalidSegment is returned when a parsed segment lacks required boxes.
var ErrInvalidSegment = errors.New("fmp4mux: invalid media segment")

// InitInfo describes a parsed initialization segment.
type InitInfo struct {
	Timescale uint32
	Width     uint16
	Height    uint16
	AVCC      []byte // AVCDecoderConfigurationRecord payload
	Boxes     []*Box
}

// SampleInfo is one sample reconstructed from tfdt/trun/mdat.
type SampleInfo struct {
	DTS        uint64
	CTO        int32
	PTS        int64 // DTS + CTO
	Size       uint32
	SyncSample bool
	Data       []byte // AVCC (length-prefixed) sample bytes from mdat
}

// SIDXInfo is the parsed sidx reference of a media segment.
type SIDXInfo struct {
	Version            uint8
	Timescale          uint32
	EarliestPTS        int64
	FirstOffset        uint64
	ReferencedSize     uint32
	SubsegmentDuration uint64
	SAPType            uint8
	SAPDeltaTime       uint32
}

// MediaSegment is a fully parsed media segment (styp/sidx/moof/mdat).
type MediaSegment struct {
	SequenceNumber      uint32
	BaseMediaDecodeTime uint64
	TrunVersion         uint8
	TrunFlags           uint32
	Samples             []SampleInfo
	SIDX                *SIDXInfo
	Boxes               []*Box
}

// ParseInit parses an initialization segment produced by this package.
func ParseInit(data []byte) (*InitInfo, error) {
	boxes, err := ParseBoxes(data)
	if err != nil {
		return nil, err
	}
	mdhd := FindBox(boxes, "mdhd")
	if mdhd == nil || len(mdhd.Payload) < 12 {
		return nil, ErrInvalidSegment
	}
	info := &InitInfo{
		Timescale: binary.BigEndian.Uint32(mdhd.Payload[8:12]),
		Boxes:     boxes,
	}
	if avc1 := FindBox(boxes, "avc1"); avc1 != nil {
		// Width/height are at fixed offsets 24/26 inside the 78-byte
		// visual sample entry fixed fields.
		if len(avc1.Payload) >= 28 {
			info.Width = binary.BigEndian.Uint16(avc1.Payload[24:26])
			info.Height = binary.BigEndian.Uint16(avc1.Payload[26:28])
		}
		if avcC := FindBox(avc1.Children, "avcC"); avcC != nil {
			info.AVCC = avcC.Payload
		}
	}
	return info, nil
}

// ParseMediaSegment parses one media segment (with or without a leading sidx)
// and reconstructs every sample with its decode and presentation timestamps.
func ParseMediaSegment(data []byte) (*MediaSegment, error) {
	boxes, err := ParseBoxes(data)
	if err != nil {
		return nil, err
	}

	moof := FindBox(boxes, "moof")
	mfhd := FindBox(boxes, "mfhd")
	tfdt := FindBox(boxes, "tfdt")
	trun := FindBox(boxes, "trun")
	mdat := FindBox(boxes, "mdat")
	if moof == nil || mfhd == nil || tfdt == nil || trun == nil || mdat == nil {
		return nil, ErrInvalidSegment
	}
	if len(mfhd.Payload) < 4 {
		return nil, ErrInvalidSegment
	}

	seg := &MediaSegment{
		SequenceNumber: binary.BigEndian.Uint32(mfhd.Payload[:4]),
		TrunVersion:    trun.Version,
		TrunFlags:      trun.Flags,
		Boxes:          boxes,
	}

	// baseMediaDecodeTime: v0 uint32, v1 uint64.
	if tfdt.Version == 1 {
		if len(tfdt.Payload) < 8 {
			return nil, ErrInvalidSegment
		}
		seg.BaseMediaDecodeTime = binary.BigEndian.Uint64(tfdt.Payload[:8])
	} else {
		if len(tfdt.Payload) < 4 {
			return nil, ErrInvalidSegment
		}
		seg.BaseMediaDecodeTime = uint64(binary.BigEndian.Uint32(tfdt.Payload[:4]))
	}

	samples, err := parseTrunSamples(trun, seg.BaseMediaDecodeTime)
	if err != nil {
		return nil, err
	}
	if err := fillSampleData(boxes, moof, trun, mdat, samples); err != nil {
		return nil, err
	}
	seg.Samples = samples

	if sidx := FindBox(boxes, "sidx"); sidx != nil {
		seg.SIDX = parseSIDX(sidx)
	}
	return seg, nil
}

func parseTrunSamples(trun *Box, baseDTS uint64) ([]SampleInfo, error) {
	p := trun.Payload
	if len(p) < 4 {
		return nil, ErrInvalidSegment
	}
	count := int(binary.BigEndian.Uint32(p[:4]))
	p = p[4:]

	flags := trun.Flags
	if flags&0x000001 != 0 { // data-offset
		if len(p) < 4 {
			return nil, ErrInvalidSegment
		}
		p = p[4:]
	}
	// first-sample-flags (trun flag 0x01000000) adds one header field that
	// is separate from the per-sample entries.
	if flags&0x01000000 != 0 {
		if len(p) < 4 {
			return nil, ErrInvalidSegment
		}
		p = p[4:]
	}

	hasDuration := flags&0x000100 != 0
	hasSize := flags&0x000200 != 0
	hasFlags := flags&0x000400 != 0
	hasCTO := flags&0x000800 != 0

	samples := make([]SampleInfo, count)
	dts := baseDTS
	for i := 0; i < count; i++ {
		var dur, size, sampleFlags, cto uint32
		var ok bool
		if hasDuration {
			if dur, p, ok = takeU32(p); !ok {
				return nil, ErrInvalidSegment
			}
		}
		if hasSize {
			if size, p, ok = takeU32(p); !ok {
				return nil, ErrInvalidSegment
			}
		}
		if hasFlags {
			if sampleFlags, p, ok = takeU32(p); !ok {
				return nil, ErrInvalidSegment
			}
		}
		if hasCTO {
			if cto, p, ok = takeU32(p); !ok {
				return nil, ErrInvalidSegment
			}
		}
		s := SampleInfo{
			DTS:        dts,
			Size:       size,
			SyncSample: sampleFlags&0x00010000 == 0,
		}
		if hasCTO {
			// Version 1 stores signed offsets; version 0 is unsigned but
			// its valid range fits int32, so the conversion is shared.
			s.CTO = int32(cto)
		}
		s.PTS = int64(s.DTS) + int64(s.CTO)
		samples[i] = s
		dts += uint64(dur)
	}
	return samples, nil
}

func takeU32(p []byte) (uint32, []byte, bool) {
	if len(p) < 4 {
		return 0, nil, false
	}
	return binary.BigEndian.Uint32(p[:4]), p[4:], true
}

// fillSampleData locates the mdat payload via trun.data_offset (relative to
// the moof box start) and slices each sample using its trun size. Box offsets
// are reconstructed by summing sibling box sizes.
func fillSampleData(topBoxes []*Box, moof, trun, mdat *Box, samples []SampleInfo) error {
	moofOffset, mdatOffset, ok := siblingOffsets(topBoxes, moof, mdat)
	if !ok {
		return ErrInvalidSegment
	}
	if trun.Flags&0x000001 == 0 || len(trun.Payload) < 8 {
		return ErrInvalidSegment
	}
	dataOffset := int64(int32(binary.BigEndian.Uint32(trun.Payload[4:8])))
	mdatPayloadStart := moofOffset + dataOffset
	relativeStart := mdatPayloadStart - (mdatOffset + 8)
	if relativeStart < 0 {
		return ErrInvalidSegment
	}

	payload := mdat.Payload
	pos := int(relativeStart)
	for i := range samples {
		end := pos + int(samples[i].Size)
		if end > len(payload) {
			return ErrInvalidSegment
		}
		samples[i].Data = payload[pos:end]
		pos = end
	}
	return nil
}

// siblingOffsets returns the absolute byte offsets of target boxes within the
// parsed top-level sequence.
func siblingOffsets(topBoxes []*Box, targets ...*Box) (int64, int64, bool) {
	if len(targets) != 2 {
		return 0, 0, false
	}
	var cur int64
	var first, second int64
	hits := 0
	for _, b := range topBoxes {
		if b == targets[0] {
			first = cur
			hits++
		}
		if b == targets[1] {
			second = cur
			hits++
		}
		cur += int64(boxSize(b))
	}
	return first, second, hits == 2
}

// boxSize returns the box's total on-wire size (children are included in the
// parent's stored size; leaf payload sizes cover the remaining bytes).
func boxSize(b *Box) int { return b.Size }

func parseSIDX(sidx *Box) *SIDXInfo {
	p := sidx.Payload
	info := &SIDXInfo{Version: sidx.Version}
	if len(p) < 8 {
		return info
	}
	info.Timescale = binary.BigEndian.Uint32(p[4:8])
	pos := 8
	if sidx.Version == 1 {
		if len(p) < pos+16 {
			return info
		}
		info.EarliestPTS = int64(binary.BigEndian.Uint64(p[pos : pos+8]))
		info.FirstOffset = binary.BigEndian.Uint64(p[pos+8 : pos+16])
		pos += 16
	} else {
		if len(p) < pos+8 {
			return info
		}
		info.EarliestPTS = int64(binary.BigEndian.Uint32(p[pos : pos+4]))
		info.FirstOffset = uint64(binary.BigEndian.Uint32(p[pos+4 : pos+8]))
		pos += 8
	}
	pos += 4 // reserved (16) + reference_count (16)
	if sidx.Version == 1 {
		if len(p) < pos+16 {
			return info
		}
		refSize := binary.BigEndian.Uint32(p[pos : pos+4])
		info.ReferencedSize = refSize & 0x7fffffff
		info.SubsegmentDuration = binary.BigEndian.Uint64(p[pos+4 : pos+12])
		sap := binary.BigEndian.Uint32(p[pos+12 : pos+16])
		info.SAPType = uint8(sap >> 28)
		info.SAPDeltaTime = sap & 0x0fffffff
		return info
	}
	if len(p) < pos+12 {
		return info
	}
	refSize := binary.BigEndian.Uint32(p[pos : pos+4])
	info.ReferencedSize = refSize & 0x7fffffff
	info.SubsegmentDuration = uint64(binary.BigEndian.Uint32(p[pos+4 : pos+8]))
	sap := binary.BigEndian.Uint32(p[pos+8 : pos+12])
	info.SAPType = uint8(sap >> 28)
	info.SAPDeltaTime = sap & 0x0fffffff
	return info
}
