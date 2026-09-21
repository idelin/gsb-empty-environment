package bitlayout

// Encode validates the whole record and the target capacity first, then
// writes the record into buf atomically: if Encode returns an error, buf
// is guaranteed to be unmodified. It returns the record size in bits.
// Every bit of [0, nbits) is defined by Encode; bits beyond are untouched.
func (p *Plan) Encode(rec map[string]any, buf []byte) (int64, error) {
	nbits, err := p.Validate(rec)
	if err != nil {
		return 0, err
	}
	if nbits > int64(len(buf))*8 {
		return 0, errf(ErrCapacity, "/", int64(len(buf))*8,
			"record needs %d bits, buffer holds %d", nbits, int64(len(buf))*8)
	}
	// Define every bit of the record region regardless of prior buffer
	// contents (padding must be zero, writer ORs bits in).
	zeroBits(buf, p.BitOrder, 0, nbits)
	w := newBitWriter(buf, p.BitOrder)
	ew := &encodeWriter{plan: p, w: w}
	if err := ew.writeScope(p.root, rec, nil); err != nil {
		// Unreachable: Validate already approved every value. Guarded
		// anyway so a bug can never report success on a partial write.
		return 0, err
	}
	return w.pos, nil
}

// Validate checks the record against the plan without writing anything
// and returns the exact encoded size in bits.
func (p *Plan) Validate(rec map[string]any) (int64, error) {
	v := &validator{plan: p}
	if err := v.checkScope(p.root, rec, nil); err != nil {
		return 0, err
	}
	return v.pos, nil
}

// validator walks the record computing sizes and checking every value.
// pos tracks the current bit offset.
type validator struct {
	plan *Plan
	pos  int64
}

func (v *validator) checkScope(n *node, rec map[string]any, topStarts []int64) error {
	if n == v.plan.root {
		topStarts = make([]int64, len(n.fields))
	}
	for i, f := range n.fields {
		if n == v.plan.root {
			topStarts[i] = v.pos
		}
		if err := v.checkNode(f, rec, topStarts); err != nil {
			return err
		}
	}
	return nil
}

func (v *validator) checkNode(f *node, rec map[string]any, topStarts []int64) error {
	switch f.kind {
	case kindInt:
		val, ok := rec[f.name]
		if !ok {
			return errf(ErrValue, f.path, v.pos, "missing field %q", f.name)
		}
		if err := checkIntValue(f, val, v.pos); err != nil {
			return err
		}
		v.pos += int64(f.bits)
	case kindStruct:
		sub, ok := rec[f.name].(map[string]any)
		if !ok {
			return errf(ErrValue, f.path, v.pos, "field %q must be an object", f.name)
		}
		return v.checkScope(f, sub, topStarts)
	case kindArray:
		arr, ok := rec[f.name].([]any)
		if !ok {
			return errf(ErrValue, f.path, v.pos, "field %q must be an array", f.name)
		}
		want, err := toUint(rec[f.lengthFrom.name])
		if err != nil {
			return errf(ErrValue, f.path, v.pos, "bad length field: %v", err)
		}
		if uint64(len(arr)) != want {
			return errf(ErrValue, f.path, v.pos,
				"array has %d elements but %q says %d", len(arr), f.lengthFrom.name, want)
		}
		for _, e := range arr {
			if err := v.checkValue(f.elem, e, topStarts); err != nil {
				return err
			}
		}
	case kindUnion:
		sub, ok := rec[f.name].(map[string]any)
		if !ok {
			return errf(ErrValue, f.path, v.pos, "field %q must be an object", f.name)
		}
		tag, err := toUint(rec[f.tagFrom.name])
		if err != nil {
			return errf(ErrValue, f.path, v.pos, "bad tag field: %v", err)
		}
		cn, ok := f.cases[tag]
		if !ok {
			return errf(ErrValue, f.path, v.pos, "undefined tag value %d", tag)
		}
		return v.checkScope(cn, sub, topStarts)
	case kindPad:
		v.pos += int64(padBits(v.pos, f.align))
	case kindChecksum:
		// The covered range is fully laid out by now (compile-time
		// guarantee), so the value is computable during the write pass.
		v.pos += int64(f.bits)
	}
	return nil
}

func (v *validator) checkValue(n *node, val any, topStarts []int64) error {
	switch n.kind {
	case kindInt:
		if err := checkIntValue(n, val, v.pos); err != nil {
			return err
		}
		v.pos += int64(n.bits)
	case kindStruct:
		sub, ok := val.(map[string]any)
		if !ok {
			return errf(ErrValue, n.path, v.pos, "array element must be an object")
		}
		return v.checkScope(n, sub, topStarts)
	}
	return nil
}

func checkIntValue(f *node, val any, pos int64) error {
	min, max, umax := intRange(f.bits, f.signed)
	if f.signed {
		sv, ok := val.(int64)
		if !ok {
			return errf(ErrValue, f.path, pos, "field %q must be a signed integer, got %T", f.name, val)
		}
		if sv < min || sv > max {
			return errf(ErrValue, f.path, pos, "value %d out of range [%d,%d]", sv, min, max)
		}
		return nil
	}
	uv, ok := val.(uint64)
	if !ok {
		return errf(ErrValue, f.path, pos, "field %q must be an unsigned integer, got %T", f.name, val)
	}
	if uv > umax {
		return errf(ErrValue, f.path, pos, "value %d out of range [0,%d]", uv, umax)
	}
	return nil
}

func padBits(pos int64, align int) int {
	a := int64(align)
	return int((a - pos%a) % a)
}

// encodeWriter performs the actual write. It must only be called after a
// successful Validate, so every value is known to fit.
type encodeWriter struct {
	plan *Plan
	w    *bitWriter
}

func (e *encodeWriter) writeScope(n *node, rec map[string]any, topStarts []int64) error {
	if n == e.plan.root {
		topStarts = make([]int64, len(n.fields))
	}
	for i, f := range n.fields {
		if n == e.plan.root {
			topStarts[i] = e.w.pos
		}
		if err := e.writeNode(f, rec, topStarts); err != nil {
			return err
		}
	}
	return nil
}

func (e *encodeWriter) writeNode(f *node, rec map[string]any, topStarts []int64) error {
	switch f.kind {
	case kindInt:
		e.writeInt(f, rec[f.name])
	case kindStruct:
		return e.writeScope(f, rec[f.name].(map[string]any), topStarts)
	case kindArray:
		for _, el := range rec[f.name].([]any) {
			e.writeValue(f.elem, el, topStarts)
		}
	case kindUnion:
		tag, _ := toUint(rec[f.tagFrom.name])
		return e.writeScope(f.cases[tag], rec[f.name].(map[string]any), topStarts)
	case kindPad:
		e.w.pos += int64(padBits(e.w.pos, f.align)) // region pre-zeroed
	case kindChecksum:
		start := topStarts[f.fromIx]
		end := e.w.pos // the 'to' field ends where the checksum begins
		sum := checksum(f.algo, checksumBytes(e.w.buf, e.plan.BitOrder, start, end), f.bits)
		e.w.field(sum, f.bits, e.plan.ByteOrder)
	}
	return nil
}

func (e *encodeWriter) writeInt(f *node, val any) {
	if f.signed {
		e.w.field(uint64(val.(int64))&mask(f.bits), f.bits, e.plan.ByteOrder)
		return
	}
	e.w.field(val.(uint64), f.bits, e.plan.ByteOrder)
}

func (e *encodeWriter) writeValue(n *node, val any, topStarts []int64) {
	switch n.kind {
	case kindInt:
		e.writeInt(n, val)
	case kindStruct:
		e.writeScope(n, val.(map[string]any), topStarts)
	}
}

// zeroBits clears the stream bits [start,end) in buf.
func zeroBits(buf []byte, order BitOrder, start, end int64) {
	for i := start; i < end; i++ {
		bi := i >> 3
		if order == MSBFirst {
			buf[bi] &^= 0x80 >> (uint(i) & 7)
		} else {
			buf[bi] &^= 1 << (uint(i) & 7)
		}
	}
}
