package netsec

import (
	"fmt"
	"sort"
	"unicode/utf8"
)

var cborRequiredKeys = []string{"Value", "Validity", "ValidityType", "Sequence", "TTL"}

// parseCBORPayload decodes the deterministic CBOR subset used inside IPNS V2
// records: a single top-level map with the five required text keys.
func parseCBORPayload(data []byte) (map[string]any, error) {
	dec := &cborDecoder{data: data}
	v, err := dec.decode()
	if err != nil {
		return nil, err
	}
	if dec.off != len(dec.data) {
		return nil, fmt.Errorf("%w: trailing CBOR bytes", ErrMalformedRecord)
	}

	fields, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: CBOR payload is not a map", ErrMalformedRecord)
	}
	if len(fields) != len(cborRequiredKeys) {
		return nil, fmt.Errorf("%w: CBOR payload must contain exactly %d keys", ErrMalformedRecord, len(cborRequiredKeys))
	}
	for _, key := range cborRequiredKeys {
		if _, ok := fields[key]; !ok {
			return nil, fmt.Errorf("%w: missing CBOR key %q", ErrMalformedRecord, key)
		}
	}
	return fields, nil
}

type cborDecoder struct {
	data []byte
	off  int
}

func (d *cborDecoder) decode() (any, error) {
	if d.off >= len(d.data) {
		return nil, fmt.Errorf("%w: truncated CBOR item", ErrMalformedRecord)
	}
	initial := d.data[d.off]
	d.off++

	major := initial >> 5
	info := initial & 0x1f

	switch major {
	case 0, 1:
		n, err := d.readUint(info)
		if err != nil {
			return nil, err
		}
		if major == 0 {
			return n, nil
		}
		return int64(-1) - int64(n), nil
	case 2, 3:
		length, err := d.readUint(info)
		if err != nil {
			return nil, err
		}
		if length > uint64(len(d.data)-d.off) {
			return nil, fmt.Errorf("%w: truncated CBOR string", ErrMalformedRecord)
		}
		raw := d.data[d.off : d.off+int(length)]
		d.off += int(length)
		if major == 2 {
			return append([]byte(nil), raw...), nil
		}
		if !utf8.Valid(raw) {
			return nil, fmt.Errorf("%w: invalid UTF-8 CBOR text", ErrMalformedRecord)
		}
		return string(raw), nil
	case 4:
		count, err := d.readCount(info)
		if err != nil {
			return nil, err
		}
		arr := make([]any, count)
		for i := range arr {
			arr[i], err = d.decode()
			if err != nil {
				return nil, err
			}
		}
		return arr, nil
	case 5:
		count, err := d.readCount(info)
		if err != nil {
			return nil, err
		}
		m := make(map[string]any, count)
		keys := make([]string, 0, count)
		for i := 0; i < count; i++ {
			keyItem, err := d.decode()
			if err != nil {
				return nil, err
			}
			key, ok := keyItem.(string)
			if !ok {
				return nil, fmt.Errorf("%w: non-text CBOR map key", ErrMalformedRecord)
			}
			if _, exists := m[key]; exists {
				return nil, fmt.Errorf("%w: duplicate CBOR map key %q", ErrMalformedRecord, key)
			}
			val, err := d.decode()
			if err != nil {
				return nil, err
			}
			m[key] = val
			keys = append(keys, key)
		}
		if !sort.StringsAreSorted(keys) {
			return nil, fmt.Errorf("%w: CBOR map keys are not in canonical order", ErrMalformedRecord)
		}
		return m, nil
	case 7:
		return nil, fmt.Errorf("%w: unsupported CBOR simple/float item", ErrMalformedRecord)
	default:
		return nil, fmt.Errorf("%w: unsupported CBOR tag item", ErrMalformedRecord)
	}
}

func (d *cborDecoder) readCount(info byte) (int, error) {
	if info == 31 {
		return 0, fmt.Errorf("%w: indefinite-length CBOR not supported", ErrMalformedRecord)
	}
	n, err := d.readUint(info)
	if err != nil {
		return 0, err
	}
	if n > uint64(len(d.data)-d.off) {
		return 0, fmt.Errorf("%w: truncated CBOR container", ErrMalformedRecord)
	}
	return int(n), nil
}

func (d *cborDecoder) readUint(info byte) (uint64, error) {
	switch {
	case info < 24:
		return uint64(info), nil
	case info == 24:
		if d.off+1 > len(d.data) {
			return 0, truncated()
		}
		v := d.data[d.off]
		d.off++
		return uint64(v), nil
	case info == 25:
		if d.off+2 > len(d.data) {
			return 0, truncated()
		}
		v := uint64(d.data[d.off])<<8 | uint64(d.data[d.off+1])
		d.off += 2
		return v, nil
	case info == 26:
		if d.off+4 > len(d.data) {
			return 0, truncated()
		}
		v := uint64(d.data[d.off])<<24 | uint64(d.data[d.off+1])<<16 |
			uint64(d.data[d.off+2])<<8 | uint64(d.data[d.off+3])
		d.off += 4
		return v, nil
	case info == 27:
		if d.off+8 > len(d.data) {
			return 0, truncated()
		}
		var v uint64
		for i := 0; i < 8; i++ {
			v = v<<8 | uint64(d.data[d.off+i])
		}
		d.off += 8
		return v, nil
	default:
		return 0, fmt.Errorf("%w: unsupported CBOR additional info %d", ErrMalformedRecord, info)
	}
}

func truncated() error {
	return fmt.Errorf("%w: truncated CBOR integer", ErrMalformedRecord)
}
