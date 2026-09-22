package bitlayout

import (
	"fmt"
	"math"
	"strings"
)

// Quotas bound the resources a single decode session may consume. The zero
// value selects DefaultQuotas.
type Quotas struct {
	MaxElements uint64 // total array elements decoded
	MaxBits     uint64 // total input bits consumed
	MaxDepth    int    // array nesting depth
}

// DefaultQuotas are the limits applied when a zero Quotas is given.
func DefaultQuotas() Quotas {
	return Quotas{MaxElements: 1 << 20, MaxBits: 1 << 32, MaxDepth: MaxSchemaDepth}
}

type scope struct {
	label string
	obj   map[string]any
	pos   map[string][2]uint64
}

type aframe struct {
	name        string
	parentDepth int
	start       uint64
	remaining   uint64
	index       uint64
	values      []any
	elemStart   int
	elemEnd     int
}

type uframe struct {
	name  string
	start uint64
}

// Session is an incremental decoder. Its cursor, buffers and quota
// accounting are private to the session; the shared Plan stays read-only.
type Session struct {
	plan   *Plan
	quotas Quotas

	buf   []byte
	nbits uint64

	pc      int
	pos     uint64
	scopes  []scope
	frames  []aframe
	uframes []uframe
	elems   uint64

	done   bool
	result map[string]any
	err    *Error
}

// NewSession creates a decoder session bound to the plan.
func (p *Plan) NewSession(q Quotas) *Session {
	if q == (Quotas{}) {
		q = DefaultQuotas()
	}
	root := scope{label: p.Name, obj: map[string]any{}, pos: map[string][2]uint64{}}
	return &Session{plan: p, quotas: q, scopes: []scope{root}}
}

// Feed appends whole bytes of input.
func (s *Session) Feed(p []byte) {
	s.buf = append(s.buf, p...)
	s.nbits += uint64(len(p)) * 8
}

// FeedBits appends input whose final byte may be partial. Feeding a partial
// byte is only allowed on a byte boundary, i.e. when the number of bits fed
// so far is a multiple of 8.
func (s *Session) FeedBits(p []byte, nbits int) error {
	if nbits < 0 || nbits > len(p)*8 {
		return fmt.Errorf("bitlayout: FeedBits: %d bits not covered by %d bytes", nbits, len(p))
	}
	if s.nbits%8 != 0 {
		return fmt.Errorf("bitlayout: FeedBits: previous feed ended mid-byte")
	}
	s.buf = append(s.buf, p[:(nbits+7)/8]...)
	s.nbits += uint64(nbits)
	return nil
}

// Run executes decode steps until the record is complete, input runs out
// (ErrNeedInput, recoverable) or a terminal error occurs.
func (s *Session) Run() error {
	if s.err != nil {
		return s.err
	}
	for !s.done {
		if err := s.step(); err != nil {
			if e, ok := err.(*Error); ok && e.Kind != ErrNeedInput {
				s.err = e
			}
			return err
		}
	}
	return nil
}

// Result returns the decoded record and the number of input bits consumed.
// ok is false until the whole record, including the trailing checksum, has
// been verified; a failed session never delivers a partial record.
func (s *Session) Result() (rec map[string]any, bitsUsed uint64, ok bool) {
	if !s.done {
		return nil, 0, false
	}
	return s.result, s.pos, true
}

func (s *Session) cur() *scope { return &s.scopes[len(s.scopes)-1] }

func (s *Session) errAt(kind ErrKind, leaf string, bit uint64, format string, args ...any) *Error {
	var b strings.Builder
	for i := range s.scopes {
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(s.scopes[i].label)
	}
	if leaf != "" {
		b.WriteByte('.')
		b.WriteString(leaf)
	}
	return &Error{Kind: kind, Path: b.String(), Bit: bit, Msg: fmt.Sprintf(format, args...)}
}

// checkRead verifies the bit quota and input availability for reading n
// bits at the current position.
func (s *Session) checkRead(leaf string, n uint64) *Error {
	if n > s.quotas.MaxBits || s.pos > s.quotas.MaxBits-n {
		return s.errAt(ErrQuota, leaf, s.pos, "bit budget %d exceeded", s.quotas.MaxBits)
	}
	if n > s.nbits || s.pos > s.nbits-n {
		return s.errAt(ErrNeedInput, leaf, s.pos, "need %d more bits", s.pos+n-s.nbits)
	}
	return nil
}

func (s *Session) step() error {
	o := &s.plan.ops[s.pc]
	cur := s.cur()
	switch o.kind {
	case opValue:
		if err := s.checkRead(o.name, uint64(o.bits)); err != nil {
			return err
		}
		r := bitReader{buf: s.buf, nbits: s.nbits, bo: s.plan.BitOrder}
		p := s.pos
		v, ok := readField(&r, &p, o.bits, s.plan.ByteOrder)
		if !ok {
			return s.errAt(ErrNeedInput, o.name, s.pos, "need more bits")
		}
		if len(o.enum) > 0 && !containsUint(o.enum, v) {
			return s.errAt(ErrIllegalValue, o.name, s.pos, "value %d not in enum", v)
		}
		if o.signed {
			cur.obj[o.name] = signExtend(v, o.bits)
		} else {
			cur.obj[o.name] = v
		}
		cur.pos[o.name] = [2]uint64{s.pos, p}
		s.pos = p
		s.pc++

	case opAlign:
		start := s.pos
		n := alignPad(s.pos, o.multiple)
		if err := s.checkRead("", uint64(n)); err != nil {
			return err
		}
		if n > 0 {
			r := bitReader{buf: s.buf, nbits: s.nbits, bo: s.plan.BitOrder}
			v, _ := r.get(s.pos, n)
			if v != 0 {
				return s.errAt(ErrReservedBits, "", s.pos, "non-zero reserved bits in %d-bit padding", n)
			}
			s.pos += uint64(n)
		}
		cur.pos[o.name] = [2]uint64{start, s.pos}
		s.pc++

	case opArray:
		cnt, _ := cur.obj[o.lengthFrom].(uint64)
		if len(s.frames)+1 > s.quotas.MaxDepth {
			return s.errAt(ErrQuota, o.name, s.pos, "nesting depth %d exceeds budget %d",
				len(s.frames)+1, s.quotas.MaxDepth)
		}
		if cnt > s.quotas.MaxElements-s.elems {
			return s.errAt(ErrQuota, o.name, s.pos, "element budget %d exceeded by count %d",
				s.quotas.MaxElements, cnt)
		}
		if o.elemMax > 0 && cnt > math.MaxUint64/o.elemMax {
			return s.errAt(ErrQuota, o.name, s.pos, "length multiplication overflow: %d x %d bits",
				cnt, o.elemMax)
		}
		s.elems += cnt
		if cnt == 0 {
			cur.obj[o.name] = []any{}
			cur.pos[o.name] = [2]uint64{s.pos, s.pos}
			s.pc = o.elemEnd
			return s.finishElements()
		}
		s.frames = append(s.frames, aframe{
			name: o.name, parentDepth: len(s.scopes) - 1, start: s.pos,
			remaining: cnt, values: make([]any, 0, minU64(cnt, 64)),
			elemStart: o.elemStart, elemEnd: o.elemEnd,
		})
		s.pushElemScope()
		s.pc = o.elemStart

	case opUnion:
		tag, _ := cur.obj[o.tagFrom].(uint64)
		se, ok := o.cases[tag]
		if !ok {
			bit := s.pos
			if pr, ok := cur.pos[o.tagFrom]; ok {
				bit = pr[0]
			}
			return s.errAt(ErrIllegalValue, o.name, bit, "undefined tag %d", tag)
		}
		s.uframes = append(s.uframes, uframe{name: o.name, start: s.pos})
		s.scopes = append(s.scopes, scope{
			label: o.name, obj: map[string]any{}, pos: map[string][2]uint64{},
		})
		s.pc = se[0]

	case opJump:
		uf := s.uframes[len(s.uframes)-1]
		s.uframes = s.uframes[:len(s.uframes)-1]
		body := s.scopes[len(s.scopes)-1]
		s.scopes = s.scopes[:len(s.scopes)-1]
		parent := s.cur()
		parent.obj[uf.name] = body.obj
		parent.pos[uf.name] = [2]uint64{uf.start, s.pos}
		s.pc = o.jumpTgt

	case opChecksum:
		if err := s.checkRead(o.name, uint64(o.bits)); err != nil {
			return err
		}
		r := bitReader{buf: s.buf, nbits: s.nbits, bo: s.plan.BitOrder}
		p := s.pos
		got, ok := readField(&r, &p, o.bits, s.plan.ByteOrder)
		if !ok {
			return s.errAt(ErrNeedInput, o.name, s.pos, "need more bits")
		}
		from, okFrom := cur.pos[o.from]
		to, okTo := cur.pos[o.to]
		if !okFrom || !okTo {
			return s.errAt(ErrIllegalValue, o.name, s.pos, "checksum range fields missing")
		}
		want := checksumSum(&r, from[0], to[1], o.bits)
		if got != want {
			return s.errAt(ErrIllegalValue, o.name, s.pos,
				"checksum mismatch: got 0x%x want 0x%x", got, want)
		}
		cur.obj[o.name] = got
		cur.pos[o.name] = [2]uint64{s.pos, p}
		s.pos = p
		s.pc++

	case opEnd:
		s.done = true
		s.result = s.scopes[0].obj
		return nil
	}
	return s.finishElements()
}

func (s *Session) pushElemScope() {
	top := &s.frames[len(s.frames)-1]
	s.scopes = append(s.scopes, scope{
		label: fmt.Sprintf("%s[%d]", top.name, top.index),
		obj:   map[string]any{},
		pos:   map[string][2]uint64{},
	})
}

// finishElements closes array elements whose end the program counter has
// reached, possibly cascading through several nesting levels.
func (s *Session) finishElements() error {
	for len(s.frames) > 0 {
		top := &s.frames[len(s.frames)-1]
		if s.pc != top.elemEnd {
			return nil
		}
		el := s.scopes[len(s.scopes)-1]
		s.scopes = s.scopes[:len(s.scopes)-1]
		top.values = append(top.values, el.obj)
		top.remaining--
		top.index++
		if top.remaining == 0 {
			parent := &s.scopes[top.parentDepth]
			parent.obj[top.name] = top.values
			parent.pos[top.name] = [2]uint64{top.start, s.pos}
			s.frames = s.frames[:len(s.frames)-1]
			continue
		}
		s.pushElemScope()
		s.pc = top.elemStart
	}
	return nil
}

func signExtend(v uint64, bits int) int64 {
	if bits >= 64 {
		return int64(v)
	}
	return int64(v<<uint(64-bits)) >> uint(64-bits)
}

func minU64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
