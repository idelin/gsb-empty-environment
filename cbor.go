package netsec

import "encoding/binary"

// Text keys of the CBOR map that makes up IpnsEntry.data. The spelling is
// fixed by the IPNS specification.
const (
	cborKeyValue        = "Value"
	cborKeyValidity     = "Validity"
	cborKeyValidityType = "ValidityType"
	cborKeySequence     = "Sequence"
	cborKeyTTL          = "TTL"
)

// cborData is the decoded IpnsEntry.data CBOR map.
type cborData struct {
	Value        []byte
	Validity     []byte
	ValidityType uint64
	Sequence     uint64
	TTL          uint64

	present map[string]bool
}

// parseCBOR decodes the single definite-length CBOR map that carries the
// signed IPNS fields. The decoder is intentionally narrow:
//
//   - indefinite-length items, tags, floats and simple values are rejected;
//   - duplicate map keys are rejected, including unknown ones;
//   - every one of the five specification keys must appear exactly once.
func parseCBOR(data []byte) (*cborData, error) {
	major, count, n, err := readCBORHead(data)
	if err != nil {
		return nil, err
	}
	if major != 5 {
		return nil, ErrMalformedRecord
	}

	buf := data[n:]
	parsed := &cborData{present: make(map[string]bool)}

	for i := uint64(0); i < count; i++ {
		key, rest, err := readCBORTstr(buf)
		if err != nil {
			return nil, err
		}
		buf = rest
		if parsed.present[key] {
			return nil, ErrMalformedRecord
		}
		parsed.present[key] = true

		switch key {
		case cborKeyValue:
			parsed.Value, buf, err = readCBORBstr(buf)
		case cborKeyValidity:
			// The spec writes Validity as a text string, while the Go
			// reference implementation serialises it as a byte string; both
			// encodings carry the same RFC 3339 bytes.
			parsed.Validity, buf, err = readCBORString(buf)
		case cborKeyValidityType:
			parsed.ValidityType, buf, err = readCBORUint(buf)
		case cborKeySequence:
			parsed.Sequence, buf, err = readCBORUint(buf)
		case cborKeyTTL:
			parsed.TTL, buf, err = readCBORUint(buf)
		default:
			buf, err = skipCBOR(buf)
		}
		if err != nil {
			return nil, err
		}
	}

	if len(buf) != 0 {
		return nil, ErrMalformedRecord
	}

	required := []string{
		cborKeyValue,
		cborKeyValidity,
		cborKeyValidityType,
		cborKeySequence,
		cborKeyTTL,
	}
	for _, key := range required {
		if !parsed.present[key] {
			return nil, ErrMalformedRecord
		}
	}
	return parsed, nil
}

// readCBORHead parses one CBOR initial byte plus its argument. Indefinite
// length (additional info 31) and reserved arguments (28-30) are refused.
func readCBORHead(buf []byte) (major byte, value uint64, n int, err error) {
	if len(buf) == 0 {
		return 0, 0, 0, ErrMalformedRecord
	}

	initial := buf[0]
	major = initial >> 5
	info := initial & 0x1f
	n = 1

	switch {
	case info < 24:
		value = uint64(info)
	case info == 24:
		if len(buf) < 2 {
			return 0, 0, 0, ErrMalformedRecord
		}
		value = uint64(buf[1])
		n = 2
	case info == 25:
		if len(buf) < 3 {
			return 0, 0, 0, ErrMalformedRecord
		}
		value = uint64(binary.BigEndian.Uint16(buf[1:3]))
		n = 3
	case info == 26:
		if len(buf) < 5 {
			return 0, 0, 0, ErrMalformedRecord
		}
		value = uint64(binary.BigEndian.Uint32(buf[1:5]))
		n = 5
	case info == 27:
		if len(buf) < 9 {
			return 0, 0, 0, ErrMalformedRecord
		}
		value = binary.BigEndian.Uint64(buf[1:9])
		n = 9
	default:
		return 0, 0, 0, ErrMalformedRecord
	}

	return major, value, n, nil
}

func readCBORUint(buf []byte) (uint64, []byte, error) {
	major, value, n, err := readCBORHead(buf)
	if err != nil {
		return 0, nil, err
	}
	if major != 0 {
		// Negative integers and non-integer heads are not valid here.
		return 0, nil, ErrMalformedRecord
	}
	return value, buf[n:], nil
}

func readCBORBstr(buf []byte) ([]byte, []byte, error) {
	major, length, n, err := readCBORHead(buf)
	if err != nil {
		return nil, nil, err
	}
	if major != 2 {
		return nil, nil, ErrMalformedRecord
	}
	buf = buf[n:]
	if length > uint64(len(buf)) {
		return nil, nil, ErrMalformedRecord
	}
	return buf[:length], buf[length:], nil
}

func readCBORTstr(buf []byte) (string, []byte, error) {
	major, length, n, err := readCBORHead(buf)
	if err != nil {
		return "", nil, err
	}
	if major != 3 {
		return "", nil, ErrMalformedRecord
	}
	buf = buf[n:]
	if length > uint64(len(buf)) {
		return "", nil, ErrMalformedRecord
	}
	return string(buf[:length]), buf[length:], nil
}

// readCBORString accepts either a byte string (major 2) or a text string
// (major 3) and returns its raw content bytes.
func readCBORString(buf []byte) ([]byte, []byte, error) {
	major, length, n, err := readCBORHead(buf)
	if err != nil {
		return nil, nil, err
	}
	if major != 2 && major != 3 {
		return nil, nil, ErrMalformedRecord
	}
	buf = buf[n:]
	if length > uint64(len(buf)) {
		return nil, nil, ErrMalformedRecord
	}
	return append([]byte(nil), buf[:length]...), buf[length:], nil
}

// skipCBOR advances past one well-formed definite-length CBOR item. Tags and
// floating-point/simple values never occur in an IPNS data map and are
// rejected rather than ignored.
func skipCBOR(buf []byte) ([]byte, error) {
	major, value, n, err := readCBORHead(buf)
	if err != nil {
		return nil, err
	}
	buf = buf[n:]

	switch major {
	case 0, 1: // unsigned or negative integer
		return buf, nil
	case 2, 3: // byte or text string
		if value > uint64(len(buf)) {
			return nil, ErrMalformedRecord
		}
		return buf[value:], nil
	case 4: // array
		for i := uint64(0); i < value; i++ {
			buf, err = skipCBOR(buf)
			if err != nil {
				return nil, err
			}
		}
		return buf, nil
	case 5: // map
		for i := uint64(0); i < value; i++ {
			buf, err = skipCBOR(buf)
			if err != nil {
				return nil, err
			}
			buf, err = skipCBOR(buf)
			if err != nil {
				return nil, err
			}
		}
		return buf, nil
	default: // 6 = tag, 7 = simple/float
		return nil, ErrMalformedRecord
	}
}
