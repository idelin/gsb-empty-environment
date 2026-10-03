package codec

import (
	"reflect"

	"pbcodec/wire"
)

// Marshal encodes msg (a struct or pointer to struct) into the protobuf
// binary wire format. Fields are emitted in field-number order, followed by
// any captured UnknownFields, so output is deterministic.
func Marshal(msg any) ([]byte, error) {
	sv, err := structValue(reflect.ValueOf(msg))
	if err != nil {
		return nil, err
	}
	w := wire.NewWriter()
	if err := marshalStruct(w, sv); err != nil {
		return nil, err
	}
	out := make([]byte, len(w.Out()))
	copy(out, w.Out())
	return out, nil
}

func structValue(v reflect.Value) (reflect.Value, error) {
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return v, ErrNotMessage
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return v, ErrNotMessage
	}
	return v, nil
}

func marshalStruct(w *wire.Writer, sv reflect.Value) error {
	si, err := cachedInfo(sv.Type())
	if err != nil {
		return err
	}
	for _, fi := range si.fields {
		if err := marshalField(w, sv.Field(fi.index), fi); err != nil {
			return err
		}
	}
	if si.unknownIdx >= 0 {
		raw := sv.Field(si.unknownIdx).FieldByName("Raw")
		if raw.IsValid() {
			w.BytesRaw(raw.Bytes())
		}
	}
	return nil
}

func marshalField(w *wire.Writer, v reflect.Value, fi *fieldInfo) error {
	switch {
	case fi.isSlice && fi.isMsg:
		for i := 0; i < v.Len(); i++ {
			if err := marshalMessage(w, v.Index(i), fi.num); err != nil {
				return err
			}
		}
	case fi.isSlice && fi.isBytes: // []string
		for i := 0; i < v.Len(); i++ {
			w.Tag(fi.num, wire.WireBytes)
			w.Bytes([]byte(v.Index(i).String()))
		}
	case fi.isSlice: // repeated scalar
		if v.Len() == 0 {
			return nil
		}
		if fi.packed {
			w.Tag(fi.num, wire.WireBytes)
			inner := wire.NewWriter()
			for i := 0; i < v.Len(); i++ {
				encodeScalar(inner, fi.scalar, v.Index(i))
			}
			w.Bytes(inner.Out())
		} else {
			for i := 0; i < v.Len(); i++ {
				w.Tag(fi.num, fi.scalar.wireType())
				encodeScalar(w, fi.scalar, v.Index(i))
			}
		}
	case fi.isMsg:
		if v.Kind() == reflect.Ptr && v.IsNil() {
			return nil
		}
		if err := marshalMessage(w, v, fi.num); err != nil {
			return err
		}
	case fi.isBytes:
		if v.IsZero() {
			return nil // proto3-style: omit empty string/[]byte
		}
		w.Tag(fi.num, wire.WireBytes)
		if v.Kind() == reflect.String {
			w.Bytes([]byte(v.String()))
		} else {
			w.Bytes(v.Bytes())
		}
	default: // scalar
		if v.IsZero() {
			return nil // proto3-style: omit zero scalars
		}
		w.Tag(fi.num, fi.scalar.wireType())
		encodeScalar(w, fi.scalar, v)
	}
	return nil
}

func marshalMessage(w *wire.Writer, v reflect.Value, num int) error {
	sv, err := structValue(v)
	if err != nil {
		return err
	}
	inner := wire.NewWriter()
	if err := marshalStruct(inner, sv); err != nil {
		return err
	}
	w.Tag(num, wire.WireBytes)
	w.Bytes(inner.Out())
	return nil
}

func encodeScalar(w *wire.Writer, k scalarKind, v reflect.Value) {
	switch k {
	case kVarint:
		w.Varint(scalarUint(v))
	case kZigzag32, kZigzag64:
		w.Zigzag(v.Int())
	case kFixed32, kSFixed32:
		w.Fixed32(uint32(scalarUint(v)))
	case kFixed64, kSFixed64:
		w.Fixed64(scalarUint(v))
	}
}

// scalarUint normalizes a bool/int/uint reflect.Value to its raw uint64 bits.
func scalarUint(v reflect.Value) uint64 {
	switch v.Kind() {
	case reflect.Bool:
		if v.Bool() {
			return 1
		}
		return 0
	case reflect.Int32, reflect.Int64:
		return uint64(v.Int())
	default:
		return v.Uint()
	}
}
