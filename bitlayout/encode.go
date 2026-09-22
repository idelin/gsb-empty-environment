package bitlayout

// encScope tracks the encoded bit range of each field at one struct level.
type encScope struct {
	pos map[string][2]uint64
}

type encoder struct {
	plan *Plan
	w    *bitWriter // nil during the sizing/validation pass
	pos  uint64
}

// Encode validates the whole object against the plan, checks that dst can
// hold the record, and only then writes into dst. If an error is returned,
// dst is guaranteed to be unmodified. It returns the number of bits written.
func (p *Plan) Encode(obj map[string]any, dst []byte) (int, error) {
	sz := &encoder{plan: p}
	if err := sz.encodeStruct(p.fields, obj, &encScope{pos: map[string][2]uint64{}}, p.Name); err != nil {
		return 0, err
	}
	total := sz.pos
	if total > uint64(len(dst))*8 {
		return 0, &Error{Kind: ErrCapacity, Path: p.Name, Bit: total,
			Msg: "record does not fit destination buffer"}
	}
	e := &encoder{plan: p, w: &bitWriter{buf: dst, bo: p.BitOrder}}
	// Every check that can fail already ran in the sizing pass, so this
	// pass cannot fail; the error is checked defensively.
	if err := e.encodeStruct(p.fields, obj, &encScope{pos: map[string][2]uint64{}}, p.Name); err != nil {
		return 0, err
	}
	return int(e.pos), nil
}

// EncodeBits is a convenience wrapper that allocates the destination buffer.
func (p *Plan) EncodeBits(obj map[string]any) ([]byte, int, error) {
	sz := &encoder{plan: p}
	if err := sz.encodeStruct(p.fields, obj, &encScope{pos: map[string][2]uint64{}}, p.Name); err != nil {
		return nil, 0, err
	}
	dst := make([]byte, (sz.pos+7)/8)
	n, err := p.Encode(obj, dst)
	if err != nil {
		return nil, 0, err
	}
	return dst, n, nil
}

func (e *encoder) encodeStruct(fields []cfield, obj map[string]any, sc *encScope, path string) error {
	for i := range fields {
		f := &fields[i]
		start := e.pos
		switch f.typ {
		case tUint:
			raw, present := obj[f.name]
			v, ok := asUint(raw)
			if !present || !ok {
				return e.valueErr(path, f.name, "missing or not an unsigned integer")
			}
			if f.bits < 64 && v > mask64(f.bits) {
				return e.valueErr(path, f.name, "value %d does not fit %d bits", v, f.bits)
			}
			if len(f.enum) > 0 && !containsUint(f.enum, v) {
				return e.valueErr(path, f.name, "value %d not in enum", v)
			}
			e.emit(v, f.bits)

		case tInt:
			raw, present := obj[f.name]
			v, ok := asInt(raw)
			if !present || !ok {
				return e.valueErr(path, f.name, "missing or not a signed integer")
			}
			if f.bits < 64 {
				min := -int64(1) << uint(f.bits-1)
				max := int64(1)<<uint(f.bits-1) - 1
				if v < min || v > max {
					return e.valueErr(path, f.name, "value %d does not fit %d signed bits", v, f.bits)
				}
			}
			e.emit(uint64(v)&mask64(f.bits), f.bits)

		case tArray:
			cntRaw, present := obj[f.lengthFrom]
			cnt, ok := asUint(cntRaw)
			if !present || !ok {
				return e.valueErr(path, f.name, "length field %q missing", f.lengthFrom)
			}
			arr, ok := obj[f.name].([]any)
			if !ok {
				return e.valueErr(path, f.name, "missing or not an array")
			}
			if uint64(len(arr)) != cnt {
				return e.valueErr(path, f.name,
					"array has %d elements but %q is %d", len(arr), f.lengthFrom, cnt)
			}
			for idx, ev := range arr {
				em, ok := ev.(map[string]any)
				if !ok {
					return e.valueErr(path, f.name, "element %d is not an object", idx)
				}
				if err := e.encodeStruct(f.elem, em, &encScope{pos: map[string][2]uint64{}}, path+"."+f.name); err != nil {
					return err
				}
			}

		case tUnion:
			tagRaw, present := obj[f.tagFrom]
			tag, ok := asUint(tagRaw)
			if !present || !ok {
				return e.valueErr(path, f.name, "tag field %q missing", f.tagFrom)
			}
			caseFields, ok := f.cases[tag]
			if !ok {
				return e.valueErr(path, f.name, "undefined tag %d", tag)
			}
			body, ok := obj[f.name].(map[string]any)
			if !ok {
				return e.valueErr(path, f.name, "missing or not an object")
			}
			if err := e.encodeStruct(caseFields, body, sc, path+"."+f.name); err != nil {
				return err
			}

		case tAlign:
			e.emit(0, alignPad(e.pos, f.multiple))

		case tChecksum:
			if e.w != nil {
				from, okFrom := sc.pos[f.from]
				to, okTo := sc.pos[f.to]
				if !okFrom || !okTo {
					return e.valueErr(path, f.name, "checksum range fields missing")
				}
				r := bitReader{buf: e.w.buf, nbits: e.pos, bo: e.plan.BitOrder}
				sum := checksumSum(&r, from[0], to[1], f.bits)
				e.emit(sum, f.bits)
			} else {
				e.pos += uint64(f.bits)
			}
		}
		sc.pos[f.name] = [2]uint64{start, e.pos}
	}
	return nil
}

func (e *encoder) emit(v uint64, n int) {
	if e.w != nil {
		emitField(e.w, &e.pos, v, n, e.plan.ByteOrder)
	} else {
		e.pos += uint64(n)
	}
}

func (e *encoder) valueErr(path, field, format string, args ...any) *Error {
	return &Error{Kind: ErrIllegalValue, Path: path + "." + field, Bit: e.pos,
		Msg: sprintf(format, args...)}
}
