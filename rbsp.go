package h264bs

// EBSP (encapsulated byte stream payload) inserts an emulation
// prevention byte 0x03 after every 0x00 0x00 sequence whose next byte
// is 0x00, 0x01, 0x02 or 0x03, so that NAL start codes (00 00 01) can
// never appear inside a NAL unit.

// UnescapeRBSP removes emulation prevention bytes from EBSP data and
// returns the raw RBSP bytes.
func UnescapeRBSP(ebsp []byte) ([]byte, error) {
	zeros := 0
	rbsp := make([]byte, 0, len(ebsp))
	for i, b := range ebsp {
		if zeros >= 2 && b == 0x03 {
			// A prevention byte at the very end of the EBSP is legal
			// (it may coincide with rbsp_trailing_bits); only a
			// forbidden follower value makes the sequence invalid.
			if i+1 < len(ebsp) && ebsp[i+1] > 0x03 {
				return nil, ErrInvalidRBSP
			}
			zeros = 0
			continue
		}
		rbsp = append(rbsp, b)
		if b == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return rbsp, nil
}

// EscapeRBSP inserts emulation prevention bytes into raw RBSP data and
// returns EBSP data suitable for Annex B / AVCC NAL units.
func EscapeRBSP(rbsp []byte) []byte {
	ebsp := make([]byte, 0, len(rbsp)+len(rbsp)/16)
	zeros := 0
	for _, b := range rbsp {
		if zeros >= 2 && b <= 0x03 {
			ebsp = append(ebsp, 0x03)
			zeros = 0
		}
		ebsp = append(ebsp, b)
		if b == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return ebsp
}

// bitReader reads big-endian bits from an RBSP buffer and decodes
// unsigned/signed Exp-Golomb coded values (ITU-T H.264 9.1).
type bitReader struct {
	data []byte
	pos  int // bit position
}

func newBitReader(data []byte) *bitReader {
	return &bitReader{data: data}
}

// readBit returns one bit.
func (r *bitReader) readBit() (uint, error) {
	byteIndex := r.pos >> 3
	if byteIndex >= len(r.data) {
		return 0, ErrTruncated
	}
	bit := uint((r.data[byteIndex] >> (7 - uint(r.pos&7))) & 1)
	r.pos++
	return bit, nil
}

// readBits returns n bits as an unsigned integer (n <= 32).
func (r *bitReader) readBits(n int) (uint, error) {
	var v uint
	for i := 0; i < n; i++ {
		b, err := r.readBit()
		if err != nil {
			return 0, err
		}
		v = (v << 1) | b
	}
	return v, nil
}

// readUE decodes an unsigned Exp-Golomb code (ue(v)).
func (r *bitReader) readUE() (uint, error) {
	leadingZeros := 0
	for {
		b, err := r.readBit()
		if err != nil {
			return 0, err
		}
		if b == 1 {
			break
		}
		leadingZeros++
		if leadingZeros > 32 {
			return 0, ErrInvalidRBSP
		}
	}
	if leadingZeros == 0 {
		return 0, nil
	}
	suffix, err := r.readBits(leadingZeros)
	if err != nil {
		return 0, err
	}
	return (uint(1) << leadingZeros) - 1 + suffix, nil
}

// readSE decodes a signed Exp-Golomb code (se(v)).
func (r *bitReader) readSE() (int, error) {
	codeNum, err := r.readUE()
	if err != nil {
		return 0, err
	}
	if codeNum&1 == 1 {
		return int((codeNum + 1) / 2), nil
	}
	return -int(codeNum / 2), nil
}

// moreRBSPData reports whether bits other than rbsp_trailing_bits remain.
func (r *bitReader) moreRBSPData() bool {
	if r.pos >= len(r.data)*8 {
		return false
	}
	return r.data[r.pos>>3]&(0xff>>uint(r.pos&7)) != 0
}

// byteAligned reports the reader position on a byte boundary.
func (r *bitReader) byteAligned() bool {
	return r.pos&7 == 0
}

// readTrailingBits consumes rbsp_trailing_bits and verifies the stop bit.
func (r *bitReader) readTrailingBits() error {
	stop, err := r.readBit()
	if err != nil {
		return err
	}
	if stop != 1 {
		return ErrInvalidRBSP
	}
	for !r.byteAligned() {
		b, err := r.readBit()
		if err != nil {
			return err
		}
		if b != 0 {
			return ErrInvalidRBSP
		}
	}
	return nil
}
