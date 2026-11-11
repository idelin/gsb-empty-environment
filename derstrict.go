// Package derstrict implements a strict DER (ITU-T X.690) decoder.
//
// Unlike encoding/asn1, which accepts several BER-tolerant encodings, this
// package rejects every non-canonical encoding it encounters and reports the
// byte offset of the first violation. This guarantees that a given abstract
// value has exactly one accepted byte representation, which keeps signature
// caches keyed on the raw certificate bytes effective.
package derstrict

import (
	"bytes"
	"errors"
	"math/big"
)

// Class is the ASN.1 tag class carried in the identifier octet.
type Class uint8

const (
	ClassUniversal       Class = 0
	ClassApplication     Class = 1
	ClassContextSpecific Class = 2
	ClassPrivate         Class = 3
)

// AnyClass may be used in a Pattern to accept any tag class.
const AnyClass Class = 0xff

// Universal tag numbers defined by X.690.
const (
	TagBoolean     = 1
	TagInteger     = 2
	TagBitString   = 3
	TagOctetString = 4
	TagNull        = 5
	TagOID         = 6
	TagEnum        = 10
	TagUTF8String  = 12
	TagSequence    = 16
	TagSet         = 17
	TagPrintable   = 19
	TagIA5String   = 22
	TagUTCTime     = 23
	TagGeneralTime = 24
)

// AnyTag may be used in a Pattern to accept any tag number.
const AnyTag = -1

const maxInt = int(^uint(0) >> 1)

// Node is one decoded TLV element.
type Node struct {
	// Class is the tag class.
	Class Class
	// Tag is the tag number (already decoded from high-tag-number form).
	Tag int
	// Constructed reports whether the constructed bit is set.
	Constructed bool
	// Offset is the absolute byte offset of the identifier octet in the
	// original input.
	Offset int
	// HeaderLen is the number of identifier and length octets.
	HeaderLen int
	// Length is the decoded content length.
	Length int
	// Content is the raw content (V) octets.
	Content []byte
	// Full is the complete TLV encoding, as a sub-slice of the original input.
	Full []byte
	// Children holds the nested elements of a constructed value.
	Children []*Node
}

// Error reports a DER violation together with the offset of the first
// offending byte in the original input.
type Error struct {
	Offset int
	Op     string
	Msg    string
}

func (e *Error) Error() string {
	return "derstrict: " + e.Op + " at offset " + itoa(e.Offset) + ": " + e.Msg
}

// OffsetOf returns the violation offset carried by a *Error, or -1 if the
// error did not originate from this package.
func OffsetOf(err error) int {
	var strictErr *Error
	if errors.As(err, &strictErr) {
		return strictErr.Offset
	}
	return -1
}

// Decode parses exactly one complete DER element. Trailing bytes after the
// element are rejected.
func Decode(data []byte) (*Node, error) {
	node, rest, err := parseElement(data, 0)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, &Error{
			Offset: len(data) - len(rest),
			Op:     "decode",
			Msg:    "trailing data after DER element",
		}
	}
	return node, nil
}

// parseElement parses one TLV at the start of data. base is the absolute
// offset of data[0] within the original input. It returns the parsed node and
// the unconsumed remainder.
func parseElement(data []byte, base int) (*Node, []byte, error) {
	if len(data) == 0 {
		return nil, nil, &Error{Offset: base, Op: "decode", Msg: "expected element, got empty data"}
	}

	identifier := data[0]
	class := Class(identifier >> 6)
	constructed := identifier&0x20 != 0
	tag := int(identifier & 0x1f)
	pos := 1

	if tag == 0x1f {
		var err error
		tag, pos, err = parseHighTagNumber(data, pos, base)
		if err != nil {
			return nil, nil, err
		}
	}

	if class == ClassUniversal && tag == 0 {
		return nil, nil, &Error{
			Offset: base,
			Op:     "decode",
			Msg:    "end-of-contents octet is not valid in DER",
		}
	}

	if pos >= len(data) {
		return nil, nil, &Error{Offset: base + pos, Op: "length", Msg: "missing length octet"}
	}
	lengthStart := pos
	firstLengthOctet := data[pos]
	pos++

	var length int
	switch {
	case firstLengthOctet < 0x80:
		length = int(firstLengthOctet)
	case firstLengthOctet == 0x80:
		return nil, nil, &Error{
			Offset: base + lengthStart,
			Op:     "length",
			Msg:    "indefinite length is BER-only and not valid DER",
		}
	default:
		numBytes := int(firstLengthOctet & 0x7f)
		if numBytes == 0x7f {
			return nil, nil, &Error{
				Offset: base + lengthStart,
				Op:     "length",
				Msg:    "reserved length value 0x7f",
			}
		}
		if pos+numBytes > len(data) {
			return nil, nil, &Error{
				Offset: base + lengthStart,
				Op:     "length",
				Msg:    "truncated multi-octet length",
			}
		}
		// DER requires the minimum number of length octets: a leading zero
		// octet is never needed.
		if data[pos] == 0x00 {
			return nil, nil, &Error{
				Offset: base + pos,
				Op:     "length",
				Msg:    "non-minimal length: redundant leading zero octet",
			}
		}
		for i := 0; i < numBytes; i++ {
			if length > maxInt>>8 {
				return nil, nil, &Error{
					Offset: base + lengthStart,
					Op:     "length",
					Msg:    "length exceeds addressable input",
				}
			}
			length = length<<8 | int(data[pos+i])
		}
		pos += numBytes
		// Values 0..127 must use the short form.
		if length <= 0x7f {
			return nil, nil, &Error{
				Offset: base + lengthStart,
				Op:     "length",
				Msg:    "non-minimal length: long form used for value <= 127",
			}
		}
	}

	contentStart := pos
	contentEnd := pos + length
	if contentEnd > len(data) {
		return nil, nil, &Error{
			Offset: base + lengthStart,
			Op:     "length",
			Msg:    "declared length exceeds available data",
		}
	}

	node := &Node{
		Class:       class,
		Tag:         tag,
		Constructed: constructed,
		Offset:      base,
		HeaderLen:   contentStart,
		Length:      length,
		Content:     data[contentStart:contentEnd],
		Full:        data[0:contentEnd],
	}

	if class == ClassUniversal {
		if err := validateUniversalContent(node, data, base, contentStart, contentEnd); err != nil {
			return nil, nil, err
		}
	}

	if constructed {
		children, err := parseChildren(data[contentStart:contentEnd], base+contentStart)
		if err != nil {
			return nil, nil, err
		}
		node.Children = children
	}

	if class == ClassUniversal && tag == TagSet {
		if err := checkSetOrdering(node); err != nil {
			return nil, nil, err
		}
	}

	return node, data[contentEnd:], nil
}

// parseHighTagNumber decodes the base-128 tag number following an 0x1f
// identifier octet and enforces the minimum-octet rule.
func parseHighTagNumber(data []byte, pos, base int) (int, int, error) {
	tag := 0
	first := true
	for {
		if pos >= len(data) {
			return 0, pos, &Error{
				Offset: base + pos,
				Op:     "tag",
				Msg:    "truncated high-tag-number encoding",
			}
		}
		octet := data[pos]
		if first && octet == 0x80 {
			return 0, pos, &Error{
				Offset: base + pos,
				Op:     "tag",
				Msg:    "non-minimal high-tag-number encoding",
			}
		}
		first = false
		if tag > maxInt>>7 {
			return 0, pos, &Error{
				Offset: base + pos,
				Op:     "tag",
				Msg:    "tag number too large",
			}
		}
		tag = tag<<7 | int(octet&0x7f)
		pos++
		if octet&0x80 == 0 {
			break
		}
	}
	if tag < 0x1f {
		return 0, pos, &Error{
			Offset: base,
			Op:     "tag",
			Msg:    "non-minimal tag: high-tag-number form used for tag < 31",
		}
	}
	return tag, pos, nil
}

// validateUniversalContent applies the DER content rules for universal tags.
func validateUniversalContent(node *Node, data []byte, base, contentStart, contentEnd int) error {
	contentOffset := base + contentStart
	switch node.Tag {
	case TagBoolean:
		if node.Length != 1 {
			return &Error{
				Offset: contentOffset,
				Op:     "boolean",
				Msg:    "BOOLEAN must have exactly one content octet",
			}
		}
		switch value := data[contentStart]; value {
		case 0x00, 0xff:
		default:
			return &Error{
				Offset: contentOffset,
				Op:     "boolean",
				Msg:    "non-canonical BOOLEAN: value must be 0x00 or 0xff",
			}
		}
	case TagInteger, TagEnum:
		if node.Length == 0 {
			return &Error{
				Offset: contentOffset,
				Op:     "integer",
				Msg:    "INTEGER must have at least one content octet",
			}
		}
		if node.Length >= 2 {
			first := data[contentStart]
			second := data[contentStart+1]
			redundantZero := first == 0x00 && second&0x80 == 0
			redundantFF := first == 0xff && second&0x80 != 0
			if redundantZero || redundantFF {
				return &Error{
					Offset: contentOffset,
					Op:     "integer",
					Msg:    "non-minimal INTEGER: redundant leading octet",
				}
			}
		}
	case TagNull:
		if node.Length != 0 {
			return &Error{
				Offset: contentOffset,
				Op:     "null",
				Msg:    "NULL must have zero content octets",
			}
		}
	case TagBitString:
		if node.Length == 0 {
			return &Error{
				Offset: contentOffset,
				Op:     "bitstring",
				Msg:    "BIT STRING must start with an unused-bits octet",
			}
		}
		unusedBits := data[contentStart]
		if unusedBits > 7 {
			return &Error{
				Offset: contentOffset,
				Op:     "bitstring",
				Msg:    "unused-bits count must be in range 0..7",
			}
		}
		if node.Length == 1 {
			if unusedBits != 0 {
				return &Error{
					Offset: contentOffset,
					Op:     "bitstring",
					Msg:    "empty BIT STRING must have zero unused bits",
				}
			}
			break
		}
		lastOctet := data[contentEnd-1]
		if lastOctet&((1<<unusedBits)-1) != 0 {
			return &Error{
				Offset: base + contentEnd - 1,
				Op:     "bitstring",
				Msg:    "unused bits of BIT STRING must be zero",
			}
		}
	case TagSequence, TagSet:
		if !node.Constructed {
			return &Error{
				Offset: node.Offset,
				Op:     "tag",
				Msg:    "universal SEQUENCE/SET must use constructed encoding",
			}
		}
	default:
		// DER requires primitive encoding for every other universal type.
		if node.Constructed {
			return &Error{
				Offset: node.Offset,
				Op:     "tag",
				Msg:    "universal primitive type encoded as constructed",
			}
		}
	}
	return nil
}

// parseChildren decodes successive TLV elements until the content is
// exhausted. Leftover bytes that do not form a complete element are reported
// by the recursive parseElement call.
func parseChildren(data []byte, base int) ([]*Node, error) {
	var children []*Node
	for len(data) > 0 {
		child, rest, err := parseElement(data, base)
		if err != nil {
			return nil, err
		}
		children = append(children, child)
		consumed := len(data) - len(rest)
		data = rest
		base += consumed
	}
	return children, nil
}

// checkSetOrdering enforces X.690 11.6: the full TLV encodings of SET OF
// members must appear in ascending lexicographic order.
func checkSetOrdering(node *Node) error {
	for i := 1; i < len(node.Children); i++ {
		prev := node.Children[i-1].Full
		cur := node.Children[i].Full
		if bytes.Compare(prev, cur) > 0 {
			return &Error{
				Offset: node.Children[i].Offset,
				Op:     "set",
				Msg:    "SET OF elements are not in ascending DER order",
			}
		}
	}
	return nil
}

// Bool decodes a canonical BOOLEAN node.
func (n *Node) Bool() (bool, error) {
	if n.Class != ClassUniversal || n.Tag != TagBoolean {
		return false, &Error{Offset: n.Offset, Op: "boolean", Msg: "node is not a BOOLEAN"}
	}
	return n.Content[0] == 0xff, nil
}

// Integer decodes an INTEGER node as a big.Int.
func (n *Node) Integer() (*big.Int, error) {
	if n.Class != ClassUniversal || n.Tag != TagInteger {
		return nil, &Error{Offset: n.Offset, Op: "integer", Msg: "node is not an INTEGER"}
	}
	if n.Content[0]&0x80 == 0 {
		return new(big.Int).SetBytes(n.Content), nil
	}
	// Negative integer: invert the two's-complement encoding to recover the
	// magnitude and negate the result.
	magnitude := make([]byte, len(n.Content))
	copy(magnitude, n.Content)
	carry := true
	for i := len(magnitude) - 1; i >= 0; i-- {
		value := ^magnitude[i]
		if carry {
			value++
			carry = value == 0
		}
		magnitude[i] = value
	}
	magnitudeInt := new(big.Int).SetBytes(magnitude)
	return magnitudeInt.Neg(magnitudeInt), nil
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	var digits [20]byte
	pos := len(digits)
	for value != 0 {
		digit := value % 10
		if digit < 0 {
			digit = -digit
		}
		pos--
		digits[pos] = byte('0' + digit)
		value /= 10
	}
	if negative {
		pos--
		digits[pos] = '-'
	}
	return string(digits[pos:])
}
