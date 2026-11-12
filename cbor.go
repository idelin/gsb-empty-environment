package netsec

import (
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

const maxCBORDepth = 64

type dataFields struct {
	Value        []byte
	Validity     []byte
	ValidityType uint64
	Sequence     uint64
	TTL          uint64
}

type cborKind uint8

const (
	cborOther cborKind = iota
	cborUint
	cborBytes
	cborText
)

type cborValue struct {
	kind   cborKind
	bytes  []byte
	number uint64
}

type cborDecoder struct {
	data []byte
	off  int
}

func decodeData(data []byte) (*dataFields, error) {
	d := &cborDecoder{data: data}
	major, ai, err := d.readType()
	if err != nil {
		return nil, cborError(err)
	}
	if major != 5 || ai == 31 {
		return nil, cborError(errors.New("signed data is not a definite-length map"))
	}
	n, err := d.readArg(ai)
	if err != nil {
		return nil, cborError(err)
	}
	if n > uint64(len(d.data)-d.off) {
		return nil, cborError(errors.New("map length exceeds payload"))
	}

	fields := &dataFields{}
	var haveValue, haveValidity, haveValidityType, haveSequence, haveTTL bool
	seen := make(map[string]struct{}, n)
	for i := uint64(0); i < n; i++ {
		key, err := d.readTextKey()
		if err != nil {
			return nil, cborError(err)
		}
		if _, ok := seen[key]; ok {
			return nil, cborError(fmt.Errorf("duplicate map key %q", key))
		}
		seen[key] = struct{}{}
		value, err := d.readValue(1)
		if err != nil {
			return nil, cborError(err)
		}

		switch key {
		case "Value":
			if value.kind != cborBytes {
				return nil, cborError(errors.New("Value is not a byte string"))
			}
			fields.Value = value.bytes
			haveValue = true
		case "Validity":
			if value.kind != cborBytes {
				return nil, cborError(errors.New("Validity is not a byte string"))
			}
			fields.Validity = value.bytes
			haveValidity = true
		case "ValidityType":
			if value.kind != cborUint {
				return nil, cborError(errors.New("ValidityType is not an unsigned integer"))
			}
			fields.ValidityType = value.number
			haveValidityType = true
		case "Sequence":
			if value.kind != cborUint {
				return nil, cborError(errors.New("Sequence is not an unsigned integer"))
			}
			fields.Sequence = value.number
			haveSequence = true
		case "TTL":
			if value.kind != cborUint {
				return nil, cborError(errors.New("TTL is not an unsigned integer"))
			}
			fields.TTL = value.number
			haveTTL = true
		}
	}
	if d.off != len(d.data) {
		return nil, cborError(errors.New("trailing bytes after signed data"))
	}
	if !haveValue || !haveValidity || !haveValidityType || !haveSequence || !haveTTL {
		return nil, cborError(errors.New("signed data is missing required fields"))
	}
	return fields, nil
}

func cborError(err error) error {
	if errors.Is(err, ErrMalformedData) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrMalformedData, err)
}

func (d *cborDecoder) readType() (byte, byte, error) {
	if d.off >= len(d.data) {
		return 0, 0, io.ErrUnexpectedEOF
	}
	b := d.data[d.off]
	d.off++
	return b >> 5, b & 0x1f, nil
}

func (d *cborDecoder) readArg(ai byte) (uint64, error) {
	switch {
	case ai < 24:
		return uint64(ai), nil
	case ai == 24:
		if d.off+1 > len(d.data) {
			return 0, io.ErrUnexpectedEOF
		}
		v := uint64(d.data[d.off])
		d.off++
		return v, nil
	case ai == 25:
		return d.readUint(2)
	case ai == 26:
		return d.readUint(4)
	case ai == 27:
		return d.readUint(8)
	case ai == 31:
		return 0, errors.New("indefinite-length item is not allowed")
	default:
		return 0, fmt.Errorf("reserved additional information %d", ai)
	}
}

func (d *cborDecoder) readUint(n int) (uint64, error) {
	if d.off+n > len(d.data) {
		return 0, io.ErrUnexpectedEOF
	}
	var v uint64
	for i := 0; i < n; i++ {
		v = v<<8 | uint64(d.data[d.off+i])
	}
	d.off += n
	return v, nil
}

func (d *cborDecoder) readTextKey() (string, error) {
	major, ai, err := d.readType()
	if err != nil {
		return "", err
	}
	if major != 3 || ai == 31 {
		return "", errors.New("map key is not a definite-length text string")
	}
	n, err := d.readArg(ai)
	if err != nil {
		return "", err
	}
	if n > uint64(len(d.data)-d.off) {
		return "", io.ErrUnexpectedEOF
	}
	s := string(d.data[d.off : d.off+int(n)])
	if !utf8.ValidString(s) {
		return "", errors.New("map key is not valid UTF-8")
	}
	d.off += int(n)
	return s, nil
}

func (d *cborDecoder) readValue(depth int) (cborValue, error) {
	if depth > maxCBORDepth {
		return cborValue{}, errors.New("CBOR nesting is too deep")
	}
	major, ai, err := d.readType()
	if err != nil {
		return cborValue{}, err
	}
	switch major {
	case 0:
		n, err := d.readArg(ai)
		if err != nil {
			return cborValue{}, err
		}
		return cborValue{kind: cborUint, number: n}, nil
	case 1:
		if ai == 31 {
			return cborValue{}, errors.New("indefinite-length item is not allowed")
		}
		if _, err := d.readArg(ai); err != nil {
			return cborValue{}, err
		}
		return cborValue{kind: cborOther}, nil
	case 2, 3:
		if ai == 31 {
			return cborValue{}, errors.New("indefinite-length string is not allowed")
		}
		n, err := d.readArg(ai)
		if err != nil {
			return cborValue{}, err
		}
		if n > uint64(len(d.data)-d.off) {
			return cborValue{}, io.ErrUnexpectedEOF
		}
		b := d.data[d.off : d.off+int(n)]
		d.off += int(n)
		if major == 3 {
			if !utf8.Valid(b) {
				return cborValue{}, errors.New("text string is not valid UTF-8")
			}
			return cborValue{kind: cborText, bytes: b}, nil
		}
		return cborValue{kind: cborBytes, bytes: b}, nil
	case 4:
		if ai == 31 {
			return cborValue{}, errors.New("indefinite-length array is not allowed")
		}
		n, err := d.readArg(ai)
		if err != nil {
			return cborValue{}, err
		}
		if n > uint64(len(d.data)-d.off) {
			return cborValue{}, io.ErrUnexpectedEOF
		}
		for i := uint64(0); i < n; i++ {
			if _, err := d.readValue(depth + 1); err != nil {
				return cborValue{}, err
			}
		}
		return cborValue{kind: cborOther}, nil
	case 5:
		if ai == 31 {
			return cborValue{}, errors.New("indefinite-length map is not allowed")
		}
		n, err := d.readArg(ai)
		if err != nil {
			return cborValue{}, err
		}
		if n > uint64(len(d.data)-d.off) {
			return cborValue{}, io.ErrUnexpectedEOF
		}
		seen := make(map[string]struct{}, n)
		for i := uint64(0); i < n; i++ {
			key, err := d.readTextKey()
			if err != nil {
				return cborValue{}, err
			}
			if _, ok := seen[key]; ok {
				return cborValue{}, fmt.Errorf("duplicate nested map key %q", key)
			}
			seen[key] = struct{}{}
			if _, err := d.readValue(depth + 1); err != nil {
				return cborValue{}, err
			}
		}
		return cborValue{kind: cborOther}, nil
	case 6:
		if ai == 31 {
			return cborValue{}, errors.New("indefinite-length item is not allowed")
		}
		if _, err := d.readArg(ai); err != nil {
			return cborValue{}, err
		}
		if _, err := d.readValue(depth + 1); err != nil {
			return cborValue{}, err
		}
		return cborValue{kind: cborOther}, nil
	case 7:
		switch {
		case ai < 24:
			return cborValue{kind: cborOther}, nil
		case ai == 24:
			if d.off+1 > len(d.data) {
				return cborValue{}, io.ErrUnexpectedEOF
			}
			d.off++
		case ai == 25:
			if d.off+2 > len(d.data) {
				return cborValue{}, io.ErrUnexpectedEOF
			}
			d.off += 2
		case ai == 26:
			if d.off+4 > len(d.data) {
				return cborValue{}, io.ErrUnexpectedEOF
			}
			d.off += 4
		case ai == 27:
			if d.off+8 > len(d.data) {
				return cborValue{}, io.ErrUnexpectedEOF
			}
			d.off += 8
		default:
			return cborValue{}, fmt.Errorf("invalid simple/float encoding %d", ai)
		}
		return cborValue{kind: cborOther}, nil
	default:
		return cborValue{}, fmt.Errorf("invalid CBOR major type %d", major)
	}
}

// encodeData produces the canonical DAG-CBOR payload used by tests and by
// callers that need to construct a record. Map keys are ordered by encoded
// key length and then bytewise, as required by DAG-CBOR.
func encodeData(value, validity []byte, validityType ValidityType, sequence, ttl uint64) []byte {
	var out []byte
	out = append(out, 0xa5) // map with five entries

	out = appendCBORText(out, "TTL")
	out = appendCBORUint(out, ttl)
	out = appendCBORText(out, "Value")
	out = appendCBORBytes(out, value)
	out = appendCBORText(out, "Sequence")
	out = appendCBORUint(out, sequence)
	out = appendCBORText(out, "Validity")
	out = appendCBORBytes(out, validity)
	out = appendCBORText(out, "ValidityType")
	out = appendCBORUint(out, uint64(validityType))
	return out
}

func appendCBORText(out []byte, s string) []byte {
	out = appendCBORHead(out, 3, uint64(len(s)))
	return append(out, s...)
}

func appendCBORBytes(out []byte, b []byte) []byte {
	out = appendCBORHead(out, 2, uint64(len(b)))
	return append(out, b...)
}

func appendCBORUint(out []byte, n uint64) []byte {
	return appendCBORHead(out, 0, n)
}

func appendCBORHead(out []byte, major byte, n uint64) []byte {
	switch {
	case n < 24:
		return append(out, major<<5|byte(n))
	case n <= 0xff:
		return append(out, major<<5|24, byte(n))
	case n <= 0xffff:
		return append(out, major<<5|25, byte(n>>8), byte(n))
	case n <= 0xffffffff:
		return append(out, major<<5|26, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	default:
		return append(out, major<<5|27,
			byte(n>>56), byte(n>>48), byte(n>>40), byte(n>>32),
			byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
}
