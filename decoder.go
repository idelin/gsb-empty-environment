package tsblock

import (
	"encoding/binary"
	"math"
)

// Decoder reads samples back from a sealed frame, one frame at a time.
type Decoder struct {
	frame []byte

	numSamples uint64
	firstT     uint64
	firstDelta uint64
	firstV     uint64

	br bitReader

	index uint64 // index of the current sample within the frame
	pos   uint64 // next index Next should hand out
	curT  uint64
	curV  uint64
	lastV uint64

	haveWindow bool
	lead       uint
	trail      uint

	lastDelta uint64
	primed    bool
	err       error
}

// NewDecoder parses a sealed frame header and positions on its first sample.
func NewDecoder(frame []byte) (*Decoder, error) {
	d := &Decoder{frame: frame}
	if err := d.parseHeader(); err != nil {
		return nil, err
	}
	d.curT = d.firstT
	d.curV = d.firstV
	d.lastV = d.firstV
	d.index = 0
	d.pos = 0
	d.primed = true
	return d, nil
}

func (d *Decoder) parseHeader() error {
	b := d.frame
	if len(b) < 4 || b[0] != magic[0] || b[1] != magic[1] || b[2] != magic[2] || b[3] != magic[3] {
		return ErrCorrupt
	}
	off := 4

	n, m := binary.Uvarint(b[off:])
	if m <= 0 || n == 0 {
		return ErrCorrupt
	}
	d.numSamples = n
	off += m

	if len(b) < off+8 {
		return ErrCorrupt
	}
	d.firstT = binary.BigEndian.Uint64(b[off:])
	off += 8

	if d.numSamples >= 2 {
		if len(b) < off+8 {
			return ErrCorrupt
		}
		d.firstDelta = binary.BigEndian.Uint64(b[off:])
		off += 8
	}

	if len(b) < off+8 {
		return ErrCorrupt
	}
	d.firstV = binary.BigEndian.Uint64(b[off:])
	off += 8

	bitLen, m := binary.Uvarint(b[off:])
	if m <= 0 {
		return ErrCorrupt
	}
	off += m

	bodyBytes := (int(bitLen) + 7) / 8
	if len(b) != off+bodyBytes {
		return ErrCorrupt
	}
	d.br = bitReader{buf: b[off:]}
	return nil
}

// Count returns the number of samples in the frame.
func (d *Decoder) Count() uint64 { return d.numSamples }

// FirstTime returns the timestamp of the first sample.
func (d *Decoder) FirstTime() uint64 { return d.firstT }

// Time returns the timestamp of the current sample.
func (d *Decoder) Time() uint64 { return d.curT }

// Value returns the value of the current sample.
func (d *Decoder) Value() float64 { return math.Float64frombits(d.curV) }

// Index returns the zero-based position of the current sample in the frame.
func (d *Decoder) Index() uint64 { return d.index }

// Err returns the first corruption error encountered while scanning.
func (d *Decoder) Err() error { return d.err }

// Next advances to the next sample and reports whether one is available.
// Read the current sample with Time and Value. The first successful call after
// NewDecoder or Seek yields the sample the decoder is parked on; later calls
// decode the following samples. It returns false at end of frame or after a
// corruption error (see Err).
func (d *Decoder) Next() bool {
	if d.err != nil {
		return false
	}
	if !d.primed {
		return false
	}
	if d.pos == d.index {
		// Parked sample (sample 0 on open, or the Seek landing point).
		d.pos = d.index + 1
		return true
	}
	if d.index+1 >= d.numSamples {
		d.primed = false
		return false
	}
	if err := d.decodeNext(); err != nil {
		d.err = err
		d.primed = false
		return false
	}
	d.pos = d.index + 1
	return true
}

// decodeNext consumes one full (timestamp, value) pair after sample 0.
func (d *Decoder) decodeNext() error {
	nextT, err := d.decodeTimestamp()
	if err != nil {
		return err
	}
	nextV, err := d.decodeValue()
	if err != nil {
		return err
	}
	d.curT = nextT
	d.curV = nextV
	d.lastV = nextV
	d.index++
	return nil
}

// decodeTimestamp reads the next timestamp, updating delta state.
func (d *Decoder) decodeTimestamp() (uint64, error) {
	var delta int64
	if d.index == 0 {
		// Sample 1 carries the raw first delta.
		raw, err := d.br.readBits(64)
		if err != nil {
			return 0, err
		}
		d.lastDelta = raw
		return d.firstT + raw, nil
	}

	dod, err := d.readDeltaOfDelta()
	if err != nil {
		return 0, err
	}
	delta = int64(d.lastDelta) + dod
	if delta < 0 {
		return 0, ErrCorrupt
	}
	d.lastDelta = uint64(delta)
	return d.curT + uint64(delta), nil
}

// readDeltaOfDelta decodes the variable-length signed delta-of-delta.
func (d *Decoder) readDeltaOfDelta() (int64, error) {
	bit, err := d.br.readBits(1)
	if err != nil {
		return 0, err
	}
	if bit == 0 {
		return 0, nil
	}

	bit, err = d.br.readBits(1)
	if err != nil {
		return 0, err
	}
	if bit == 0 {
		v, err := d.br.readBits(7)
		return signExtend(v, 7), err
	}

	bit, err = d.br.readBits(1)
	if err != nil {
		return 0, err
	}
	if bit == 0 {
		v, err := d.br.readBits(9)
		return signExtend(v, 9), err
	}

	bit, err = d.br.readBits(1)
	if err != nil {
		return 0, err
	}
	if bit == 0 {
		v, err := d.br.readBits(12)
		return signExtend(v, 12), err
	}

	v, err := d.br.readBits(64)
	if err != nil {
		return 0, err
	}
	return int64(v), nil
}

// decodeValue reads the next XOR-compressed value.
func (d *Decoder) decodeValue() (uint64, error) {
	flag, err := d.br.readBits(1)
	if err != nil {
		return 0, err
	}
	if flag == 0 {
		return d.lastV, nil
	}

	flag, err = d.br.readBits(1)
	if err != nil {
		return 0, err
	}
	if flag == 0 {
		if !d.haveWindow {
			return 0, ErrCorrupt
		}
		meaningful := 64 - d.lead - d.trail
		mid, err := d.br.readBits(meaningful)
		if err != nil {
			return 0, err
		}
		xor := mid << d.trail
		return d.lastV ^ xor, nil
	}

	leadBits, err := d.br.readBits(5)
	if err != nil {
		return 0, err
	}
	lenBits, err := d.br.readBits(6)
	if err != nil {
		return 0, err
	}
	meaningful := uint(lenBits)
	lead := uint(leadBits)
	if lead > 31 || meaningful+lead > 64 {
		return 0, ErrCorrupt
	}
	if meaningful == 0 {
		// Escape descriptor: a full-word XOR with no zero padding. A real
		// descriptor always has meaningful >= 1, so lead must also be zero here.
		if lead != 0 {
			return 0, ErrCorrupt
		}
		xor, err := d.br.readBits(64)
		if err != nil {
			return 0, err
		}
		d.lead = 0
		d.trail = 0
		d.haveWindow = true
		return d.lastV ^ xor, nil
	}
	trail := 64 - lead - meaningful

	mid, err := d.br.readBits(meaningful)
	if err != nil {
		return 0, err
	}
	xor := mid << trail

	d.lead = lead
	d.trail = trail
	d.haveWindow = true
	return d.lastV ^ xor, nil
}

// skipValue consumes one value record while keeping the XOR window state in
// sync, without materializing the value.
func (d *Decoder) skipValue() error {
	v, err := d.decodeValue()
	if err != nil {
		return err
	}
	d.lastV = v
	d.curV = v
	return nil
}

// Seek moves to the first sample whose timestamp is >= t and reports whether
// such a sample exists. On false the decoder is left at the last sample.
//
// Seek works from the middle of a frame by scanning only the timestamp stream
// while advancing value state, so no earlier frame has to be fully decoded.
func (d *Decoder) Seek(t uint64) bool {
	d.resetToFirst()
	if d.firstT >= t {
		d.pos = d.index
		return true
	}
	for d.index+1 < d.numSamples {
		nextT, err := d.decodeTimestamp()
		if err != nil {
			d.err = err
			d.primed = false
			return false
		}
		d.curT = nextT
		d.index++
		matched := nextT >= t
		if err := d.skipValue(); err != nil {
			d.err = err
			d.primed = false
			return false
		}
		if matched {
			d.pos = d.index
			return true
		}
	}
	// No match: remain positioned on the last sample, still readable.
	d.primed = true
	d.pos = d.index
	return false
}

// resetToFirst restores decoder state as if freshly positioned on sample 0.
func (d *Decoder) resetToFirst() {
	d.br = bitReader{buf: d.br.buf}
	d.index = 0
	d.pos = 0
	d.curT = d.firstT
	d.curV = d.firstV
	d.lastV = d.firstV
	d.lastDelta = d.firstDelta
	d.haveWindow = false
	d.lead = 0
	d.trail = 0
	d.primed = true
	d.err = nil
}

// signExtend interprets v as an nbits-bit two's-complement integer.
func signExtend(v uint64, nbits uint) int64 {
	v &= 1<<nbits - 1
	if bit := uint64(1) << (nbits - 1); v&bit != 0 {
		v |= ^(1<<nbits - 1)
	}
	return int64(v)
}
