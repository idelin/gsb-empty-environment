package bitlayout

import "math/bits"

// DecodeOptions carries the per-session decode quotas. The zero value
// uses the defaults below. Quotas bound the work any single record can
// cause, independent of what the length fields claim.
type DecodeOptions struct {
	// MaxDepth bounds nesting of arrays/structs/unions. Default 8.
	MaxDepth int
	// MaxTotalElements bounds the sum of array lengths in one record.
	// Default 65536.
	MaxTotalElements uint64
	// MaxTotalBits bounds the record size in bits. Default 1<<20.
	MaxTotalBits int64
}

func (o *DecodeOptions) withDefaults() DecodeOptions {
	d := DecodeOptions{MaxDepth: 8, MaxTotalElements: 1 << 16, MaxTotalBits: 1 << 20}
	if o != nil {
		if o.MaxDepth > 0 {
			d.MaxDepth = o.MaxDepth
		}
		if o.MaxTotalElements > 0 {
			d.MaxTotalElements = o.MaxTotalElements
		}
		if o.MaxTotalBits > 0 {
			d.MaxTotalBits = o.MaxTotalBits
		}
	}
	return d
}

// Decode parses one complete record from buf (only the first nbits bits
// are considered) and returns the record and the number of consumed bits.
// On any error the returned record is nil: a failed decode never delivers
// half a record. If the input is too short the error has Kind
// ErrNeedMoreInput.
func (p *Plan) Decode(buf []byte, nbits int64, opts *DecodeOptions) (map[string]any, int64, error) {
	d := newDecoder(p, buf, nbits, opts)
	rec, err := d.run()
	if err != nil {
		return nil, 0, err
	}
	return rec, d.r.pos, nil
}

// Session is an incremental decode session. It owns its input buffer,
// cursor and quota state; sessions are independent of each other and may
// be used concurrently as long as each Session is used by one goroutine.
// The shared Plan is safe for concurrent use by any number of Sessions.
type Session struct {
	plan  *Plan
	opts  DecodeOptions
	buf   []byte
	nbits int64 // valid bits in buf
}

// NewSession starts an incremental decode session.
func (p *Plan) NewSession(opts *DecodeOptions) *Session {
	return &Session{plan: p, opts: opts.withDefaults()}
}

// Feed appends data to the session's input. nbits is the number of valid
// bits in data (starting at bit 0 of data[0]); it may begin and end
// mid-byte, so chunks may be cut at any bit position.
func (s *Session) Feed(data []byte, nbits int64) {
	if nbits > int64(len(data))*8 {
		nbits = int64(len(data)) * 8
	}
	src := newBitReader(data, s.plan.BitOrder, nbits)
	for src.pos < nbits {
		want := nbits - src.pos
		bytePos := s.nbits >> 3
		if bytePos >= int64(len(s.buf)) {
			s.buf = append(s.buf, 0)
		}
		off := uint(s.nbits & 7)
		k := int64(8 - off)
		if k > want {
			k = want
		}
		chunk := src.group(int(k))
		if s.plan.BitOrder == MSBFirst {
			s.buf[bytePos] |= byte(chunk) << (8 - off - uint(k))
		} else {
			s.buf[bytePos] |= byte(chunk) << off
		}
		s.nbits += k
	}
}

// Try attempts to decode a complete record from everything fed so far.
// It returns (nil, false, nil) when more input is needed. On success it
// returns the record and true; the session is then consumed. On a hard
// error (reserved bits, illegal value, quota) it returns the error and
// the session is consumed. A failed Try never delivers a partial record.
func (s *Session) Try() (map[string]any, bool, error) {
	rec, _, err := s.plan.Decode(s.buf, s.nbits, &s.opts)
	if err != nil {
		if e, ok := err.(*Error); ok && e.Kind == ErrNeedMoreInput {
			return nil, false, nil
		}
		return nil, false, err
	}
	return rec, true, nil
}

// decoder holds the per-run state: cursor, quotas, decoded values.
type decoder struct {
	plan  *Plan
	r     *bitReader
	opts  DecodeOptions
	elems uint64 // total elements decoded so far
}

func newDecoder(p *Plan, buf []byte, nbits int64, opts *DecodeOptions) *decoder {
	o := opts.withDefaults()
	return &decoder{plan: p, r: newBitReader(buf, p.BitOrder, nbits), opts: o}
}

func (d *decoder) run() (map[string]any, error) {
	rec, err := d.readScope(d.plan.root, nil, 0)
	if err != nil {
		return nil, err
	}
	if d.r.pos > d.opts.MaxTotalBits {
		return nil, errf(ErrQuota, "/", d.r.pos, "record of %d bits exceeds quota %d", d.r.pos, d.opts.MaxTotalBits)
	}
	return rec, nil
}

func (d *decoder) readScope(n *node, topStarts []int64, depth int) (map[string]any, error) {
	if depth > d.opts.MaxDepth {
		return nil, errf(ErrQuota, n.path, d.r.pos, "nesting depth %d exceeds quota %d", depth, d.opts.MaxDepth)
	}
	if n == d.plan.root {
		topStarts = make([]int64, len(n.fields))
	}
	rec := map[string]any{}
	for i, f := range n.fields {
		if n == d.plan.root {
			topStarts[i] = d.r.pos
		}
		if err := d.readNode(f, rec, topStarts, depth); err != nil {
			return nil, err
		}
	}
	return rec, nil
}

func (d *decoder) need(path string, n int64) error {
	if d.r.remaining() < n {
		return errf(ErrNeedMoreInput, path, d.r.pos,
			"need %d bits, only %d left", n, d.r.remaining())
	}
	return nil
}

func (d *decoder) readNode(f *node, rec map[string]any, topStarts []int64, depth int) error {
	switch f.kind {
	case kindInt:
		if err := d.need(f.path, int64(f.bits)); err != nil {
			return err
		}
		rec[f.name] = d.readInt(f)
	case kindStruct:
		sub, err := d.readScope(f, topStarts, depth+1)
		if err != nil {
			return err
		}
		rec[f.name] = sub
	case kindArray:
		length, err := toUint(rec[f.lengthFrom.name])
		if err != nil {
			return errf(ErrValue, f.path, d.r.pos, "bad length field: %v", err)
		}
		sum, carry := bits.Add64(d.elems, length, 0)
		if carry != 0 {
			return errf(ErrOverflow, f.path, d.r.pos, "element count overflows 64 bits")
		}
		if sum > d.opts.MaxTotalElements {
			return errf(ErrQuota, f.path, d.r.pos,
				"total elements %d would exceed quota %d", sum, d.opts.MaxTotalElements)
		}
		d.elems = sum
		arr := make([]any, 0, min(length, uint64(d.opts.MaxTotalElements)))
		for i := uint64(0); i < length; i++ {
			el, err := d.readValue(f.elem, topStarts, depth+1)
			if err != nil {
				if e, ok := err.(*Error); ok {
					e.Path = elemPath(f.path, i, e.Path)
				}
				return err
			}
			arr = append(arr, el)
		}
		rec[f.name] = arr
	case kindUnion:
		tag, err := toUint(rec[f.tagFrom.name])
		if err != nil {
			return errf(ErrValue, f.path, d.r.pos, "bad tag field: %v", err)
		}
		cn, ok := f.cases[tag]
		if !ok {
			return errf(ErrValue, f.path, d.r.pos, "undefined tag value %d", tag)
		}
		sub, err := d.readScope(cn, topStarts, depth+1)
		if err != nil {
			return err
		}
		rec[f.name] = sub
	case kindPad:
		pad := padBits(d.r.pos, f.align)
		if err := d.need(f.path, int64(pad)); err != nil {
			return err
		}
		start := d.r.pos
		for i := 0; i < pad; i++ {
			if d.r.group(1) != 0 {
				return errf(ErrReservedBits, f.path, start+int64(i),
					"reserved padding bit must be zero")
			}
		}
	case kindChecksum:
		if err := d.need(f.path, int64(f.bits)); err != nil {
			return err
		}
		start := topStarts[f.fromIx]
		end := d.r.pos
		want := checksum(f.algo, checksumBytes(d.r.buf, d.plan.BitOrder, start, end), f.bits)
		got := d.r.field(f.bits, d.plan.ByteOrder)
		if got != want {
			return errf(ErrValue, f.path, end,
				"checksum mismatch: computed %#x, read %#x", want, got)
		}
		rec[f.name] = got
	}
	return nil
}

func (d *decoder) readValue(n *node, topStarts []int64, depth int) (any, error) {
	switch n.kind {
	case kindInt:
		if err := d.need(n.path, int64(n.bits)); err != nil {
			return nil, err
		}
		return d.readInt(n), nil
	case kindStruct:
		return d.readScope(n, topStarts, depth+1)
	}
	return nil, errf(ErrValue, n.path, d.r.pos, "unsupported element kind")
}

func (d *decoder) readInt(f *node) any {
	uv := d.r.field(f.bits, d.plan.ByteOrder)
	if !f.signed {
		return uv
	}
	if f.bits == 64 {
		return int64(uv)
	}
	if uv&(uint64(1)<<uint(f.bits-1)) != 0 {
		return int64(uv) - (int64(1) << uint(f.bits))
	}
	return int64(uv)
}

func elemPath(arrayPath string, i uint64, sub string) string {
	p := arrayPath + "[" + itoa(i) + "]"
	if sub != "" && sub != arrayPath {
		return p + sub[len(arrayPath):]
	}
	return p
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
