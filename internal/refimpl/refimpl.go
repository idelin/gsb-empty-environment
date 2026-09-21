// Package refimpl is an independent, deliberately naive bit-by-bit
// implementation of the bitlayout semantics. It exists only to validate
// the production plan in tests: it parses the schema JSON on its own and
// processes one bit at a time, so shared bugs are unlikely.
package refimpl

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// Kind classifies reference errors for comparison with production.
type Kind string

const (
	NeedMore Kind = "need-more-input"
	Reserved Kind = "reserved-bits"
	Value    Kind = "illegal-value"
)

// Error is the reference error type.
type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string { return string(e.Kind) + ": " + e.Msg }

type schema struct {
	BitOrder  string  `json:"bitOrder"`
	ByteOrder string  `json:"byteOrder"`
	Fields    []field `json:"fields"`
}

type field struct {
	Kind       string  `json:"kind"`
	Name       string  `json:"name"`
	Bits       int     `json:"bits"`
	Signed     bool    `json:"signed"`
	Fields     []field `json:"fields"`
	LengthFrom string  `json:"lengthFrom"`
	Element    *field  `json:"element"`
	TagFrom    string  `json:"tagFrom"`
	Cases      map[string]struct {
		Fields []field `json:"fields"`
	} `json:"cases"`
	Align int    `json:"align"`
	Algo  string `json:"algo"`
	From  string `json:"from"`
	To    string `json:"to"`
}

func parse(schemaJSON []byte) (*schema, error) {
	var s schema
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		return nil, err
	}
	if s.BitOrder == "" {
		s.BitOrder = "msb"
	}
	if s.ByteOrder == "" {
		s.ByteOrder = "big"
	}
	return &s, nil
}

// ---- bit vector helpers (one bit at a time, no chunking) ----

// emitField appends the low n bits of v to out, honouring both orders.
func emitField(out []bool, v uint64, n int, bitOrder, byteOrder string) []bool {
	type group struct {
		val  uint64
		size int
	}
	var groups []group
	if byteOrder == "little" {
		for rem, shift := n, 0; rem > 0; {
			g := 8
			if rem < 8 {
				g = rem
			}
			groups = append(groups, group{(v >> uint(shift)) & maskN(g), g})
			shift += g
			rem -= g
		}
	} else {
		first := (n-1)%8 + 1
		groups = append(groups, group{(v >> uint(n-first)) & maskN(first), first})
		for rem := n - first; rem > 0; rem -= 8 {
			groups = append(groups, group{(v >> uint(rem-8)) & 0xff, 8})
		}
	}
	for _, g := range groups {
		if bitOrder == "lsb" {
			for j := 0; j < g.size; j++ {
				out = append(out, g.val>>uint(j)&1 == 1)
			}
		} else {
			for j := g.size - 1; j >= 0; j-- {
				out = append(out, g.val>>uint(j)&1 == 1)
			}
		}
	}
	return out
}

// readField consumes n bits from bits[pos:], honouring both orders.
func readField(bits []bool, pos int, n int, bitOrder, byteOrder string) (uint64, int) {
	readGroup := func(pos, g int) (uint64, int) {
		var v uint64
		if bitOrder == "lsb" {
			for j := 0; j < g; j++ {
				if bits[pos] {
					v |= 1 << uint(j)
				}
				pos++
			}
		} else {
			for j := 0; j < g; j++ {
				v <<= 1
				if bits[pos] {
					v |= 1
				}
				pos++
			}
		}
		return v, pos
	}
	if byteOrder == "little" {
		var v uint64
		for rem, shift := n, 0; rem > 0; {
			g := 8
			if rem < 8 {
				g = rem
			}
			var gv uint64
			gv, pos = readGroup(pos, g)
			v |= gv << uint(shift)
			shift += g
			rem -= g
		}
		return v, pos
	}
	first := (n-1)%8 + 1
	v, pos := readGroup(pos, first)
	for rem := n - first; rem > 0; rem -= 8 {
		var gv uint64
		gv, pos = readGroup(pos, 8)
		v = v<<8 | gv
	}
	return v, pos
}

func maskN(n int) uint64 {
	if n >= 64 {
		return ^uint64(0)
	}
	return uint64(1)<<uint(n) - 1
}

func padN(pos, align int) int {
	return (align - pos%align) % align
}

// ---- checksum (independent reimplementation) ----

func checksum(algo string, covered []bool, w int, bitOrder string) uint64 {
	// pack covered bits into bytes using the stream's own bit order
	packed := make([]byte, (len(covered)+7)/8)
	for i, b := range covered {
		if b {
			if bitOrder == "lsb" {
				packed[i/8] |= 1 << uint(i%8)
			} else {
				packed[i/8] |= 0x80 >> uint(i%8)
			}
		}
	}
	switch algo {
	case "sum":
		var s uint64
		for _, b := range packed {
			s += uint64(b)
		}
		return s & maskN(w)
	case "xor":
		var x uint64
		for _, b := range packed {
			x ^= uint64(b)
		}
		return x & maskN(w)
	case "crc8":
		var crc byte
		for _, b := range packed {
			crc ^= b
			for i := 0; i < 8; i++ {
				if crc&0x80 != 0 {
					crc = crc<<1 ^ 0x07
				} else {
					crc <<= 1
				}
			}
		}
		return uint64(crc)
	}
	return 0
}

// ---- encode ----

// Encode produces the reference bit vector for a record. Checksums are
// emitted as zeros first and patched in a fix-up pass once every covered
// bit is known.
func Encode(schemaJSON []byte, rec map[string]any) ([]bool, error) {
	s, err := parse(schemaJSON)
	if err != nil {
		return nil, err
	}
	e := &enc{s: s}
	if err := e.scope(s.Fields, rec, true); err != nil {
		return nil, err
	}
	for _, fx := range e.fixups {
		covered := e.out[e.topStarts[fx.fromIx]:fx.pos]
		sum := checksum(fx.algo, covered, fx.bits, e.s.BitOrder)
		bits := emitField(nil, sum, fx.bits, s.BitOrder, s.ByteOrder)
		copy(e.out[fx.pos:fx.pos+fx.bits], bits)
	}
	return e.out, nil
}

type fixup struct {
	pos    int
	fromIx int
	algo   string
	bits   int
}

type enc struct {
	s         *schema
	out       []bool
	topStarts []int
	fixups    []fixup
}

func (e *enc) scope(fields []field, rec map[string]any, isTop bool) error {
	if isTop {
		e.topStarts = make([]int, len(fields))
	}
	for i, f := range fields {
		if isTop {
			e.topStarts[i] = len(e.out)
		}
		if err := e.node(f, rec); err != nil {
			return err
		}
	}
	return nil
}

func (e *enc) node(f field, rec map[string]any) error {
	switch f.Kind {
	case "int":
		v, ok := rec[f.Name]
		if !ok {
			return &Error{Value, "missing " + f.Name}
		}
		var uv uint64
		if f.Signed {
			sv, ok := v.(int64)
			if !ok {
				return &Error{Value, "want int64"}
			}
			uv = uint64(sv) & maskN(f.Bits)
		} else {
			uv, ok = v.(uint64)
			if !ok {
				return &Error{Value, "want uint64"}
			}
		}
		e.out = emitField(e.out, uv, f.Bits, e.s.BitOrder, e.s.ByteOrder)
	case "struct":
		sub, _ := rec[f.Name].(map[string]any)
		return e.scope(f.Fields, sub, false)
	case "array":
		arr, _ := rec[f.Name].([]any)
		for _, el := range arr {
			if err := e.value(*f.Element, el); err != nil {
				return err
			}
		}
	case "union":
		tag, _ := rec[f.TagFrom].(uint64)
		cd, ok := f.Cases[strconv.FormatUint(tag, 10)]
		if !ok {
			return &Error{Value, "undefined tag"}
		}
		sub, _ := rec[f.Name].(map[string]any)
		return e.scope(cd.Fields, sub, false)
	case "pad":
		e.out = append(e.out, make([]bool, padN(len(e.out), f.Align))...)
	case "checksum":
		fromIx := -1
		for i, tf := range e.s.Fields {
			if tf.Name == f.From && fromIx < 0 {
				fromIx = i
			}
		}
		e.fixups = append(e.fixups, fixup{pos: len(e.out), fromIx: fromIx, algo: f.Algo, bits: f.Bits})
		e.out = append(e.out, make([]bool, f.Bits)...)
		return nil
	}
	return nil
}

func (e *enc) value(f field, v any) error {
	switch f.Kind {
	case "int":
		return e.node(field{Kind: "int", Name: "", Bits: f.Bits, Signed: f.Signed},
			map[string]any{"": v})
	case "struct":
		sub, _ := v.(map[string]any)
		return e.scope(f.Fields, sub, false)
	}
	return &Error{Value, "bad element"}
}

// ---- decode ----

// Decode parses bits according to the schema and returns the record and
// the number of consumed bits.
func Decode(schemaJSON []byte, bits []bool) (map[string]any, int, error) {
	s, err := parse(schemaJSON)
	if err != nil {
		return nil, 0, err
	}
	d := &dec{s: s, bits: bits}
	rec, err := d.scope(s.Fields, nil, true)
	if err != nil {
		return nil, 0, err
	}
	return rec, d.pos, nil
}

type dec struct {
	s    *schema
	bits []bool
	pos  int
}

func (d *dec) scope(fields []field, top []int, isTop bool) (map[string]any, error) {
	if isTop {
		top = make([]int, len(fields))
	}
	rec := map[string]any{}
	for i, f := range fields {
		if isTop {
			top[i] = d.pos
		}
		if err := d.node(f, rec, top); err != nil {
			return nil, err
		}
	}
	return rec, nil
}

func (d *dec) need(n int) error {
	if len(d.bits)-d.pos < n {
		return &Error{NeedMore, fmt.Sprintf("need %d bits at %d, have %d", n, d.pos, len(d.bits)-d.pos)}
	}
	return nil
}

func (d *dec) node(f field, rec map[string]any, top []int) error {
	switch f.Kind {
	case "int":
		if err := d.need(f.Bits); err != nil {
			return err
		}
		var uv uint64
		uv, d.pos = readField(d.bits, d.pos, f.Bits, d.s.BitOrder, d.s.ByteOrder)
		rec[f.Name] = signedOrNot(uv, f.Bits, f.Signed)
	case "struct":
		sub, err := d.scope(f.Fields, top, false)
		if err != nil {
			return err
		}
		rec[f.Name] = sub
	case "array":
		ln, _ := rec[f.LengthFrom].(uint64)
		arr := make([]any, 0, ln)
		for i := uint64(0); i < ln; i++ {
			el, err := d.value(*f.Element, top)
			if err != nil {
				return err
			}
			arr = append(arr, el)
		}
		rec[f.Name] = arr
	case "union":
		tag, _ := rec[f.TagFrom].(uint64)
		cd, ok := f.Cases[strconv.FormatUint(tag, 10)]
		if !ok {
			return &Error{Value, fmt.Sprintf("undefined tag %d", tag)}
		}
		sub, err := d.scope(cd.Fields, top, false)
		if err != nil {
			return err
		}
		rec[f.Name] = sub
	case "pad":
		n := padN(d.pos, f.Align)
		if err := d.need(n); err != nil {
			return err
		}
		for i := 0; i < n; i++ {
			if d.bits[d.pos] {
				return &Error{Reserved, fmt.Sprintf("pad bit %d set", d.pos)}
			}
			d.pos++
		}
	case "checksum":
		if err := d.need(f.Bits); err != nil {
			return err
		}
		fromIx := -1
		for i, tf := range d.s.Fields {
			if tf.Name == f.From && fromIx < 0 {
				fromIx = i
			}
		}
		covered := d.bits[top[fromIx]:d.pos]
		want := checksum(f.Algo, covered, f.Bits, d.s.BitOrder)
		var got uint64
		got, d.pos = readField(d.bits, d.pos, f.Bits, d.s.BitOrder, d.s.ByteOrder)
		if got != want {
			return &Error{Value, fmt.Sprintf("checksum want %d got %d", want, got)}
		}
		rec[f.Name] = got
	}
	return nil
}

func (d *dec) value(f field, top []int) (any, error) {
	switch f.Kind {
	case "int":
		if err := d.need(f.Bits); err != nil {
			return nil, err
		}
		var uv uint64
		uv, d.pos = readField(d.bits, d.pos, f.Bits, d.s.BitOrder, d.s.ByteOrder)
		return signedOrNot(uv, f.Bits, f.Signed), nil
	case "struct":
		return d.scope(f.Fields, top, false)
	}
	return nil, &Error{Value, "bad element"}
}

func signedOrNot(uv uint64, bits int, signed bool) any {
	if !signed {
		return uv
	}
	if bits == 64 {
		return int64(uv)
	}
	if uv&(1<<uint(bits-1)) != 0 {
		return int64(uv) - (int64(1) << uint(bits))
	}
	return int64(uv)
}
