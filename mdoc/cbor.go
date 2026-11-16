package mdoc

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
)

// Tag is a CBOR tag together with its content value.
type Tag struct {
	ID      uint64
	Content any
}

// RawCBOR embeds already-encoded CBOR bytes inside a larger encoding without
// re-encoding them. It is used for the verifier-provided SessionTranscript,
// which must be embedded verbatim into the DeviceAuthentication structure.
type RawCBOR []byte

// Encode produces a deterministic CBOR encoding of value.
//
// The supported value universe covers the data model used by 18013-5
// structures: nil, booleans, integers (signed and unsigned), float64,
// strings, byte strings, tagged values, slices/arrays and maps keyed by
// string or integer. Map entries are emitted sorted by the bytewise
// lexicographic ordering of their encoded keys, as required by the
// deterministic encoding rules of RFC 8949 section 4.2.1.
func Encode(value any) ([]byte, error) {
	var out []byte
	if err := encodeAny(&out, value); err != nil {
		return nil, err
	}
	return out, nil
}

// ValidCBOR reports whether data is one complete, well-formed CBOR data item
// with no trailing bytes.
func ValidCBOR(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	rest, err := skipCBOR(data)
	if err != nil || len(rest) != 0 {
		return false
	}
	return true
}

func encodeAny(out *[]byte, value any) error {
	switch v := value.(type) {
	case nil:
		*out = append(*out, 0xf6)
	case RawCBOR:
		*out = append(*out, v...)
	case Tag:
		if err := encodeHead(out, 6, v.ID); err != nil {
			return err
		}
		return encodeAny(out, v.Content)
	case bool:
		if v {
			*out = append(*out, 0xf5)
		} else {
			*out = append(*out, 0xf4)
		}
	case []byte:
		if err := encodeHead(out, 2, uint64(len(v))); err != nil {
			return err
		}
		*out = append(*out, v...)
	case string:
		if err := encodeHead(out, 3, uint64(len(v))); err != nil {
			return err
		}
		*out = append(*out, v...)
	case int:
		return encodeInt(out, int64(v))
	case int64:
		return encodeInt(out, v)
	case uint64:
		return encodeHead(out, 0, v)
	case uint:
		return encodeHead(out, 0, uint64(v))
	case float64:
		*out = append(*out, 0xfb)
		bits := math.Float64bits(v)
		for shift := 56; shift >= 0; shift -= 8 {
			*out = append(*out, byte(bits>>shift))
		}
	default:
		return encodeReflect(out, value)
	}
	return nil
}

func encodeReflect(out *[]byte, value any) error {
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Slice:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			raw := rv.Bytes()
			if err := encodeHead(out, 2, uint64(len(raw))); err != nil {
				return err
			}
			*out = append(*out, raw...)
			return nil
		}
		return encodeSequence(out, rv)
	case reflect.Ptr:
		if rv.IsNil() {
			*out = append(*out, 0xf6)
			return nil
		}
		return encodeAny(out, rv.Elem().Interface())
	case reflect.Array:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			raw := make([]byte, rv.Len())
			reflect.Copy(reflect.ValueOf(raw), rv)
			if err := encodeHead(out, 2, uint64(len(raw))); err != nil {
				return err
			}
			*out = append(*out, raw...)
			return nil
		}
		return encodeSequence(out, rv)
	case reflect.Map:
		return encodeMap(out, rv)
	case reflect.Struct:
		return fmt.Errorf("%w: structs are not directly encodable", ErrEncoding)
	default:
		return fmt.Errorf("%w: unsupported Go type %s", ErrEncoding, rv.Kind())
	}
}

func encodeSequence(out *[]byte, rv reflect.Value) error {
	if err := encodeHead(out, 4, uint64(rv.Len())); err != nil {
		return err
	}
	for i := 0; i < rv.Len(); i++ {
		if err := encodeAny(out, rv.Index(i).Interface()); err != nil {
			return err
		}
	}
	return nil
}

type mapEntry struct {
	keyBytes []byte
	value    reflect.Value
}

func encodeMap(out *[]byte, rv reflect.Value) error {
	keys := rv.MapKeys()
	entries := make([]mapEntry, 0, len(keys))
	for _, key := range keys {
		var keyOut []byte
		if err := encodeAny(&keyOut, key.Interface()); err != nil {
			return err
		}
		entries = append(entries, mapEntry{keyBytes: keyOut, value: rv.MapIndex(key)})
	}
	sort.Slice(entries, func(i, j int) bool {
		return bytesLess(entries[i].keyBytes, entries[j].keyBytes)
	})
	if err := encodeHead(out, 5, uint64(len(entries))); err != nil {
		return err
	}
	for _, entry := range entries {
		*out = append(*out, entry.keyBytes...)
		if err := encodeAny(out, entry.value.Interface()); err != nil {
			return err
		}
	}
	return nil
}

func encodeInt(out *[]byte, value int64) error {
	if value >= 0 {
		return encodeHead(out, 0, uint64(value))
	}
	return encodeHead(out, 1, uint64(-value-1))
}

// encodeHead writes a CBOR initial byte and argument for the given major type.
func encodeHead(out *[]byte, major byte, argument uint64) error {
	switch {
	case argument < 24:
		*out = append(*out, major<<5|byte(argument))
	case argument >= 24 && argument <= math.MaxUint8:
		*out = append(*out, major<<5|24, byte(argument))
	case argument > math.MaxUint8 && argument <= math.MaxUint16:
		*out = append(*out, major<<5|25,
			byte(argument>>8), byte(argument))
	case argument > math.MaxUint16 && argument <= math.MaxUint32:
		*out = append(*out, major<<5|26,
			byte(argument>>24), byte(argument>>16),
			byte(argument>>8), byte(argument))
	case argument > math.MaxUint32:
		*out = append(*out, major<<5|27,
			byte(argument>>56), byte(argument>>48),
			byte(argument>>40), byte(argument>>32),
			byte(argument>>24), byte(argument>>16),
			byte(argument>>8), byte(argument))
	default:
		return fmt.Errorf("%w: impossible CBOR argument %d", ErrEncoding, argument)
	}
	return nil
}

func bytesLess(a, b []byte) bool {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// Minimal well-formedness parser used to validate embedded data such as the
// SessionTranscript. Indefinite-length strings are rejected while
// indefinite-length arrays and maps are accepted and skipped.

func skipCBOR(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty CBOR input")
	}
	initial := data[0]
	major := initial >> 5
	info := initial & 0x1f

	switch info {
	case 24, 25, 26, 27:
		width := 1 << (info - 24)
		if len(data) < 1+width {
			return nil, errors.New("truncated CBOR argument")
		}
		var argument uint64
		for _, b := range data[1 : 1+width] {
			argument = argument<<8 | uint64(b)
		}
		data = data[1+width:]
		return skipContent(major, argument, data)
	case 28, 29, 30:
		return nil, errors.New("reserved CBOR additional information")
	case 31:
		return skipIndefinite(major, data[1:])
	default:
		return skipContent(major, uint64(info), data[1:])
	}
}

func skipContent(major byte, argument uint64, data []byte) ([]byte, error) {
	switch major {
	case 0, 1: // unsigned / negative integer
		return data, nil
	case 2, 3: // byte string / text string
		if argument > uint64(len(data)) {
			return nil, errors.New("truncated CBOR string")
		}
		return data[argument:], nil
	case 4: // array
		for i := uint64(0); i < argument; i++ {
			var err error
			data, err = skipCBOR(data)
			if err != nil {
				return nil, err
			}
		}
		return data, nil
	case 5: // map
		for i := uint64(0); i < argument; i++ {
			var err error
			data, err = skipCBOR(data)
			if err != nil {
				return nil, err
			}
			data, err = skipCBOR(data)
			if err != nil {
				return nil, err
			}
		}
		return data, nil
	case 6: // tag
		return skipCBOR(data)
	case 7: // simple values and floats
		return data, nil
	default:
		return nil, fmt.Errorf("unknown CBOR major type %d", major)
	}
}

func skipIndefinite(major byte, data []byte) ([]byte, error) {
	switch major {
	case 2, 3:
		return nil, errors.New("indefinite-length CBOR strings are not accepted")
	case 4:
		for {
			if len(data) == 0 {
				return nil, errors.New("unterminated indefinite CBOR array")
			}
			if data[0] == 0xff {
				return data[1:], nil
			}
			var err error
			data, err = skipCBOR(data)
			if err != nil {
				return nil, err
			}
		}
	case 5:
		for {
			if len(data) == 0 {
				return nil, errors.New("unterminated indefinite CBOR map")
			}
			if data[0] == 0xff {
				return data[1:], nil
			}
			var err error
			data, err = skipCBOR(data)
			if err != nil {
				return nil, err
			}
			data, err = skipCBOR(data)
			if err != nil {
				return nil, err
			}
		}
	default:
		return nil, errors.New("invalid CBOR break code")
	}
}
