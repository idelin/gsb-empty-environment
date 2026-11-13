package netsec

import (
	"encoding/asn1"
	"fmt"
	"time"
)

const (
	derClassUniversal = 0
	derClassContext   = 2

	derTagBoolean         = 1
	derTagInteger         = 2
	derTagBitString       = 3
	derTagOctetString     = 4
	derTagOID             = 6
	derTagEnum            = 10
	derTagUTCTime         = 23
	derTagGeneralizedTime = 24
	derTagSequence        = 16
)

// derElement describes one definite-length DER element.
type derElement struct {
	class        int
	tag          int
	constructed  bool
	start        int
	contentStart int
	contentEnd   int
	end          int
}

func (e derElement) full(data []byte) []byte {
	return data[e.start:e.end]
}

func (e derElement) content(data []byte) []byte {
	return data[e.contentStart:e.contentEnd]
}

func (e derElement) is(class, tag int, constructed bool) bool {
	return e.class == class && e.tag == tag && e.constructed == constructed
}

func parseDERElement(data []byte, offset int) (derElement, error) {
	var elem derElement
	if offset < 0 || offset >= len(data) {
		return elem, fmt.Errorf("%w: missing DER identifier at offset %d", ErrInvalidResponse, offset)
	}

	elem.start = offset
	identifier := data[offset]
	elem.class = int(identifier >> 6)
	elem.constructed = identifier&0x20 != 0
	elem.tag = int(identifier & 0x1f)
	if elem.tag == 0x1f {
		return elem, fmt.Errorf("%w: high-tag-number DER identifier is unsupported", ErrInvalidResponse)
	}
	offset++

	if offset >= len(data) {
		return elem, fmt.Errorf("%w: missing DER length", ErrInvalidResponse)
	}
	firstLength := data[offset]
	offset++
	length := 0
	switch {
	case firstLength < 0x80:
		length = int(firstLength)
	case firstLength == 0x80:
		return elem, fmt.Errorf("%w: indefinite DER length is not allowed", ErrInvalidResponse)
	default:
		lengthBytes := int(firstLength & 0x7f)
		if lengthBytes > 8 || offset+lengthBytes > len(data) {
			return elem, fmt.Errorf("%w: invalid DER length", ErrInvalidResponse)
		}
		if data[offset] == 0 {
			return elem, fmt.Errorf("%w: non-minimal DER length", ErrInvalidResponse)
		}
		for i := 0; i < lengthBytes; i++ {
			if length > (int(^uint(0)>>1)-int(data[offset+i]))/256 {
				return elem, fmt.Errorf("%w: DER length overflows int", ErrInvalidResponse)
			}
			length = length*256 + int(data[offset+i])
		}
		offset += lengthBytes
	}

	elem.contentStart = offset
	elem.contentEnd = offset + length
	elem.end = elem.contentEnd
	if elem.contentEnd < elem.contentStart || elem.contentEnd > len(data) {
		return elem, fmt.Errorf("%w: DER element extends beyond input", ErrInvalidResponse)
	}
	return elem, nil
}

func parseSingleDERElement(data []byte) (derElement, error) {
	elem, err := parseDERElement(data, 0)
	if err != nil {
		return elem, err
	}
	if elem.end != len(data) {
		return elem, fmt.Errorf("%w: trailing data after DER element", ErrInvalidResponse)
	}
	return elem, nil
}

func derChildren(data []byte, parent derElement) ([]derElement, error) {
	if !parent.constructed {
		return nil, fmt.Errorf("%w: DER element is not constructed", ErrInvalidResponse)
	}
	var children []derElement
	for offset := parent.contentStart; offset < parent.contentEnd; {
		child, err := parseDERElement(data, offset)
		if err != nil {
			return nil, err
		}
		if child.end > parent.contentEnd {
			return nil, fmt.Errorf("%w: child DER element extends beyond parent", ErrInvalidResponse)
		}
		children = append(children, child)
		offset = child.end
	}
	return children, nil
}

func requireDERElement(data []byte, elem derElement, class, tag int, constructed bool, name string) error {
	if !elem.is(class, tag, constructed) {
		return fmt.Errorf("%w: %s has unexpected DER tag", ErrInvalidResponse, name)
	}
	return nil
}

func parseDERSequence(data []byte, elem derElement, name string) ([]derElement, error) {
	if err := requireDERElement(data, elem, derClassUniversal, derTagSequence, true, name); err != nil {
		return nil, err
	}
	return derChildren(data, elem)
}

func parseDEROID(data []byte, elem derElement) (asn1.ObjectIdentifier, error) {
	if err := requireDERElement(data, elem, derClassUniversal, derTagOID, false, "object identifier"); err != nil {
		return nil, err
	}
	var oid asn1.ObjectIdentifier
	rest, err := asn1.Unmarshal(elem.full(data), &oid)
	if err != nil || len(rest) != 0 {
		return nil, fmt.Errorf("%w: invalid object identifier", ErrInvalidResponse)
	}
	return oid, nil
}

func parseDERInteger(data []byte, elem derElement) (int, error) {
	if err := requireDERElement(data, elem, derClassUniversal, derTagInteger, false, "integer"); err != nil {
		return 0, err
	}
	var value int
	rest, err := asn1.Unmarshal(elem.full(data), &value)
	if err != nil || len(rest) != 0 {
		return 0, fmt.Errorf("%w: invalid integer", ErrInvalidResponse)
	}
	return value, nil
}

func parseDEREnumerated(data []byte, elem derElement) (int, error) {
	if err := requireDERElement(data, elem, derClassUniversal, derTagEnum, false, "enumerated"); err != nil {
		return 0, err
	}
	value := elem.content(data)
	if len(value) == 0 || len(value) > 8 {
		return 0, fmt.Errorf("%w: invalid enumerated value", ErrInvalidResponse)
	}
	if value[0]&0x80 != 0 {
		return 0, fmt.Errorf("%w: negative enumerated value", ErrInvalidResponse)
	}
	if len(value) > 1 && value[0] == 0 {
		return 0, fmt.Errorf("%w: non-minimal enumerated value", ErrInvalidResponse)
	}

	result := 0
	for _, octet := range value {
		if result > (int(^uint(0)>>1)-int(octet))/256 {
			return 0, fmt.Errorf("%w: enumerated value overflows int", ErrInvalidResponse)
		}
		result = result*256 + int(octet)
	}
	return result, nil
}

func parseDERBoolean(data []byte, elem derElement) (bool, error) {
	if err := requireDERElement(data, elem, derClassUniversal, derTagBoolean, false, "boolean"); err != nil {
		return false, err
	}
	value := elem.content(data)
	if len(value) != 1 || (value[0] != 0 && value[0] != 0xff) {
		return false, fmt.Errorf("%w: invalid boolean value", ErrInvalidResponse)
	}
	return value[0] == 0xff, nil
}

func parseDERTime(data []byte, elem derElement) (time.Time, error) {
	if elem.class != derClassUniversal || elem.constructed ||
		(elem.tag != derTagUTCTime && elem.tag != derTagGeneralizedTime) {
		return time.Time{}, fmt.Errorf("%w: invalid OCSP timestamp", ErrInvalidResponse)
	}
	var value time.Time
	rest, err := asn1.Unmarshal(elem.full(data), &value)
	if err != nil || len(rest) != 0 {
		return time.Time{}, fmt.Errorf("%w: invalid OCSP timestamp", ErrInvalidResponse)
	}
	return value, nil
}

func parseNonceExtensionValue(data []byte, elem derElement) ([]byte, int, int, error) {
	if err := requireDERElement(data, elem, derClassUniversal, derTagOctetString, false, "nonce extension value"); err != nil {
		return nil, 0, 0, err
	}

	value := elem.content(data)
	inner, err := parseSingleDERElement(value)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("%w: invalid nonce extension value", ErrInvalidResponse)
	}
	if err := requireDERElement(value, inner, derClassUniversal, derTagOctetString, false, "nonce value"); err != nil {
		return nil, 0, 0, err
	}

	nonceStart := elem.contentStart + inner.contentStart
	nonceEnd := elem.contentStart + inner.contentEnd
	return value[inner.contentStart:inner.contentEnd], nonceStart, nonceEnd, nil
}
