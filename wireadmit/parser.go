package wireadmit

import "fmt"

const (
	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
	wireStartG  = 3
	wireEndG    = 4
	wireFixed32 = 5

	// maxVarintBytes is the longest legal base-128 varint for a 64-bit
	// integer: nine continuation bytes plus a final byte carrying at most
	// one payload bit.
	maxVarintBytes = 10
)

type parser struct {
	cfg config
}

func newParser(cfg config) *parser {
	return &parser{cfg: cfg}
}

func (p *parser) fail(code ErrorCode, off int, field int, format string, args ...any) error {
	return &Error{
		Code:   code,
		Offset: off,
		Field:  field,
		Msg:    fmt.Sprintf(format, args...),
	}
}

// parseVarint reads a base-128 varint from buf[off:]. It returns the value and
// the offset immediately following it. A missing continuation byte yields
// ErrTruncated; an encoding longer than ten bytes or whose tenth byte holds
// more than one payload bit yields ErrVarintOverflow.
func (p *parser) parseVarint(buf []byte, off int) (uint64, int, error) {
	var value uint64
	for i := 0; i < maxVarintBytes; i++ {
		if off >= len(buf) {
			return 0, off, p.fail(ErrTruncated, off, 0, "varint truncated after %d byte(s)", i)
		}
		b := buf[off]
		off++
		if i == maxVarintBytes-1 {
			if b > 1 {
				return 0, off, p.fail(ErrVarintOverflow, off-1, 0,
					"tenth varint byte %#x exceeds one payload bit", b)
			}
			value |= uint64(b&0x7f) << (7 * i)
			return value, off, nil
		}
		value |= uint64(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			return value, off, nil
		}
	}
	// An eleventh continuation byte can never fit in 64 bits.
	if off >= len(buf) {
		return 0, off, p.fail(ErrTruncated, off, 0, "varint truncated after %d bytes", maxVarintBytes)
	}
	return 0, off, p.fail(ErrVarintOverflow, off, 0, "varint longer than %d bytes", maxVarintBytes)
}

// parseMessage validates buf as one message level at the given 1-based depth.
// endNumber, when non-zero, makes the level a group that must terminate with
// an end-group tag carrying exactly this field number.
func (p *parser) parseMessage(buf []byte, depth int, schema *Schema, endNumber int) (*Report, error) {
	if depth > p.cfg.maxDepth {
		return nil, p.fail(ErrNesting, 0, 0, "depth %d exceeds budget %d", depth, p.cfg.maxDepth)
	}
	rep := &Report{MaxDepth: depth}
	seen := make(map[int]bool)
	off := 0
	for off < len(buf) {
		tagStart := off
		tag, next, err := p.parseVarint(buf, off)
		if err != nil {
			return nil, err
		}
		off = next
		wire := int(tag & 0x7)
		fieldNum := int(tag >> 3)

		if wire == wireEndG {
			if endNumber == 0 {
				return nil, p.fail(ErrInvalidField, tagStart, fieldNum, "unexpected end-group tag")
			}
			if fieldNum != endNumber {
				return nil, p.fail(ErrInvalidField, tagStart, fieldNum,
					"end-group field %d does not match start-group field %d", fieldNum, endNumber)
			}
			if off != len(buf) {
				return nil, p.fail(ErrInvalidField, off, fieldNum, "bytes follow end-group tag")
			}
			return rep, nil
		}

		if fieldNum <= 0 {
			return nil, p.fail(ErrInvalidField, tagStart, fieldNum, "field number must be positive")
		}
		if fieldNum > maxFieldNumber {
			return nil, p.fail(ErrInvalidField, tagStart, fieldNum, "field number exceeds %d", maxFieldNumber)
		}
		if wire == 6 || wire == 7 {
			return nil, p.fail(ErrInvalidField, tagStart, fieldNum, "reserved wire type %d", wire)
		}

		rule := schema.rule(fieldNum)
		if rule == nil {
			// Unknown field: walk it using only wire rules, record its exact
			// span and never associate it with an authorised field.
			end, err := p.skipField(buf, off, wire, depth, fieldNum)
			if err != nil {
				return nil, err
			}
			rep.Unknown = append(rep.Unknown, Range{Start: tagStart, End: end})
			off = end
			continue
		}

		if rule.Type == Group {
			if wire != wireStartG {
				return nil, p.fail(ErrWireType, tagStart, fieldNum,
					"group field requires wire type 3, got %d", wire)
			}
		} else if wire == wireBytes {
			// Wire type 2 is legal for Bytes, Message and packed repeated
			// packable scalars; the legality of packing is decided below.
			if rule.Type != Bytes && rule.Type != Message &&
				!(rule.Cardinality == Repeated && packable(rule.Type)) {
				return nil, p.fail(ErrPacked, tagStart, fieldNum,
					"field %d cannot use length-delimited packed encoding", fieldNum)
			}
		} else {
			want, ok := expectedWireType(rule.Type)
			if !ok || wire != want {
				return nil, p.fail(ErrWireType, tagStart, fieldNum,
					"field type %d requires wire type %d, got %d", rule.Type, want, wire)
			}
		}

		if seen[fieldNum] && rule.Cardinality == Singular {
			return nil, p.fail(ErrDuplicateSingular, tagStart, fieldNum,
				"singular field %d occurs more than once", fieldNum)
		}
		seen[fieldNum] = true

		entry := Entry{Number: fieldNum, Type: rule.Type, Data: Range{Start: tagStart}}
		switch {
		case wire == wireVarint:
			_, end, err := p.parseVarint(buf, off)
			if err != nil {
				return nil, err
			}
			entry.Values = []Value{{Wire: wireVarint, Raw: buf[off:end]}}
			off = end
		case wire == wireFixed64:
			end := off + 8
			if end > len(buf) {
				return nil, p.fail(ErrTruncated, tagStart, fieldNum, "fixed64 payload truncated")
			}
			entry.Values = []Value{{Wire: wireFixed64, Raw: buf[off:end]}}
			off = end
		case wire == wireFixed32:
			end := off + 4
			if end > len(buf) {
				return nil, p.fail(ErrTruncated, tagStart, fieldNum, "fixed32 payload truncated")
			}
			entry.Values = []Value{{Wire: wireFixed32, Raw: buf[off:end]}}
			off = end
		case wire == wireStartG:
			sub, end, err := p.parseGroup(buf, off, depth, rule, fieldNum)
			if err != nil {
				return nil, err
			}
			entry.Values = []Value{{Wire: wireStartG, Raw: buf[tagStart:end], Sub: sub}}
			off = end
		case wire == wireBytes:
			payload, end, err := p.readDelim(buf, off, fieldNum)
			if err != nil {
				return nil, err
			}
			switch rule.Type {
			case Message:
				sub, err := p.parseMessage(payload, depth+1, rule.Nested, 0)
				if err != nil {
					return nil, err
				}
				entry.Values = []Value{{Wire: wireBytes, Raw: buf[tagStart:end], Sub: sub}}
			case Bytes:
				entry.Values = []Value{{Wire: wireBytes, Raw: payload}}
			default:
				vals, err := p.parsePacked(payload, rule.Type, fieldNum)
				if err != nil {
					return nil, err
				}
				entry.Packed = true
				entry.Values = vals
			}
			off = end
		default:
			return nil, p.fail(ErrInvalidField, tagStart, fieldNum, "unsupported wire type %d", wire)
		}
		entry.Data.End = off
		rep.Fields = append(rep.Fields, entry)
		if d := entryDepth(entry); d > rep.MaxDepth {
			rep.MaxDepth = d
		}
	}

	if endNumber != 0 {
		return nil, p.fail(ErrTruncated, off, endNumber, "group %d has no end-group tag", endNumber)
	}
	return rep, nil
}

func entryDepth(e Entry) int {
	if len(e.Values) > 0 && e.Values[0].Sub != nil {
		return e.Values[0].Sub.MaxDepth
	}
	return 0
}

// parseGroup parses a start-group body. off is the byte immediately after the
// start-group tag; it returns once the matching end-group tag is consumed.
func (p *parser) parseGroup(buf []byte, off, depth int, rule *Rule, fieldNum int) (*Report, int, error) {
	if depth+1 > p.cfg.maxDepth {
		return nil, off, p.fail(ErrNesting, off, fieldNum, "group nesting exceeds budget %d", p.cfg.maxDepth)
	}
	// The end-group branch requires the tag to land exactly at buffer end, so
	// passing the tail makes parseMessage consume only this group.
	body := buf[off:]
	rep, err := p.parseMessage(body, depth+1, rule.Nested, fieldNum)
	if err != nil {
		return nil, off, err
	}
	return rep, off + len(body), nil
}

// readDelim reads a length-prefixed payload starting at off, returning the
// payload slice (aliasing buf) and the offset just past the payload.
func (p *parser) readDelim(buf []byte, off, fieldNum int) ([]byte, int, error) {
	length, after, err := p.parseVarint(buf, off)
	if err != nil {
		return nil, off, err
	}
	end := after + int(length)
	if end < after || end > len(buf) {
		return nil, after, p.fail(ErrLengthOverflow, off, fieldNum,
			"declared length %d exceeds %d remaining bytes", length, len(buf)-after)
	}
	return buf[after:end], end, nil
}
