package schema

import (
	"fmt"
	"reflect"

	"wirefmt/wire"
)

// Marshal encodes v (struct or pointer to struct) into the protobuf binary
// wire format. The type must pass Validate.
func Marshal(v interface{}) ([]byte, error) {
	info, err := Validate(v)
	if err != nil {
		return nil, err
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return nil, fmt.Errorf("schema: cannot marshal nil %v", rv.Type())
		}
		rv = rv.Elem()
	}
	return marshalMessage(info, rv)
}

func marshalMessage(info *MessageInfo, rv reflect.Value) ([]byte, error) {
	var buf []byte
	for _, fi := range info.Fields {
		fv := rv.Field(fi.Index)
		var err error
		buf, err = marshalField(buf, fi, fv)
		if err != nil {
			return nil, err
		}
	}
	return buf, nil
}

func marshalField(buf []byte, fi FieldInfo, fv reflect.Value) ([]byte, error) {
	if fi.Packed {
		return marshalPacked(buf, fi, fv)
	}
	if fi.Codec == CodecMessage {
		return marshalMessageField(buf, fi, fv)
	}
	buf = wire.AppendKey(buf, fi.Number, fi.WireTyp)
	switch fi.Codec {
	case CodecVarint:
		buf = wire.AppendUvarint(buf, varintOf(fv))
	case CodecZigZag:
		buf = wire.AppendUvarint(buf, zigzagOf(fv))
	case CodecFixed32:
		buf = wire.AppendFixed32(buf, uint32(fixedOf(fv)))
	case CodecFixed64:
		buf = wire.AppendFixed64(buf, fixedOf(fv))
	case CodecString:
		buf = wire.AppendString(buf, fv.String())
	case CodecBytes:
		buf = wire.AppendBytes(buf, fv.Bytes())
	}
	return buf, nil
}

func marshalMessageField(buf []byte, fi FieldInfo, fv reflect.Value) ([]byte, error) {
	if fv.Kind() == reflect.Slice {
		for i := 0; i < fv.Len(); i++ {
			el := fv.Index(i)
			if el.Kind() == reflect.Ptr {
				if el.IsNil() {
					return nil, fmt.Errorf("schema: nil element %d in repeated message field %d", i, fi.Number)
				}
				el = el.Elem()
			}
			sub, err := Validate(el.Interface())
			if err != nil {
				return nil, err
			}
			body, err := marshalMessage(sub, el)
			if err != nil {
				return nil, err
			}
			buf = wire.AppendKey(buf, fi.Number, wire.WireLengthPref)
			buf = wire.AppendBytes(buf, body)
		}
		return buf, nil
	}
	if fv.Kind() == reflect.Ptr {
		if fv.IsNil() {
			return buf, nil // absent optional message
		}
		fv = fv.Elem()
	}
	sub, err := Validate(fv.Interface())
	if err != nil {
		return nil, err
	}
	body, err := marshalMessage(sub, fv)
	if err != nil {
		return nil, err
	}
	buf = wire.AppendKey(buf, fi.Number, wire.WireLengthPref)
	return wire.AppendBytes(buf, body), nil
}

func marshalPacked(buf []byte, fi FieldInfo, fv reflect.Value) ([]byte, error) {
	if fv.Len() == 0 {
		return buf, nil
	}
	var body []byte
	for i := 0; i < fv.Len(); i++ {
		el := fv.Index(i)
		switch fi.Codec {
		case CodecVarint:
			body = wire.AppendUvarint(body, varintOf(el))
		case CodecZigZag:
			body = wire.AppendUvarint(body, zigzagOf(el))
		case CodecFixed32:
			body = wire.AppendFixed32(body, uint32(fixedOf(el)))
		case CodecFixed64:
			body = wire.AppendFixed64(body, fixedOf(el))
		default:
			return nil, fmt.Errorf("schema: field %d: codec %q cannot be packed", fi.Number, fi.Codec)
		}
	}
	buf = wire.AppendKey(buf, fi.Number, wire.WireLengthPref)
	return wire.AppendBytes(buf, body), nil
}

func varintOf(v reflect.Value) uint64 {
	switch v.Kind() {
	case reflect.Bool:
		if v.Bool() {
			return 1
		}
		return 0
	case reflect.Int32, reflect.Int64, reflect.Int:
		return uint64(v.Int()) // two's complement, 10 bytes when negative
	default:
		return v.Uint()
	}
}

func zigzagOf(v reflect.Value) uint64 {
	if v.Kind() == reflect.Int32 {
		return uint64(wire.EncodeZigZag32(int32(v.Int())))
	}
	return wire.EncodeZigZag64(v.Int())
}

func fixedOf(v reflect.Value) uint64 {
	switch v.Kind() {
	case reflect.Float32:
		return uint64(math32bits(float32(v.Float())))
	case reflect.Float64:
		return math64bits(v.Float())
	case reflect.Int32, reflect.Int64:
		return uint64(v.Int())
	default:
		return v.Uint()
	}
}

// Unmarshal decodes a protobuf wire stream into out, which must be a
// pointer to a validated struct. Unknown fields are skipped safely. Any
// truncation or type violation returns an error and leaves out partially
// populated only with fully-decoded fields.
func Unmarshal(data []byte, out interface{}) error {
	info, err := Validate(out)
	if err != nil {
		return err
	}
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return fmt.Errorf("schema: Unmarshal needs a non-nil pointer, got %T", out)
	}
	return unmarshalMessage(data, info, rv.Elem())
}

func unmarshalMessage(data []byte, info *MessageInfo, rv reflect.Value) error {
	pos := 0
	for pos < len(data) {
		f, next, done, err := wire.NextField(data, pos)
		if err != nil {
			return fmt.Errorf("schema: field at offset %d: %v", pos, err)
		}
		if done {
			break
		}
		fi, known := info.ByNumber(f.Number)
		if !known {
			pos = next // unknown field: already fully skipped by NextField
			continue
		}
		if err := assignField(fi, f, rv.Field(fi.Index)); err != nil {
			return fmt.Errorf("schema: field %d: %v", f.Number, err)
		}
		pos = next
	}
	return nil
}

func assignField(fi FieldInfo, f wire.Field, dst reflect.Value) error {
	if fi.Packed {
		if f.Type != wire.WireLengthPref {
			return fmt.Errorf("packed field arrived with wire type %d", f.Type)
		}
		return assignPacked(fi, f.Bytes, dst)
	}
	if f.Type != fi.WireTyp {
		return fmt.Errorf("wire type mismatch: got %d, want %d", f.Type, fi.WireTyp)
	}
	switch fi.Codec {
	case CodecVarint:
		setVarint(dst, f.Varint)
	case CodecZigZag:
		setZigZag(dst, f.Varint)
	case CodecFixed32:
		setFixed(dst, uint64(f.Fixed32))
	case CodecFixed64:
		setFixed(dst, f.Fixed64)
	case CodecString:
		dst.SetString(string(f.Bytes))
	case CodecBytes:
		if len(f.Bytes) == 0 {
			break // proto3 semantics: empty == absent, keep nil
		}
		b := make([]byte, len(f.Bytes))
		copy(b, f.Bytes)
		dst.SetBytes(b)
	case CodecMessage:
		return assignMessage(fi, f.Bytes, dst)
	}
	return nil
}

func assignMessage(fi FieldInfo, body []byte, dst reflect.Value) error {
	if dst.Kind() == reflect.Slice {
		et := dst.Type().Elem()
		isPtr := et.Kind() == reflect.Ptr
		if isPtr {
			et = et.Elem()
		}
		el := reflect.New(et)
		sub, err := Validate(el.Interface())
		if err != nil {
			return err
		}
		if err := unmarshalMessage(body, sub, el.Elem()); err != nil {
			return err
		}
		if isPtr {
			dst.Set(reflect.Append(dst, el))
		} else {
			dst.Set(reflect.Append(dst, el.Elem()))
		}
		return nil
	}
	target := dst
	if dst.Kind() == reflect.Ptr {
		if dst.IsNil() {
			dst.Set(reflect.New(dst.Type().Elem()))
		}
		target = dst.Elem()
	}
	sub, err := Validate(target.Interface())
	if err != nil {
		return err
	}
	return unmarshalMessage(body, sub, target)
}

func assignPacked(fi FieldInfo, body []byte, dst reflect.Value) error {
	pos := 0
	for pos < len(body) {
		el := reflect.New(dst.Type().Elem()).Elem()
		var err error
		switch fi.Codec {
		case CodecVarint:
			var v uint64
			v, pos, err = wire.ConsumeUvarint(body, pos)
			setVarint(el, v)
		case CodecZigZag:
			var v uint64
			v, pos, err = wire.ConsumeUvarint(body, pos)
			setZigZag(el, v)
		case CodecFixed32:
			var v uint32
			v, pos, err = wire.ConsumeFixed32(body, pos)
			setFixed(el, uint64(v))
		case CodecFixed64:
			var v uint64
			v, pos, err = wire.ConsumeFixed64(body, pos)
			setFixed(el, v)
		default:
			return fmt.Errorf("codec %q cannot be packed", fi.Codec)
		}
		if err != nil {
			return fmt.Errorf("packed element at offset %d: %v", pos, err)
		}
		dst.Set(reflect.Append(dst, el))
	}
	return nil
}

func setVarint(dst reflect.Value, v uint64) {
	switch dst.Kind() {
	case reflect.Bool:
		dst.SetBool(v != 0)
	case reflect.Int32, reflect.Int64, reflect.Int:
		dst.SetInt(int64(v))
	default:
		dst.SetUint(v)
	}
}

func setZigZag(dst reflect.Value, v uint64) {
	if dst.Kind() == reflect.Int32 {
		dst.SetInt(int64(wire.DecodeZigZag32(uint32(v))))
		return
	}
	dst.SetInt(wire.DecodeZigZag64(v))
}

func setFixed(dst reflect.Value, v uint64) {
	switch dst.Kind() {
	case reflect.Float32:
		dst.SetFloat(float64(math32frombits(uint32(v))))
	case reflect.Float64:
		dst.SetFloat(math64frombits(v))
	case reflect.Int32, reflect.Int64:
		dst.SetInt(int64(v))
	default:
		dst.SetUint(v)
	}
}
