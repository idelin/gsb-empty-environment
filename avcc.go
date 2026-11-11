package fmp4mux

import "encoding/binary"

// NAL unit type values defined by H.264 (ITU-T H.264 Table 7-1).
const (
	nalTypeSPS = 7
	nalTypePPS = 8
	nalTypeIDR = 5
)

// splitAnnexB splits an Annex B byte stream into raw NAL units, dropping
// start codes and emulation-prevention bytes are left untouched.
func splitAnnexB(data []byte) [][]byte {
	var nals [][]byte
	n := len(data)
	zeroRun := 0
	nalStart := -1

	emit := func(end int) {
		if nalStart >= 0 && end > nalStart {
			nals = append(nals, data[nalStart:end])
		}
		nalStart = -1
	}

	for i := 0; i < n; i++ {
		b := data[i]
		if b == 0 {
			zeroRun++
			continue
		}
		if b == 1 && zeroRun >= 2 {
			// Start code: any NAL bytes collected so far end before the zeros.
			emit(i - zeroRun)
			nalStart = i + 1
		} else if nalStart < 0 {
			// Leading bytes before the first start code are ignored.
		}
		zeroRun = 0
	}
	emit(n)
	return nals
}

// splitLengthPrefixed splits a length-prefixed (AVCC) sample into NAL units.
func splitLengthPrefixed(data []byte) ([][]byte, bool) {
	var nals [][]byte
	i := 0
	for i+4 <= len(data) {
		l := int(binary.BigEndian.Uint32(data[i:]))
		i += 4
		if l == 0 || i+l > len(data) {
			return nil, false
		}
		nals = append(nals, data[i:i+l])
		i += l
	}
	if i != len(data) {
		return nil, false
	}
	return nals, true
}

// splitNALs accepts either Annex B or 4-byte-length-prefixed input.
func splitNALs(data []byte) [][]byte {
	if nals, ok := splitLengthPrefixed(data); ok {
		return nals
	}
	return splitAnnexB(data)
}

// toAVCSample converts an access unit to an AVC sample: every NAL unit
// prefixed with a 4-byte big-endian length (AVCC format used in mdat).
func toAVCSample(au []byte) []byte {
	nals := splitNALs(au)
	total := 4 * len(nals)
	for _, nal := range nals {
		total += len(nal)
	}
	out := make([]byte, 0, total)
	for _, nal := range nals {
		var lenbuf [4]byte
		binary.BigEndian.PutUint32(lenbuf[:], uint32(len(nal)))
		out = append(out, lenbuf[:]...)
		out = append(out, nal...)
	}
	return out
}

// nalType returns the NAL unit type of a raw NAL unit.
func nalType(nal []byte) uint8 {
	if len(nal) == 0 {
		return 0
	}
	return nal[0] & 0x1f
}

// extractParameterSets pulls the first SPS and PPS NAL units out of an AU.
func extractParameterSets(au []byte) (sps, pps []byte) {
	for _, nal := range splitNALs(au) {
		switch nalType(nal) {
		case nalTypeSPS:
			if sps == nil {
				sps = append([]byte(nil), nal...)
			}
		case nalTypePPS:
			if pps == nil {
				pps = append([]byte(nil), nal...)
			}
		}
	}
	return sps, pps
}

// containsIDR reports whether the access unit contains an IDR slice NAL.
func containsIDR(au []byte) bool {
	for _, nal := range splitNALs(au) {
		if nalType(nal) == nalTypeIDR {
			return true
		}
	}
	return false
}

// buildAVCC builds the AVCDecoderConfigurationRecord payload (the payload of
// the avcC box, without its box header).
func buildAVCC(sps, pps []byte) []byte {
	var w bw
	w.u8(1)      // configurationVersion
	w.u8(sps[1]) // AVCProfileIndication
	w.u8(sps[2]) // profile_compatibility
	w.u8(sps[3]) // AVCLevelIndication
	w.u8(0xfc)   // lengthSizeMinusOne = 3 (4-byte NAL lengths), reserved bits
	w.u8(0xe1)   // numOfSequenceParameterSets = 1, reserved bits
	w.u16(uint16(len(sps)))
	w.b(sps)
	w.u8(1) // numOfPictureParameterSets
	w.u16(uint16(len(pps)))
	w.b(pps)
	return w.Bytes()
}
