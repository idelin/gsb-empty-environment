package netsec

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	derTagSequence       = 0x30
	derTagSet            = 0x31
	derTagOID            = 0x06
	derTagContextZeroSet = 0xa0
)

var smimeCapabilitiesOID = []int{1, 2, 840, 113549, 1, 9, 16, 2, 10}

// ParseSMIMECapabilities parses the DER value of the S/MIME signing attribute
// id-smime-aa-smimecapabilities (1.2.840.113549.1.9.16.2.10).
//
// The input must be the single AttributeValue, i.e. the SEQUENCE OF
// SMIMECapability, not the enclosing CMS Attribute. Use
// ExtractSMIMECapabilitiesFromSignedAttributes when starting with the
// SignedAttributes structure.
//
// Known capability OIDs are returned as canonical Algorithm values. Unknown
// OIDs are retained in dotted form so that a caller whitelist can still make
// an explicit decision about them.
func ParseSMIMECapabilities(der []byte) ([]Algorithm, error) {
	if len(der) == 0 {
		return nil, fmt.Errorf("%w: empty input", ErrInvalidCapabilities)
	}
	tag, content, rest, err := readDERValue(der)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCapabilities, err)
	}
	if tag != derTagSequence {
		return nil, fmt.Errorf("%w: expected SEQUENCE, got tag 0x%x", ErrInvalidCapabilities, tag)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: trailing data", ErrInvalidCapabilities)
	}
	algorithms, err := parseCapabilitySequence(content)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCapabilities, err)
	}
	return algorithms, nil
}

// ExtractSMIMECapabilitiesFromSignedAttributes extracts the S/MIME
// capabilities from a CMS SignedAttributes structure.
//
// signedAttributes may be encoded as the [0] IMPLICIT SET OF Attribute used by
// CMS or as a bare SET OF Attribute. The bool reports whether the capabilities
// attribute is present. A present but empty attribute is treated as malformed.
func ExtractSMIMECapabilitiesFromSignedAttributes(signedAttributes []byte) ([]Algorithm, bool, error) {
	if len(signedAttributes) == 0 {
		return nil, false, fmt.Errorf("%w: empty SignedAttributes", ErrInvalidCapabilities)
	}
	tag, content, rest, err := readDERValue(signedAttributes)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrInvalidCapabilities, err)
	}
	if tag != derTagContextZeroSet && tag != derTagSet {
		return nil, false, fmt.Errorf("%w: expected SignedAttributes SET, got tag 0x%x", ErrInvalidCapabilities, tag)
	}
	if len(rest) != 0 {
		return nil, false, fmt.Errorf("%w: trailing data", ErrInvalidCapabilities)
	}

	var (
		found      bool
		algorithms []Algorithm
	)
	for len(content) > 0 {
		attrTag, attrContent, attrRest, err := readDERValue(content)
		if err != nil {
			return nil, false, fmt.Errorf("%w: %v", ErrInvalidCapabilities, err)
		}
		content = attrRest
		if attrTag != derTagSequence {
			return nil, false, fmt.Errorf("%w: expected Attribute SEQUENCE, got tag 0x%x", ErrInvalidCapabilities, attrTag)
		}

		attributeOID, values, err := parseAttribute(attrContent)
		if err != nil {
			return nil, false, fmt.Errorf("%w: %v", ErrInvalidCapabilities, err)
		}
		if !oidEqual(attributeOID, smimeCapabilitiesOID) {
			continue
		}
		if found {
			return nil, false, fmt.Errorf("%w: duplicate SMIMECapabilities attribute", ErrInvalidCapabilities)
		}
		found = true
		if len(values) != 1 {
			return nil, false, fmt.Errorf("%w: SMIMECapabilities attribute must have exactly one value", ErrInvalidCapabilities)
		}
		valueTag, valueContent, valueRest, err := readDERValue(values[0])
		if err != nil {
			return nil, false, fmt.Errorf("%w: %v", ErrInvalidCapabilities, err)
		}
		if len(valueRest) != 0 || valueTag != derTagSequence {
			return nil, false, fmt.Errorf("%w: malformed SMIMECapabilities value", ErrInvalidCapabilities)
		}
		algorithms, err = parseCapabilitySequence(valueContent)
		if err != nil {
			return nil, false, fmt.Errorf("%w: %v", ErrInvalidCapabilities, err)
		}
	}
	return algorithms, found, nil
}

func parseAttribute(content []byte) ([]int, [][]byte, error) {
	oidTag, oidContent, rest, err := readDERValue(content)
	if err != nil {
		return nil, nil, err
	}
	if oidTag != derTagOID {
		return nil, nil, fmt.Errorf("expected Attribute OID, got tag 0x%x", oidTag)
	}
	oid, err := parseOID(oidContent)
	if err != nil {
		return nil, nil, err
	}

	valuesTag, valuesContent, valuesRest, err := readDERValue(rest)
	if err != nil {
		return nil, nil, err
	}
	if valuesTag != derTagSet {
		return nil, nil, fmt.Errorf("expected Attribute values SET, got tag 0x%x", valuesTag)
	}
	if len(valuesRest) != 0 {
		return nil, nil, fmt.Errorf("trailing data in Attribute")
	}

	var values [][]byte
	for len(valuesContent) > 0 {
		_, _, valueRest, err := readDERValue(valuesContent)
		if err != nil {
			return nil, nil, err
		}
		values = append(values, valuesContent[:len(valuesContent)-len(valueRest)])
		valuesContent = valueRest
	}
	return oid, values, nil
}

func parseCapabilitySequence(content []byte) ([]Algorithm, error) {
	var (
		algorithms []Algorithm
		seen       = make(map[Algorithm]struct{})
	)
	for len(content) > 0 {
		tag, capabilityContent, rest, err := readDERValue(content)
		if err != nil {
			return nil, err
		}
		content = rest
		if tag != derTagSequence {
			return nil, fmt.Errorf("expected SMIMECapability SEQUENCE, got tag 0x%x", tag)
		}

		oidTag, oidContent, capabilityRest, err := readDERValue(capabilityContent)
		if err != nil {
			return nil, err
		}
		if oidTag != derTagOID {
			return nil, fmt.Errorf("expected SMIMECapability capabilityID OID, got tag 0x%x", oidTag)
		}
		oidInts, err := parseOID(oidContent)
		if err != nil {
			return nil, err
		}
		if len(capabilityRest) > 0 {
			_, _, parameterRest, err := readDERValue(capabilityRest)
			if err != nil {
				return nil, fmt.Errorf("invalid SMIMECapability parameters: %v", err)
			}
			if len(parameterRest) != 0 {
				return nil, fmt.Errorf("trailing data in SMIMECapability")
			}
		}

		algorithm := algorithmFromOID(formatOID(oidInts))
		if _, duplicate := seen[algorithm]; duplicate {
			continue
		}
		seen[algorithm] = struct{}{}
		algorithms = append(algorithms, algorithm)
	}
	if algorithms == nil {
		return []Algorithm{}, nil
	}
	return algorithms, nil
}

func parseOID(content []byte) ([]int, error) {
	if len(content) == 0 {
		return nil, fmt.Errorf("empty OID")
	}
	var (
		arcs  []int
		value uint64
		open  bool
	)
	for i, b := range content {
		if !open && b == 0x80 {
			return nil, fmt.Errorf("non-minimal OID encoding")
		}
		if value > (^uint64(0) >> 7) {
			return nil, fmt.Errorf("OID arc overflow")
		}
		value = value<<7 | uint64(b&0x7f)
		open = b&0x80 != 0
		if !open {
			if value > uint64(^uint(0)>>1) {
				return nil, fmt.Errorf("OID arc out of range")
			}
			arcs = append(arcs, int(value))
			value = 0
		}
		if i == len(content)-1 && open {
			return nil, fmt.Errorf("truncated OID")
		}
	}

	firstValue := arcs[0]
	firstArc := firstValue / 40
	if firstArc > 2 {
		firstArc = 2
	}
	secondArc := firstValue - firstArc*40
	if firstArc < 2 && secondArc > 39 {
		return nil, fmt.Errorf("invalid second OID arc")
	}
	result := make([]int, 0, len(arcs)+1)
	result = append(result, firstArc, secondArc)
	result = append(result, arcs[1:]...)
	return result, nil
}

func formatOID(oid []int) string {
	var b strings.Builder
	for i, arc := range oid {
		if i != 0 {
			b.WriteByte('.')
		}
		b.WriteString(strconv.Itoa(arc))
	}
	return b.String()
}

func oidEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func readDERValue(data []byte) (tag byte, content, rest []byte, err error) {
	if len(data) < 2 {
		return 0, nil, nil, fmt.Errorf("truncated DER value")
	}
	tag = data[0]
	if tag&0x1f == 0x1f {
		return 0, nil, nil, fmt.Errorf("multi-byte DER tags are not supported")
	}

	lengthByte := data[1]
	headerLength := 2
	var length uint64
	if lengthByte < 0x80 {
		length = uint64(lengthByte)
	} else {
		octets := int(lengthByte & 0x7f)
		if octets == 0 {
			return 0, nil, nil, fmt.Errorf("indefinite DER length is not allowed")
		}
		if octets > 8 || len(data) < 2+octets {
			return 0, nil, nil, fmt.Errorf("invalid DER length")
		}
		if data[2] == 0 {
			return 0, nil, nil, fmt.Errorf("non-minimal DER length")
		}
		headerLength += octets
		for _, b := range data[2:headerLength] {
			if length > (^uint64(0) >> 8) {
				return 0, nil, nil, fmt.Errorf("DER length overflow")
			}
			length = length<<8 | uint64(b)
		}
		if length < 0x80 {
			return 0, nil, nil, fmt.Errorf("non-minimal DER length")
		}
	}
	if length > uint64(len(data)-headerLength) {
		return 0, nil, nil, fmt.Errorf("truncated DER content")
	}
	end := headerLength + int(length)
	return tag, data[headerLength:end], data[end:], nil
}
