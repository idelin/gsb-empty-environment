package cabac

// Decoder is the mirror of Encoder; given the same model sequence it
// reproduces the original bin stream.
type Decoder struct {
	low, high uint32
	code      uint32
	r         bitReader
}

// NewDecoder primes the decoder with the first 32 stream bits.
func NewDecoder(data []byte) *Decoder {
	d := &Decoder{low: 0, high: topValue}
	d.r.buf = data
	for i := 0; i < 32; i++ {
		d.code = (d.code << 1) | d.r.readBit()
	}
	return d
}

// DecodeBin decodes one binary symbol using the given model, then adapts
// it exactly as the encoder did.
func (d *Decoder) DecodeBin(m Model) uint32 {
	rng := uint64(d.high) - uint64(d.low) + 1
	lpsRange := (rng * uint64(m.ProbLPS())) >> 16
	if lpsRange < 1 {
		lpsRange = 1
	}
	if lpsRange >= rng {
		lpsRange = rng - 1
	}
	mpsHigh := uint64(d.low) + (rng - lpsRange) - 1
	var bin uint32
	if uint64(d.code) <= mpsHigh {
		d.high = uint32(mpsHigh)
		bin = m.MPS()
	} else {
		d.low += uint32(rng - lpsRange)
		d.high = d.low + uint32(lpsRange) - 1
		bin = m.MPS() ^ 1
	}
	m.Update(bin)
	d.renormalize()
	return bin
}

func (d *Decoder) renormalize() {
	for {
		if d.high < half {
			// no offset to remove
		} else if d.low >= half {
			d.low -= half
			d.high -= half
			d.code -= half
		} else if d.low >= firstQtr && d.high < thirdQtr {
			d.low -= firstQtr
			d.high -= firstQtr
			d.code -= firstQtr
		} else {
			return
		}
		d.low <<= 1
		d.high = (d.high << 1) | 1
		d.code = (d.code << 1) | d.r.readBit()
	}
}
