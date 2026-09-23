package rangecoder

// Byte-oriented 32-bit range coder (Subbotin style, carry handled via a
// cache plus pending-0xFF counter). Pure standard-library implementation;
// no compression libraries are used.

const rcTop = 1 << 24

type rangeEncoder struct {
	out       []byte
	low       uint64
	rng       uint32
	cache     byte
	cacheSize uint32
}

func newRangeEncoder() *rangeEncoder {
	return &rangeEncoder{rng: 0xFFFFFFFF, cacheSize: 1}
}

func (e *rangeEncoder) shiftLow() {
	if uint32(e.low) < 0xFF000000 || e.low>>32 != 0 {
		carry := byte(e.low >> 32)
		temp := e.cache
		for {
			e.out = append(e.out, temp+carry)
			temp = 0xFF
			e.cacheSize--
			if e.cacheSize == 0 {
				break
			}
		}
		e.cache = byte(e.low >> 24)
	}
	e.cacheSize++
	e.low = uint64(uint32(e.low << 8))
}

func (e *rangeEncoder) encode(cum, freq, total uint32) {
	r := e.rng / total
	e.low += uint64(r) * uint64(cum)
	e.rng = r * freq
	for e.rng < rcTop {
		e.shiftLow()
		e.rng <<= 8
	}
}

// flush emits the final state: one leading dummy byte (from the initial
// cacheSize=1) plus the 4 significant bytes of low. The decoder mirrors
// this by reading 5 bytes at init, which drops the dummy naturally.
func (e *rangeEncoder) flush() {
	for i := 0; i < 5; i++ {
		e.shiftLow()
	}
}

type rangeDecoder struct {
	data []byte
	pos  int
	code uint32
	rng  uint32
}

func newRangeDecoder(data []byte) *rangeDecoder {
	d := &rangeDecoder{data: data, rng: 0xFFFFFFFF}
	for i := 0; i < 5; i++ {
		d.code = d.code<<8 | d.readByte()
	}
	return d
}

// readByte returns 0 past the end of the stream; the EOF symbol and the
// output quota are what actually terminate decoding.
func (d *rangeDecoder) readByte() uint32 {
	if d.pos >= len(d.data) {
		return 0
	}
	b := d.data[d.pos]
	d.pos++
	return uint32(b)
}

func (d *rangeDecoder) value(total uint32) uint32 {
	return d.code / (d.rng / total)
}

func (d *rangeDecoder) update(cum, freq, total uint32) {
	r := d.rng / total
	d.code -= r * cum
	d.rng = r * freq
	for d.rng < rcTop {
		d.rng <<= 8
		d.code = d.code<<8 | d.readByte()
	}
}
