package codec

import (
	"fmt"
	"reflect"
	"unsafe"

	"pbcodec/wire"
)

// Unmarshal parses the protobuf wire data in data into msg, which must be a
// pointer to a struct. It is safe for concurrent use: all parser state is
// per-call and the schema cache is a sync.Map.
//
// Decoding is zero-copy: string and []byte fields alias data, so data must
// not be mutated or freed while the decoded message is in use (this is what
// makes the parser suitable for memory-mapped buffers).
//
// Every read is bounds-checked; truncated or hostile input yields an error,
// never a panic or an out-of-bounds access.
func Unmarshal(data []byte, msg any) error {
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return ErrNotMessage
	}
	sv, err := structValue(v)
	if err != nil {
		return err
	}
	return unmarshalStruct(wire.NewReader(data), sv, 0)
}

func unmarshalStruct(r *wire.Reader, sv reflect.Value, depth int) error {
	if depth > MaxDepth {
		return ErrMaxDepth
	}
	si, err := cachedInfo(sv.Type())
	if err != nil {
		return err
	}
	var unknown []byte
	for !r.Done() {
		start := r.Offset()
		num, wt, err := r.Tag()
		if err != nil {
			return err
		}
		fi := si.byNum[num]
		if fi == nil {
			// Unknown field: skip safely, keep raw bytes for round trips.
			if err := r.Skip(wt); err != nil {
				return fmt.Errorf("codec: field %d: %w", num, err)
			}
			if si.unknownIdx >= 0 {
				unknown = append(unknown, r.Slice(start, r.Offset())...)
			}
			continue
		}
		if err := decodeField(r, sv.Field(fi.index), fi, wt, depth); err != nil {
			return fmt.Errorf("codec: field %d: %w", num, err)
		}
	}
	if si.unknownIdx >= 0 && len(unknown) > 0 {
		raw := sv.Field(si.unknownIdx).FieldByName("Raw")
		raw.SetBytes(append(raw.Bytes(), unknown...))
	}
	return nil
}

func decodeField(r *wire.Reader, v reflect.Value, fi *fieldInfo, wt, depth int) error {
	switch {
	case fi.isSlice && fi.isMsg:
		if wt != wire.WireBytes {
			return ErrWireType
		}
		elem, err := decodeMessage(r, fi, v.Type().Elem(), depth)
		if err != nil {
			return err
		}
		v.Set(reflect.Append(v, elem))
	case fi.isSlice && fi.isBytes: // []string
		if wt != wire.WireBytes {
			return ErrWireType
		}
		b, err := r.Bytes()
		if err != nil {
			return err
		}
		v.Set(reflect.Append(v, reflect.ValueOf(zeroCopyString(b))))
	case fi.isSlice: // repeated scalar: accept packed and unpacked
		if wt == wire.WireBytes {
			b, err := r.Bytes()
			if err != nil {
				return err
			}
			sub := wire.NewReader(b)
			for !sub.Done() {
				if err := appendScalar(sub, v, fi.scalar); err != nil {
					return err
				}
			}
		} else if wt == fi.scalar.wireType() {
			if err := appendScalar(r, v, fi.scalar); err != nil {
				return err
			}
		} else {
			return ErrWireType
		}
	case fi.isMsg:
		if wt != wire.WireBytes {
			return ErrWireType
		}
		elem, err := decodeMessage(r, fi, v.Type(), depth)
		if err != nil {
			return err
		}
		v.Set(elem)
	case fi.isBytes:
		if wt != wire.WireBytes {
			return ErrWireType
		}
		b, err := r.Bytes()
		if err != nil {
			return err
		}
		if v.Kind() == reflect.String {
			v.SetString(zeroCopyString(b))
		} else {
			v.SetBytes(b) // zero-copy: aliases the input buffer
		}
	default: // scalar
		if wt != fi.scalar.wireType() {
			return ErrWireType
		}
		if err := decodeScalar(r, fi.scalar, v); err != nil {
			return err
		}
	}
	return nil
}

// decodeMessage reads one length-delimited nested message and returns a
// value assignable to t (struct or *struct).
func decodeMessage(r *wire.Reader, fi *fieldInfo, t reflect.Type, depth int) (reflect.Value, error) {
	b, err := r.Bytes()
	if err != nil {
		return reflect.Value{}, err
	}
	if depth+1 > MaxDepth {
		return reflect.Value{}, ErrMaxDepth
	}
	isPtr := t.Kind() == reflect.Ptr
	st := t
	if isPtr {
		st = t.Elem()
	}
	nv := reflect.New(st)
	if err := unmarshalStruct(wire.NewReader(b), nv.Elem(), depth+1); err != nil {
		return reflect.Value{}, err
	}
	if isPtr {
		return nv, nil
	}
	return nv.Elem(), nil
}

func appendScalar(r *wire.Reader, slice reflect.Value, k scalarKind) error {
	elem := reflect.New(slice.Type().Elem()).Elem()
	if err := decodeScalar(r, k, elem); err != nil {
		return err
	}
	slice.Set(reflect.Append(slice, elem))
	return nil
}

func decodeScalar(r *wire.Reader, k scalarKind, v reflect.Value) error {
	switch k {
	case kVarint:
		u, err := r.Varint()
		if err != nil {
			return err
		}
		switch v.Kind() {
		case reflect.Bool:
			v.SetBool(u != 0)
		case reflect.Int32, reflect.Int64:
			v.SetInt(int64(u))
		default:
			v.SetUint(u)
		}
	case kZigzag32, kZigzag64:
		s, err := r.Zigzag()
		if err != nil {
			return err
		}
		v.SetInt(s)
	case kFixed32, kSFixed32:
		u, err := r.Fixed32()
		if err != nil {
			return err
		}
		setUintOrInt(v, uint64(u))
	case kFixed64, kSFixed64:
		u, err := r.Fixed64()
		if err != nil {
			return err
		}
		setUintOrInt(v, u)
	}
	return nil
}

func setUintOrInt(v reflect.Value, u uint64) {
	if v.Kind() == reflect.Int32 || v.Kind() == reflect.Int64 {
		v.SetInt(int64(u))
	} else {
		v.SetUint(u)
	}
}

// zeroCopyString views b as a string without copying. The input buffer must
// outlive the string and never be mutated.
func zeroCopyString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(&b[0], len(b))
}
