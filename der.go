package netsec

import (
	"bytes"
	"encoding/asn1"
	"fmt"
	"math/big"
	"time"
)

const (
	derClassUniversal = 0
	derClassContext   = 2

	derTagBoolean         = 1
	derTagInteger         = 2
	derTagBitString       = 3
	derTagOctetString     = 4
	derTagNull            = 5
	derTagOID             = 6
	derTagEnumerated      = 10
	derTagUTF8String      = 12
	derTagSequence        = 16
	derTagSet             = 17
	derTagPrintableString = 19
	derTagT61String       = 20
	derTagIA5String       = 22
	derTagGeneralizedTime = 24
)

// derNode is a single DER TLV. full contains the tag, length, and value so
// that a value can be verified in exactly the form in which it was signed.
type derNode struct {
	class       int
	tag         int
	constructed bool
	content     []byte
	full        []byte
}

func parseDER(data []byte) (derNode, error) {
	node, rest, err := readDER(data, 0)
	if err != nil {
		return derNode{}, err
	}
	if len(rest) != 0 {
		return derNode{}, fmt.Errorf("%w: trailing data", ErrMalformed)
	}
	return node, nil
}

func readDER(data []byte, depth int) (derNode, []byte, error) {
	if depth > 64 {
		return derNode{}, nil, fmt.Errorf("%w: nesting too deep", ErrMalformed)
	}
	if len(data) < 2 {
		return derNode{}, nil, fmt.Errorf("%w: truncated DER value", ErrMalformed)
	}

	class := int(data[0] >> 6)
	constructed := data[0]&0x20 != 0
	tag := int(data[0] & 0x1f)
	if tag == 0x1f {
		return derNode{}, nil, fmt.Errorf("%w: high-tag-number form is not supported", ErrMalformed)
	}

	offset := 2
	lengthByte := data[1]
	var length int
	if lengthByte&0x80 == 0 {
		length = int(lengthByte)
	} else {
		n := int(lengthByte & 0x7f)
		if n == 0 {
			return derNode{}, nil, fmt.Errorf("%w: indefinite length is not DER", ErrMalformed)
		}
		if n > 8 || len(data) < 2+n {
			return derNode{}, nil, fmt.Errorf("%w: invalid DER length", ErrMalformed)
		}
		if data[2] == 0 {
			return derNode{}, nil, fmt.Errorf("%w: non-minimal DER length", ErrMalformed)
		}
		offset += n
		var v uint64
		for _, b := range data[2 : 2+n] {
			v = v<<8 | uint64(b)
		}
		if v < 128 {
			return derNode{}, nil, fmt.Errorf("%w: non-minimal DER length", ErrMalformed)
		}
		maxInt := uint64(^uint(0) >> 1)
		if v > maxInt {
			return derNode{}, nil, fmt.Errorf("%w: DER value is too large", ErrMalformed)
		}
		length = int(v)
	}
	if length < 0 || len(data)-offset < length {
		return derNode{}, nil, fmt.Errorf("%w: truncated DER value", ErrMalformed)
	}

	end := offset + length
	return derNode{
		class:       class,
		tag:         tag,
		constructed: constructed,
		content:     data[offset:end],
		full:        data[:end],
	}, data[end:], nil
}

func (n derNode) isUniversal(tag int) bool {
	return n.class == derClassUniversal && n.tag == tag
}

func (n derNode) requireUniversal(tag int, what string) error {
	if !n.isUniversal(tag) {
		return fmt.Errorf("%w: %s has the wrong ASN.1 tag", ErrMalformed, what)
	}
	return nil
}

func (n derNode) children() ([]derNode, error) {
	if !n.constructed {
		return nil, fmt.Errorf("%w: primitive value cannot contain children", ErrMalformed)
	}
	var out []derNode
	rest := n.content
	for len(rest) > 0 {
		child, next, err := readDER(rest, 1)
		if err != nil {
			return nil, err
		}
		out = append(out, child)
		rest = next
	}
	return out, nil
}

func sequenceChildren(n derNode, what string) ([]derNode, error) {
	if n.isUniversal(derTagSequence) {
		return n.children()
	}
	// Context-specific tags in attribute certificates may be encoded
	// explicitly or implicitly. An implicit SEQUENCE keeps the constructed
	// bit and its content is the sequence body.
	if n.constructed {
		return n.children()
	}
	return nil, fmt.Errorf("%w: %s is not a sequence", ErrMalformed, what)
}

// taggedSequence unwraps an explicitly tagged SEQUENCE. If the tag is
// implicit, it returns a synthetic SEQUENCE over the tagged content.
func taggedSequence(n derNode, what string) (derNode, error) {
	if n.isUniversal(derTagSequence) {
		return n, nil
	}
	if !n.constructed {
		return derNode{}, fmt.Errorf("%w: %s is not constructed", ErrMalformed, what)
	}
	first, rest, err := readDER(n.content, 1)
	if err == nil && len(rest) == 0 && first.isUniversal(derTagSequence) {
		return first, nil
	}
	return derNode{
		class:       derClassUniversal,
		tag:         derTagSequence,
		constructed: true,
		content:     n.content,
		full:        marshalSequence(n.content),
	}, nil
}

func marshalSequence(content []byte) []byte {
	return append(derLength(derTagSequence, len(content)), content...)
}

func derLength(tag byte, length int) []byte {
	out := []byte{tag}
	if length < 128 {
		return append(out, byte(length))
	}
	var n int
	for v := length; v > 0; v >>= 8 {
		n++
	}
	out = append(out, 0x80|byte(n))
	for i := n - 1; i >= 0; i-- {
		out = append(out, byte(length>>(8*i)))
	}
	return out
}

func parseOID(n derNode) (asn1.ObjectIdentifier, error) {
	if err := n.requireUniversal(derTagOID, "object identifier"); err != nil {
		return nil, err
	}
	var oid asn1.ObjectIdentifier
	rest, err := asn1.Unmarshal(n.full, &oid)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid object identifier: %v", ErrMalformed, err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: trailing object identifier data", ErrMalformed)
	}
	if len(oid) < 2 {
		return nil, fmt.Errorf("%w: invalid object identifier", ErrMalformed)
	}
	return oid, nil
}

func parseBigInt(n derNode, what string) (*big.Int, error) {
	if err := n.requireUniversal(derTagInteger, what); err != nil {
		return nil, err
	}
	var v *big.Int
	rest, err := asn1.Unmarshal(n.full, &v)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid %s: %v", ErrMalformed, what, err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: trailing %s data", ErrMalformed, what)
	}
	if v == nil || v.Sign() < 0 {
		return nil, fmt.Errorf("%w: %s must not be negative", ErrMalformed, what)
	}
	return v, nil
}

func parseBool(n derNode, what string) (bool, error) {
	if err := n.requireUniversal(derTagBoolean, what); err != nil {
		return false, err
	}
	var v bool
	rest, err := asn1.Unmarshal(n.full, &v)
	if err != nil {
		return false, fmt.Errorf("%w: invalid %s: %v", ErrMalformed, what, err)
	}
	if len(rest) != 0 {
		return false, fmt.Errorf("%w: trailing %s data", ErrMalformed, what)
	}
	return v, nil
}

func parseGeneralizedTime(n derNode, what string) (time.Time, error) {
	if err := n.requireUniversal(derTagGeneralizedTime, what); err != nil {
		return time.Time{}, err
	}
	var v time.Time
	rest, err := asn1.Unmarshal(n.full, &v)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: invalid %s: %v", ErrMalformed, what, err)
	}
	if len(rest) != 0 {
		return time.Time{}, fmt.Errorf("%w: trailing %s data", ErrMalformed, what)
	}
	return v, nil
}

func parseBitString(n derNode, what string) ([]byte, error) {
	if err := n.requireUniversal(derTagBitString, what); err != nil {
		return nil, err
	}
	var v asn1.BitString
	rest, err := asn1.Unmarshal(n.full, &v)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid %s: %v", ErrMalformed, what, err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: trailing %s data", ErrMalformed, what)
	}
	if v.BitLength%8 != 0 {
		return nil, fmt.Errorf("%w: %s has unused bits", ErrMalformed, what)
	}
	if len(v.Bytes) != v.BitLength/8 {
		return nil, fmt.Errorf("%w: invalid %s length", ErrMalformed, what)
	}
	return v.Bytes, nil
}

func parseOctetString(n derNode, what string) ([]byte, error) {
	if err := n.requireUniversal(derTagOctetString, what); err != nil {
		return nil, err
	}
	return n.content, nil
}

// primitiveContent handles both explicit and implicit tagging for primitive
// values. X.509 implementations in the wild use both forms.
func primitiveContent(n derNode, expectedTag int) []byte {
	if child, rest, err := readDER(n.content, 1); err == nil &&
		len(rest) == 0 && child.isUniversal(expectedTag) {
		return child.content
	}
	return n.content
}

func oidFromImplicit(n derNode) (asn1.ObjectIdentifier, error) {
	if n.class != derClassContext {
		return nil, fmt.Errorf("%w: expected a context-specific tag", ErrMalformed)
	}
	if child, rest, err := readDER(n.content, 1); err == nil &&
		len(rest) == 0 && child.isUniversal(derTagOID) {
		return parseOID(child)
	}
	raw := append(derLength(derTagOID, len(n.content)), n.content...)
	return parseOID(derNode{
		class:   derClassUniversal,
		tag:     derTagOID,
		content: n.content,
		full:    raw,
	})
}

func bytesEqual(a, b []byte) bool {
	return bytes.Equal(a, b)
}
