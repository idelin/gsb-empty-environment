package bitlayout

// This file is an independent, deliberately naive bit-by-bit reference
// implementation used to cross-check the production encoder and decoder.
// It shares no bit-manipulation code with the production path.

// refBit reads a single bit at absolute stream position pos.
func refBit(data []byte, bo BitOrder, pos uint64) uint {
	b := data[pos/8]
	if bo == MSBFirst {
		return uint((b >> (7 - pos%8)) & 1)
	}
	return uint((b >> (pos % 8)) & 1)
}

// refPutBit writes a single bit at absolute stream position pos.
func refPutBit(data []byte, bo BitOrder, pos uint64, bit uint) {
	i := pos / 8
	var m byte
	if bo == MSBFirst {
		m = 1 << (7 - pos%8)
	} else {
		m = 1 << (pos % 8)
	}
	if bit == 1 {
		data[i] |= m
	} else {
		data[i] &^= m
	}
}

// refReadVal reads an n-bit field at pos, honouring byte order, one bit at
// a time. It returns the value and the position after the field.
func refReadVal(data []byte, nbits uint64, bo BitOrder, yo ByteOrder, pos uint64, n int) (uint64, uint64, bool) {
	if n == 0 {
		return 0, pos, true
	}
	if pos+uint64(n) > nbits {
		return 0, pos, false
	}
	var v uint64
	if yo == BigEndian {
		for i := 0; i < n; i++ {
			v = v<<1 | uint64(refBit(data, bo, pos+uint64(i)))
		}
		return v, pos + uint64(n), true
	}
	sh := 0
	for sh < n {
		k := 8
		if n-sh < 8 {
			k = n - sh
		}
		var b uint64
		for j := 0; j < k; j++ {
			b = b<<1 | uint64(refBit(data, bo, pos+uint64(sh+j)))
		}
		v |= b << uint(sh)
		sh += k
	}
	return v, pos + uint64(n), true
}

// refWriteVal appends an n-bit field to the stream bit by bit, growing buf.
func refWriteVal(buf []byte, bo BitOrder, yo ByteOrder, pos uint64, v uint64, n int) ([]byte, uint64) {
	need := (pos + uint64(n) + 7) / 8
	for uint64(len(buf)) < need {
		buf = append(buf, 0)
	}
	if yo == BigEndian {
		for i := 0; i < n; i++ {
			bit := uint((v >> uint(n-1-i)) & 1)
			refPutBit(buf, bo, pos+uint64(i), bit)
		}
		return buf, pos + uint64(n)
	}
	sh := 0
	for sh < n {
		k := 8
		if n-sh < 8 {
			k = n - sh
		}
		for j := 0; j < k; j++ {
			bit := uint((v >> uint(sh+k-1-j)) & 1)
			refPutBit(buf, bo, pos+uint64(sh+j), bit)
		}
		sh += k
	}
	return buf, pos + uint64(n)
}

// refChecksum recomputes the "sum" checksum with its own bit loop.
func refChecksum(data []byte, bo BitOrder, from, to uint64, bits int) uint64 {
	var sum uint64
	for pos := from; pos < to; {
		n := to - pos
		k := 8
		if n < 8 {
			k = int(n)
		}
		var b uint64
		for j := 0; j < k; j++ {
			b = b<<1 | uint64(refBit(data, bo, pos+uint64(j)))
		}
		if k < 8 {
			b <<= uint(8 - k)
		}
		sum += b
		if bits < 64 {
			sum &= (uint64(1) << uint(bits)) - 1
		}
		pos += uint64(k)
	}
	return sum
}

// refEncode encodes obj field by field, bit by bit.
func refEncode(p *Plan, obj map[string]any) ([]byte, uint64, error) {
	w := &refWriter{bo: p.BitOrder, yo: p.ByteOrder}
	if err := refEncodeFields(p, w, p.fields, obj, map[string][2]uint64{}); err != nil {
		return nil, 0, err
	}
	return w.buf, w.pos, nil
}

type refWriter struct {
	buf []byte
	pos uint64
	bo  BitOrder
	yo  ByteOrder
}

func (w *refWriter) write(v uint64, n int) {
	w.buf, w.pos = refWriteVal(w.buf, w.bo, w.yo, w.pos, v, n)
}

func refEncodeFields(p *Plan, w *refWriter, fields []cfield, obj map[string]any, pos map[string][2]uint64) error {
	for i := range fields {
		f := &fields[i]
		start := w.pos
		switch f.typ {
		case tUint:
			v, ok := asUint(obj[f.name])
			if !ok {
				return &Error{Kind: ErrIllegalValue, Path: f.name, Msg: "bad uint"}
			}
			w.write(v, f.bits)
		case tInt:
			v, ok := asInt(obj[f.name])
			if !ok {
				return &Error{Kind: ErrIllegalValue, Path: f.name, Msg: "bad int"}
			}
			w.write(uint64(v)&mask64(f.bits), f.bits)
		case tArray:
			cnt, _ := asUint(obj[f.lengthFrom])
			arr, _ := obj[f.name].([]any)
			if uint64(len(arr)) != cnt {
				return &Error{Kind: ErrIllegalValue, Path: f.name, Msg: "length mismatch"}
			}
			for _, ev := range arr {
				if err := refEncodeFields(p, w, f.elem, ev.(map[string]any), map[string][2]uint64{}); err != nil {
					return err
				}
			}
		case tUnion:
			tag, _ := asUint(obj[f.tagFrom])
			caseFields, ok := f.cases[tag]
			if !ok {
				return &Error{Kind: ErrIllegalValue, Path: f.name, Msg: "undefined tag"}
			}
			if err := refEncodeFields(p, w, caseFields, obj[f.name].(map[string]any), pos); err != nil {
				return err
			}
		case tAlign:
			n := alignPad(w.pos, f.multiple)
			for j := 0; j < n; j++ {
				w.buf, w.pos = refWriteVal(w.buf, w.bo, BigEndian, w.pos, 0, 1)
			}
		case tChecksum:
			sum := refChecksum(w.buf, w.bo, pos[f.from][0], pos[f.to][1], f.bits)
			w.write(sum, f.bits)
		}
		pos[f.name] = [2]uint64{start, w.pos}
	}
	return nil
}

// refDecode decodes a record bit by bit. It returns the decoded object and
// the number of bits consumed, or an error with the same kind the
// production decoder would report.
func refDecode(p *Plan, data []byte, nbits uint64) (map[string]any, uint64, *Error) {
	d := &refDecoder{data: data, nbits: nbits, bo: p.BitOrder, yo: p.ByteOrder}
	obj := map[string]any{}
	if err := d.fields(p, p.fields, obj, map[string][2]uint64{}); err != nil {
		return nil, 0, err
	}
	return obj, d.pos, nil
}

type refDecoder struct {
	data  []byte
	nbits uint64
	pos   uint64
	bo    BitOrder
	yo    ByteOrder
}

func (d *refDecoder) read(n int) (uint64, bool) {
	v, np, ok := refReadVal(d.data, d.nbits, d.bo, d.yo, d.pos, n)
	if !ok {
		return 0, false
	}
	d.pos = np
	return v, true
}

func (d *refDecoder) fields(p *Plan, fields []cfield, obj map[string]any, pos map[string][2]uint64) *Error {
	for i := range fields {
		f := &fields[i]
		start := d.pos
		switch f.typ {
		case tUint, tInt:
			v, ok := d.read(f.bits)
			if !ok {
				return &Error{Kind: ErrNeedInput, Path: f.name, Bit: start}
			}
			if f.typ == tUint && len(f.enum) > 0 && !containsUint(f.enum, v) {
				return &Error{Kind: ErrIllegalValue, Path: f.name, Bit: start}
			}
			if f.typ == tInt {
				obj[f.name] = signExtend(v, f.bits)
			} else {
				obj[f.name] = v
			}
		case tArray:
			cnt, _ := obj[f.lengthFrom].(uint64)
			arr := make([]any, 0, cnt)
			for j := uint64(0); j < cnt; j++ {
				el := map[string]any{}
				if err := d.fields(p, f.elem, el, map[string][2]uint64{}); err != nil {
					return err
				}
				arr = append(arr, el)
			}
			obj[f.name] = arr
		case tUnion:
			tag, _ := obj[f.tagFrom].(uint64)
			caseFields, ok := f.cases[tag]
			if !ok {
				return &Error{Kind: ErrIllegalValue, Path: f.name, Bit: start}
			}
			body := map[string]any{}
			if err := d.fields(p, caseFields, body, map[string][2]uint64{}); err != nil {
				return err
			}
			obj[f.name] = body
		case tAlign:
			n := alignPad(d.pos, f.multiple)
			for j := 0; j < n; j++ {
				bit, ok := d.read(1)
				if !ok {
					return &Error{Kind: ErrNeedInput, Path: f.name, Bit: d.pos}
				}
				if bit != 0 {
					return &Error{Kind: ErrReservedBits, Path: f.name, Bit: d.pos - 1}
				}
			}
		case tChecksum:
			got, ok := d.read(f.bits)
			if !ok {
				return &Error{Kind: ErrNeedInput, Path: f.name, Bit: start}
			}
			want := refChecksum(d.data, d.bo, pos[f.from][0], pos[f.to][1], f.bits)
			if got != want {
				return &Error{Kind: ErrIllegalValue, Path: f.name, Bit: start}
			}
			obj[f.name] = got
		}
		pos[f.name] = [2]uint64{start, d.pos}
	}
	return nil
}
