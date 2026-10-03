package wire

import (
	"math"
	"reflect"
)

// Marshal encodes a struct carrying `pb` tags into wire format. Repeated
// fields declared `packed` are emitted as a single length-delimited field;
// other repeated fields emit one tag per element.
func Marshal(v interface{}) ([]byte, error) {
	if err := Validate(v); err != nil {
		return nil, err
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	specs, err := compileSpecs(rv.Type())
	if err != nil {
		return nil, err
	}
	var out []byte
	for _, spec := range specs {
		fv := rv.Field(spec.index)
		if spec.packed {
			if fv.Len() == 0 {
				continue
			}
			var payload []byte
			for i := 0; i < fv.Len(); i++ {
				payload = appendScalar(payload, spec, fv.Index(i))
			}
			out = EncodeTag(out, spec.num, WireLengthDelimited)
			out = EncodeVarint(out, uint64(len(payload)))
			out = append(out, payload...)
			continue
		}
		if spec.repeated {
			for i := 0; i < fv.Len(); i++ {
				out = EncodeTag(out, spec.num, expectedWire(spec.kind))
				out = appendScalar(out, spec, fv.Index(i))
			}
			continue
		}
		if spec.kind == KindMessage && fv.Kind() == reflect.Ptr && fv.IsNil() {
			continue
		}
		out = EncodeTag(out, spec.num, expectedWire(spec.kind))
		out = appendScalar(out, spec, fv)
	}
	return out, nil
}

func appendScalar(out []byte, spec *fieldSpec, fv reflect.Value) []byte {
	switch spec.kind {
	case KindVarint:
		switch fv.Kind() {
		case reflect.Bool:
			if fv.Bool() {
				return EncodeVarint(out, 1)
			}
			return EncodeVarint(out, 0)
		case reflect.Int, reflect.Int32, reflect.Int64:
			return EncodeVarint(out, uint64(fv.Int()))
		default:
			return EncodeVarint(out, fv.Uint())
		}
	case KindSint:
		return EncodeVarint(out, ZigZagEncode64(fv.Int()))
	case KindFixed32:
		if fv.Kind() == reflect.Float32 {
			return EncodeFixed32(out, math.Float32bits(float32(fv.Float())))
		}
		return EncodeFixed32(out, uint32(fv.Uint()))
	case KindFixed64:
		if fv.Kind() == reflect.Float64 {
			return EncodeFixed64(out, math.Float64bits(fv.Float()))
		}
		return EncodeFixed64(out, fv.Uint())
	case KindString:
		out = EncodeVarint(out, uint64(len(fv.String())))
		return append(out, fv.String()...)
	case KindBytes:
		out = EncodeVarint(out, uint64(fv.Len()))
		return append(out, fv.Bytes()...)
	case KindMessage:
		inner := fv
		if fv.Kind() == reflect.Ptr {
			inner = fv.Elem()
		}
		sub, err := Marshal(inner.Addr().Interface())
		if err != nil {
			return out
		}
		out = EncodeVarint(out, uint64(len(sub)))
		return append(out, sub...)
	}
	return out
}
