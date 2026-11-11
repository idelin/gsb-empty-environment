package h264bs

// Annex B byte streams delimit NAL units with start codes:
//
//	0x00 0x00 0x01        (3-byte)
//	0x00 0x00 0x00 0x01   (4-byte)
//
// A 4-byte start code must precede the first NAL unit in a stream and
// may be used to align the following NAL unit to a byte boundary.

// SplitAnnexB splits an Annex B byte stream into individual NAL units
// (EBSP bytes, start codes removed). Both 3-byte and 4-byte start codes
// are recognized; trailing zero padding after a NAL unit is discarded.
func SplitAnnexB(stream []byte) [][]byte {
	type span struct {
		start, end int
	}

	var starts []int
	for i := 0; i+2 < len(stream); i++ {
		if stream[i] == 0 && stream[i+1] == 0 && stream[i+2] == 1 {
			starts = append(starts, i+3)
			i += 2
		}
	}
	if len(starts) == 0 {
		return nil
	}

	spans := make([]span, 0, len(starts))
	for i, nalStart := range starts {
		var nalEnd int
		if i+1 < len(starts) {
			// Back up over the start code (including an optional
			// extra leading zero of a 4-byte start code) and any
			// additional zero padding.
			nalEnd = starts[i+1] - 3
			if nalEnd > nalStart && stream[nalEnd-1] == 0 {
				nalEnd--
			}
		} else {
			nalEnd = len(stream)
		}
		for nalEnd > nalStart && stream[nalEnd-1] == 0 {
			nalEnd--
		}
		if nalEnd > nalStart {
			spans = append(spans, span{nalStart, nalEnd})
		}
	}

	nals := make([][]byte, 0, len(spans))
	for _, s := range spans {
		nals = append(nals, append([]byte(nil), stream[s.start:s.end]...))
	}
	return nals
}

// PackAnnexB joins NAL units into an Annex B byte stream. Every NAL is
// prefixed with a 4-byte start code, giving canonical output.
func PackAnnexB(nals [][]byte) []byte {
	size := 0
	for _, nal := range nals {
		size += 4 + len(nal)
	}
	out := make([]byte, 0, size)
	for _, nal := range nals {
		out = append(out, 0, 0, 0, 1)
		out = append(out, nal...)
	}
	return out
}
