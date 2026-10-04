package imgcodec

import (
	"encoding/binary"
	"fmt"
)

// DecodeError pinpoints a malformed instruction in the input stream.
type DecodeError struct {
	Instr  int // index of the offending instruction (0-based)
	Offset int // byte offset where that instruction starts
	Reason string
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("imgcodec: instruction %d at byte %d: %s", e.Instr, e.Offset, e.Reason)
}

// Decode strictly decodes data into exactly Width*Height pixels.
//
// Every structural problem — truncation, zero lengths, unknown opcodes,
// references in the first row, references longer than MaxRefLen,
// references crossing a row boundary, pixel-count over- or underflow —
// is reported as a *DecodeError naming the responsible instruction;
// no partial or corrupted pixel buffer is ever returned.
func Decode(data []byte, p Params) ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	total := p.Width * p.Height
	out := make([]byte, 0, total)

	idx, off := 0, 0
	fail := func(format string, args ...any) error {
		return &DecodeError{Instr: idx, Offset: off, Reason: fmt.Sprintf(format, args...)}
	}

	for off < len(data) {
		if len(data)-off < 3 {
			return nil, fail("truncated instruction header")
		}
		op := Op(data[off])
		if op != OpLiteral && op != OpRepeat && op != OpRef {
			return nil, fail("unknown opcode 0x%02x", byte(op))
		}
		length := int(binary.LittleEndian.Uint16(data[off+1:]))
		if length == 0 {
			return nil, fail("zero-length %s instruction", op)
		}
		if op == OpRef {
			pos := len(out)
			switch {
			case length > p.MaxRefLen:
				return nil, fail("reference length %d exceeds MaxRefLen %d", length, p.MaxRefLen)
			case pos < p.Width:
				return nil, fail("reference in first row has no previous row")
			case pos%p.Width+length > p.Width:
				return nil, fail("reference of %d pixels at column %d crosses row of width %d", length, pos%p.Width, p.Width)
			}
		}
		if len(out)+length > total {
			return nil, fail("%s of %d pixels overruns image (%d of %d already decoded)", op, length, len(out), total)
		}
		switch op {
		case OpLiteral:
			if len(data)-off-3 < length {
				return nil, fail("truncated literal payload: need %d bytes, have %d", length, len(data)-off-3)
			}
			out = append(out, data[off+3:off+3+length]...)
			off += 3 + length
		case OpRepeat:
			if len(data)-off < 4 {
				return nil, fail("truncated repeat instruction")
			}
			val := data[off+3]
			for k := 0; k < length; k++ {
				out = append(out, val)
			}
			off += 4
		case OpRef:
			pos := len(out)
			base := pos - p.Width
			out = append(out, out[base:base+length]...)
			off += 3
		}
		idx++
	}

	if len(out) != total {
		return nil, &DecodeError{Instr: idx, Offset: off,
			Reason: fmt.Sprintf("image incomplete: decoded %d of %d pixels", len(out), total)}
	}
	return out, nil
}
