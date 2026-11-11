package wireadmit

// parsePacked decodes every element of a packed payload. An empty payload is
// legal and yields no values.
func (p *parser) parsePacked(payload []byte, t Type, fieldNum int) ([]Value, error) {
	var out []Value
	off := 0
	switch t {
	case Varint:
		for off < len(payload) {
			start := off
			_, end, err := p.parseVarint(payload, off)
			if err != nil {
				return nil, p.repack(err, fieldNum)
			}
			out = append(out, Value{Wire: wireVarint, Raw: payload[start:end]})
			off = end
		}
	case Fixed64:
		if len(payload)%8 != 0 {
			return nil, p.fail(ErrPacked, off, fieldNum,
				"packed fixed64 payload length %d is not a multiple of 8", len(payload))
		}
		for off < len(payload) {
			out = append(out, Value{Wire: wireFixed64, Raw: payload[off : off+8]})
			off += 8
		}
	case Fixed32:
		if len(payload)%4 != 0 {
			return nil, p.fail(ErrPacked, off, fieldNum,
				"packed fixed32 payload length %d is not a multiple of 4", len(payload))
		}
		for off < len(payload) {
			out = append(out, Value{Wire: wireFixed32, Raw: payload[off : off+4]})
			off += 4
		}
	default:
		return nil, p.fail(ErrPacked, off, fieldNum, "type %d is not packable", t)
	}
	return out, nil
}

// repack re-attributes an error raised inside a packed payload to its field
// and normalises structural failures to ErrPacked so callers can distinguish
// malformed packed data from ordinary truncation.
func (p *parser) repack(err error, fieldNum int) error {
	var e *Error
	if asError(err, &e) {
		if e.Code == ErrPacked {
			e.Field = fieldNum
			return e
		}
		return &Error{Code: ErrPacked, Offset: e.Offset, Field: fieldNum, Msg: e.Msg}
	}
	return err
}

func asError(err error, target **Error) bool {
	for err != nil {
		if e, ok := err.(*Error); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// skipField advances over one unknown field using only its wire type. The
// field's bytes are bounds-checked but never decoded semantically.
func (p *parser) skipField(buf []byte, off, wire, depth, fieldNum int) (int, error) {
	switch wire {
	case wireVarint:
		_, end, err := p.parseVarint(buf, off)
		return end, err
	case wireFixed64:
		end := off + 8
		if end > len(buf) {
			return off, p.fail(ErrTruncated, off, fieldNum, "unknown fixed64 truncated")
		}
		return end, nil
	case wireBytes:
		_, end, err := p.readDelim(buf, off, fieldNum)
		return end, err
	case wireFixed32:
		end := off + 4
		if end > len(buf) {
			return off, p.fail(ErrTruncated, off, fieldNum, "unknown fixed32 truncated")
		}
		return end, nil
	case wireStartG:
		return p.skipGroup(buf, off, depth, fieldNum)
	default:
		return off, p.fail(ErrInvalidField, off, fieldNum, "unknown field uses wire type %d", wire)
	}
}

// skipGroup walks an unknown group to its matching end-group tag, counting
// nesting against the budget. Inner fields are skipped by wire rules only and
// are never matched against any schema.
func (p *parser) skipGroup(buf []byte, off, depth, fieldNum int) (int, error) {
	if depth+1 > p.cfg.maxDepth {
		return off, p.fail(ErrNesting, off, fieldNum,
			"unknown group nesting exceeds budget %d", p.cfg.maxDepth)
	}
	for off < len(buf) {
		tagStart := off
		tag, next, err := p.parseVarint(buf, off)
		if err != nil {
			return off, err
		}
		off = next
		wire := int(tag & 0x7)
		innerNum := int(tag >> 3)
		if innerNum <= 0 {
			return off, p.fail(ErrInvalidField, tagStart, innerNum, "field number must be positive")
		}
		switch wire {
		case wireEndG:
			if innerNum != fieldNum {
				return off, p.fail(ErrInvalidField, tagStart, innerNum,
					"end-group field %d does not match start-group field %d", innerNum, fieldNum)
			}
			return off, nil
		case wireStartG:
			end, err := p.skipGroup(buf, off, depth+1, innerNum)
			if err != nil {
				return off, err
			}
			off = end
		case wireVarint, wireFixed64, wireBytes, wireFixed32:
			end, err := p.skipField(buf, off, wire, depth+1, innerNum)
			if err != nil {
				return off, err
			}
			off = end
		default:
			return off, p.fail(ErrInvalidField, tagStart, innerNum, "reserved wire type %d", wire)
		}
	}
	return off, p.fail(ErrTruncated, off, fieldNum, "unknown group %d has no end-group tag", fieldNum)
}
