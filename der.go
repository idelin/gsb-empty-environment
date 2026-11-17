package netsec

import (
	"encoding/asn1"
	"errors"
	"fmt"
)

const (
	derClassUniversal       = 0
	derClassApplication     = 1
	derClassContextSpecific = 2
	derClassPrivate         = 3

	derTagBoolean          = 1
	derTagInteger          = 2
	derTagBitString        = 3
	derTagOctetString      = 4
	derTagNull             = 5
	derTagObjectIdentifier = 6
	derTagSequence         = 16
	derTagSet              = 17
)

// derValue is a single parsed DER TLV. Content contains the value octets and
// Full contains the complete encoding, including tag and length octets.
type derValue struct {
	Class       int
	Tag         int
	Constructed bool
	Content     []byte
	Full        []byte
}

var errIndefiniteLength = errors.New("netsec: indefinite lengths are not permitted in DER")

func readDER(data []byte) (derValue, []byte, error) {
	if len(data) < 2 {
		return derValue{}, nil, errors.New("netsec: truncated DER value")
	}

	first := data[0]
	v := derValue{
		Class:       int(first >> 6),
		Constructed: first&0x20 != 0,
		Tag:         int(first & 0x1f),
	}

	offset := 1
	if v.Tag == 0x1f {
		v.Tag = 0
		for {
			if offset >= len(data) {
				return derValue{}, nil, errors.New("netsec: truncated high-tag-number DER value")
			}
			b := data[offset]
			offset++
			if v.Tag > (int(^uint(0)>>1) >> 7) {
				return derValue{}, nil, errors.New("netsec: DER tag number overflows int")
			}
			v.Tag = v.Tag<<7 | int(b&0x7f)
			if b&0x80 == 0 {
				break
			}
		}
	}

	if offset >= len(data) {
		return derValue{}, nil, errors.New("netsec: truncated DER length")
	}
	lengthByte := data[offset]
	offset++

	var length int
	if lengthByte&0x80 == 0 {
		length = int(lengthByte)
	} else {
		n := int(lengthByte & 0x7f)
		if n == 0 {
			return derValue{}, nil, errIndefiniteLength
		}
		if n > 8 || offset+n > len(data) {
			return derValue{}, nil, errors.New("netsec: invalid DER length")
		}
		for i := 0; i < n; i++ {
			if length > (int(^uint(0)>>1) >> 8) {
				return derValue{}, nil, errors.New("netsec: DER length overflows int")
			}
			length = length<<8 | int(data[offset+i])
		}
		offset += n
	}

	if length < 0 || offset > len(data) || length > len(data)-offset {
		return derValue{}, nil, errors.New("netsec: DER value extends past input")
	}

	end := offset + length
	v.Content = data[offset:end]
	v.Full = data[:end]
	return v, data[end:], nil
}

func splitDER(data []byte) ([]derValue, error) {
	var values []derValue
	for len(data) > 0 {
		v, rest, err := readDER(data)
		if err != nil {
			return nil, err
		}
		values = append(values, v)
		data = rest
	}
	return values, nil
}

func encodeDERLength(length int) []byte {
	if length < 0 {
		panic("netsec: negative DER length")
	}
	if length < 0x80 {
		return []byte{byte(length)}
	}
	var tmp [8]byte
	i := len(tmp)
	for length > 0 {
		i--
		tmp[i] = byte(length)
		length >>= 8
	}
	out := make([]byte, 0, 1+len(tmp)-i)
	out = append(out, 0x80|byte(len(tmp)-i))
	out = append(out, tmp[i:]...)
	return out
}

func encodeDER(class, tag int, constructed bool, content []byte) []byte {
	if class < 0 || class > 3 || tag < 0 {
		panic("netsec: invalid DER class or tag")
	}

	first := byte(class << 6)
	if constructed {
		first |= 0x20
	}

	out := make([]byte, 0, 1+len(content)+8)
	if tag < 0x1f {
		out = append(out, first|byte(tag))
	} else {
		out = append(out, first|0x1f)
		var tmp [8]byte
		i := len(tmp)
		tmp[i-1] = byte(tag & 0x7f)
		i--
		tag >>= 7
		for tag > 0 {
			i--
			tmp[i] = byte(tag&0x7f) | 0x80
			tag >>= 7
		}
		out = append(out, tmp[i:]...)
	}
	out = append(out, encodeDERLength(len(content))...)
	out = append(out, content...)
	return out
}

func encodeDERSequence(parts ...[]byte) []byte {
	var content []byte
	for _, part := range parts {
		content = append(content, part...)
	}
	return encodeDER(derClassUniversal, derTagSequence, true, content)
}

func encodeDERSet(parts ...[]byte) []byte {
	var content []byte
	for _, part := range parts {
		content = append(content, part...)
	}
	return encodeDER(derClassUniversal, derTagSet, true, content)
}

func encodeDEROctetString(value []byte) []byte {
	return encodeDER(derClassUniversal, derTagOctetString, false, value)
}

func encodeDEROID(oid asn1.ObjectIdentifier) ([]byte, error) {
	return asn1.Marshal(oid)
}

func encodeDERNull() []byte {
	return []byte{0x05, 0x00}
}

func encodeDERInteger(value int64) []byte {
	der, err := asn1.Marshal(value)
	if err != nil {
		panic(err)
	}
	return der
}

func parseDEROID(v derValue) (asn1.ObjectIdentifier, error) {
	if v.Class != derClassUniversal || v.Tag != derTagObjectIdentifier {
		return nil, fmt.Errorf("netsec: expected OBJECT IDENTIFIER, got class %d tag %d", v.Class, v.Tag)
	}
	var oid asn1.ObjectIdentifier
	rest, err := asn1.Unmarshal(v.Full, &oid)
	if err != nil {
		return nil, fmt.Errorf("netsec: invalid OBJECT IDENTIFIER: %w", err)
	}
	if len(rest) != 0 {
		return nil, errors.New("netsec: trailing bytes in OBJECT IDENTIFIER")
	}
	return oid, nil
}

func parseDERInteger(v derValue) (int, error) {
	if v.Class != derClassUniversal || v.Tag != derTagInteger {
		return 0, fmt.Errorf("netsec: expected INTEGER, got class %d tag %d", v.Class, v.Tag)
	}
	var value int
	rest, err := asn1.Unmarshal(v.Full, &value)
	if err != nil {
		return 0, fmt.Errorf("netsec: invalid INTEGER: %w", err)
	}
	if len(rest) != 0 {
		return 0, errors.New("netsec: trailing bytes in INTEGER")
	}
	return value, nil
}

func parseDEROctetString(v derValue) ([]byte, error) {
	if v.Class != derClassUniversal || v.Tag != derTagOctetString {
		return nil, fmt.Errorf("netsec: expected OCTET STRING, got class %d tag %d", v.Class, v.Tag)
	}
	if !v.Constructed {
		return v.Content, nil
	}

	parts, err := splitDER(v.Content)
	if err != nil {
		return nil, err
	}
	var out []byte
	for _, part := range parts {
		value, err := parseDEROctetString(part)
		if err != nil {
			return nil, err
		}
		out = append(out, value...)
	}
	return out, nil
}

func rawValue(v derValue) asn1.RawValue {
	return asn1.RawValue{
		Class:      v.Class,
		Tag:        v.Tag,
		IsCompound: v.Constructed,
		Bytes:      v.Content,
		FullBytes:  v.Full,
	}
}
