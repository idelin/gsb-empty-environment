package flowspec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
)

// Decode errors. Callers can use errors.Is to classify malformed NLRIs; a
// control-plane host should treat every decode error as fail-closed.
var (
	ErrTruncated          = errors.New("flowspec: truncated NLRI")
	ErrInvalidPrefix      = errors.New("flowspec: invalid prefix component")
	ErrUnknownComponent   = errors.New("flowspec: unknown/unsupported component type")
	ErrDuplicateComponent = errors.New("flowspec: duplicate component type")
	ErrComponentOrder     = errors.New("flowspec: components not in ascending type order")
	ErrIllegalOperator    = errors.New("flowspec: illegal operator encoding")
	ErrEmptyNLRI          = errors.New("flowspec: empty NLRI")
	ErrTrailingBytes      = errors.New("flowspec: trailing bytes after NLRI")
)

// Operator encoding failures all wrap ErrIllegalOperator so hosts can
// classify the whole malformed-operator family with errors.Is.
var (
	ErrReservedLength     = fmt.Errorf("%w: reserved length code 0b11", ErrIllegalOperator)
	ErrOperatorNoRelation = fmt.Errorf("%w: no match/greater/less bit", ErrIllegalOperator)
	ErrEndOfList          = fmt.Errorf("%w: missing end-of-list bit", ErrIllegalOperator)
)

// DecodeTerms parses a complete operator/value list (the bytes following a
// type 3..12 octet). The final term must carry E and the whole body must be
// consumed.
func DecodeTerms(body []byte) ([]Term, error) {
	terms, consumed, err := decodeTermsAt(body)
	if err != nil {
		return nil, err
	}
	if consumed != len(body) {
		return nil, fmt.Errorf("%w: %d trailing operator byte(s)", ErrTrailingBytes, len(body)-consumed)
	}
	return terms, nil
}

// decodeTermsAt parses E-terminated terms from the start of b and returns the
// number of bytes consumed through the terminating term.
func decodeTermsAt(b []byte) ([]Term, int, error) {
	terms := make([]Term, 0, 1)
	off := 0
	for {
		if off >= len(b) {
			return nil, off, ErrEndOfList
		}
		op := b[off]
		if err := validateOperator(op); err != nil {
			return nil, off, err
		}
		code := op & OpLenMask
		width := 1 << code // code 0->1, 1->2, 2->4 (code 3 rejected above)
		start := off
		off++
		if off+width > len(b) {
			return nil, off, fmt.Errorf("%w: operator value needs %d byte(s) at offset %d", ErrTruncated, width, start)
		}
		var value uint64
		switch width {
		case 1:
			value = uint64(b[off])
		case 2:
			value = uint64(binary.BigEndian.Uint16(b[off : off+2]))
		case 4:
			value = uint64(binary.BigEndian.Uint32(b[off : off+4]))
		}
		off += width

		terms = append(terms, Term{
			Raw:     op,
			Width:   width,
			Value:   value,
			End:     op&OpEndOfList != 0,
			And:     op&OpAnd != 0,
			Not:     op&OpNot != 0,
			Match:   op&OpMatch != 0,
			Greater: op&OpGreater != 0,
			Less:    op&OpLess != 0,
		})

		if op&OpEndOfList != 0 {
			return terms, off, nil
		}
	}
}

// validateOperator enforces the unambiguous wire-level validity rules.
func validateOperator(op byte) error {
	if op&OpLenMask == 3 {
		return fmt.Errorf("op=0x%02x: %w", op, ErrReservedLength)
	}
	if op&(OpMatch|OpGreater|OpLess) == 0 {
		return fmt.Errorf("op=0x%02x: %w", op, ErrOperatorNoRelation)
	}
	return nil
}

// decodePrefixAt parses a type 1/2 prefix component starting at the type octet.
func decodePrefixAt(b []byte, af AFI, t ComponentType) (Component, int, error) {
	bits := af.maxPrefixBits()
	if len(b) < 2 {
		return Component{}, 0, fmt.Errorf("%w: %s header", ErrTruncated, t)
	}
	plen := int(b[1])
	if plen > bits {
		return Component{}, 0, fmt.Errorf("%w: %s length /%d exceeds %d bits", ErrInvalidPrefix, t, plen, bits)
	}
	nbytes := (plen + 7) / 8
	if len(b) < 2+nbytes {
		return Component{}, 0, fmt.Errorf("%w: %s needs %d prefix byte(s)", ErrTruncated, t, nbytes)
	}
	raw := make([]byte, bits/8)
	copy(raw, b[2:2+nbytes])
	ip := net.IP(raw)
	if af != AFIPv6 {
		ip = ip.To4()
	}
	prefix := net.IPNet{IP: ip, Mask: net.CIDRMask(plen, bits)}
	return Component{Type: t, Prefix: prefix}, 2 + nbytes, nil
}

// decodeComponentAt parses one component at the start of b.
func decodeComponentAt(b []byte, af AFI) (Component, int, error) {
	if len(b) == 0 {
		return Component{}, 0, ErrTruncated
	}
	t := ComponentType(b[0])
	if t < TypeDestPrefix || t > TypeFragment {
		return Component{}, 0, fmt.Errorf("%w: type %d", ErrUnknownComponent, uint8(t))
	}
	if t.IsPrefix() {
		return decodePrefixAt(b, af, t)
	}
	terms, consumed, err := decodeTermsAt(b[1:])
	if err != nil {
		return Component{}, 0, err
	}
	return Component{Type: t, Terms: terms}, 1 + consumed, nil
}

// DecodeComponents decodes a bare FlowSpec component stream (the NLRI value
// after the optional two-octet length prefix). The entire buffer must form
// ordered, unique, valid components.
func DecodeComponents(b []byte, af AFI) ([]Component, error) {
	if len(b) == 0 {
		return nil, ErrEmptyNLRI
	}
	comps := make([]Component, 0, 4)
	off := 0
	lastType := ComponentType(0)
	seen := make(map[ComponentType]bool)
	for off < len(b) {
		c, n, err := decodeComponentAt(b[off:], af)
		if err != nil {
			return nil, fmt.Errorf("component at offset %d: %w", off, err)
		}
		c.Raw = append([]byte(nil), b[off:off+n]...)
		if seen[c.Type] {
			return nil, fmt.Errorf("component at offset %d: %w: type %d", off, ErrDuplicateComponent, c.Type)
		}
		if c.Type < lastType {
			return nil, fmt.Errorf("component at offset %d: %w: type %d follows %d", off, ErrComponentOrder, c.Type, lastType)
		}
		seen[c.Type] = true
		lastType = c.Type
		comps = append(comps, c)
		off += n
	}
	return comps, nil
}

// DecodeRule decodes a bare component stream into a Rule.
func DecodeRule(b []byte, af AFI) (*Rule, error) {
	comps, err := DecodeComponents(b, af)
	if err != nil {
		return nil, err
	}
	return &Rule{AF: af, Components: comps}, nil
}

// DecodeFramedNLRI decodes one length-prefixed FlowSpec NLRI
// ([uint16 component-length][components]) at the start of b and reports the
// total number of bytes consumed (2 + component-length). It does not require b
// to end at the frame so callers can walk a sequence.
func DecodeFramedNLRI(b []byte, af AFI) (*Rule, int, error) {
	if len(b) < 2 {
		return nil, 0, fmt.Errorf("%w: missing NLRI length", ErrTruncated)
	}
	n := int(binary.BigEndian.Uint16(b[:2]))
	if n == 0 {
		return nil, 2, ErrEmptyNLRI
	}
	if len(b) < 2+n {
		return nil, 2, fmt.Errorf("%w: NLRI length declares %d byte(s), %d available", ErrTruncated, n, len(b)-2)
	}
	rule, err := DecodeRule(b[2:2+n], af)
	if err != nil {
		return nil, 2 + n, err
	}
	return rule, 2 + n, nil
}

// DecodeNLRI decodes exactly one length-prefixed FlowSpec NLRI and rejects
// trailing bytes.
func DecodeNLRI(b []byte, af AFI) (*Rule, error) {
	rule, consumed, err := DecodeFramedNLRI(b, af)
	if err != nil {
		return nil, err
	}
	if consumed != len(b) {
		return nil, fmt.Errorf("%w: consumed %d of %d bytes", ErrTrailingBytes, consumed, len(b))
	}
	return rule, nil
}

// DecodeNLRISequence decodes a concatenated sequence of length-prefixed NLRIs.
func DecodeNLRISequence(b []byte, af AFI) ([]*Rule, error) {
	var rules []*Rule
	for len(b) > 0 {
		rule, n, err := DecodeFramedNLRI(b, af)
		if err != nil {
			return rules, err
		}
		rules = append(rules, rule)
		b = b[n:]
	}
	return rules, nil
}

// DecodeAuto accepts either a length-prefixed NLRI whose declared length covers
// the whole buffer or a bare component stream. It is intended for hosts feeding
// either MP_REACH NLRI bodies or pre-sliced component bytes.
func DecodeAuto(b []byte, af AFI) (*Rule, error) {
	if len(b) >= 2 {
		n := int(binary.BigEndian.Uint16(b[:2]))
		if n > 0 && n == len(b)-2 {
			if rule, err := DecodeNLRI(b, af); err == nil {
				return rule, nil
			}
		}
	}
	return DecodeRule(b, af)
}
