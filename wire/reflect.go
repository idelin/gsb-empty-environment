package wire

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Reflection-mode validation engine.
//
// Struct fields are bound to protobuf fields with tags:
//
//	type Msg struct {
//	    ID     uint64   `pb:"1,varint"`
//	    Delta  int64    `pb:"2,zigzag"`
//	    Flag   bool     `pb:"3,varint"`
//	    Ratio  uint32   `pb:"4,fixed32"`
//	    Stamp  uint64   `pb:"5,fixed64"`
//	    Name   string   `pb:"6,string"`
//	    Blob   []byte   `pb:"7,bytes"`
//	    Inner  *Inner   `pb:"8,message"`   // nested length-delimited
//	    Scores []int64  `pb:"9,packed"`    // packed repeated zigzag
//	    Tags   []string `pb:"10,string"`   // repeated (one tag per element)
//	}
//
// Unmarshal validates, for every field on the wire, that the wire type
// matches the kind declared in the struct tag; unknown fields are skipped.

type kind int

const (
	kindVarint kind = iota
	kindZigZag
	kindFixed32
	kindFixed64
	kindString
	kindBytes
	kindMessage
	kindPacked
)

type fieldMeta struct {
	num   int
	kind  kind
	index int
	name  string
}

// wantWire reports the wire type a kind expects on the wire.
func (k kind) wantWire() Type {
	switch k {
	case kindVarint, kindZigZag:
		return Varint
	case kindFixed32:
		return Fixed32
	case kindFixed64:
		return Fixed64
	default: // string, bytes, message, packed
		return Bytes
	}
}

func parseKind(s string) (kind, error) {
	switch s {
	case "varint":
		return kindVarint, nil
	case "zigzag":
		return kindZigZag, nil
	case "fixed32":
		return kindFixed32, nil
	case "fixed64":
		return kindFixed64, nil
	case "string":
		return kindString, nil
	case "bytes":
		return kindBytes, nil
	case "message":
		return kindMessage, nil
	case "packed":
		return kindPacked, nil
	default:
		return 0, fmt.Errorf("wire: unknown pb kind %q", s)
	}
}

// fieldMap indexes a struct type's pb-tagged fields by field number.
func fieldMap(t reflect.Type) (map[int]fieldMeta, error) {
	m := make(map[int]fieldMeta)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag, ok := f.Tag.Lookup("pb")
		if !ok {
			continue
		}
		parts := strings.Split(tag, ",")
		if len(parts) != 2 {
			return nil, fmt.Errorf("wire: %s.%s: malformed pb tag %q", t.Name(), f.Name, tag)
		}
		num, err := strconv.Atoi(parts[0])
		if err != nil || num <= 0 {
			return nil, fmt.Errorf("wire: %s.%s: bad field number %q", t.Name(), f.Name, parts[0])
		}
		k, err := parseKind(parts[1])
		if err != nil {
			return nil, fmt.Errorf("wire: %s.%s: %w", t.Name(), f.Name, err)
		}
		if _, dup := m[num]; dup {
			return nil, fmt.Errorf("wire: %s.%s: duplicate field number %d", t.Name(), f.Name, num)
		}
		m[num] = fieldMeta{num: num, kind: k, index: i, name: f.Name}
	}
	return m, nil
}

// Unmarshal decodes a protobuf wire-format message into out, which must be
// a non-nil pointer to a struct with `pb` field tags. Unknown fields are
// skipped; wire-type mismatches and truncations are reported as errors.
func Unmarshal(data []byte, out any) error {
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Ptr || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("wire: Unmarshal target must be a non-nil pointer to struct, got %T", out)
	}
	return unmarshalMessage(data, rv.Elem())
}

func unmarshalMessage(data []byte, sv reflect.Value) error {
	fm, err := fieldMap(sv.Type())
	if err != nil {
		return err
	}
	d := NewDecoder(data)
	for !d.EOF() {
		num, wt, err := d.Tag()
		if err != nil {
			return err
		}
		meta, ok := fm[num]
		if !ok {
			if err := d.Skip(wt); err != nil {
				return fmt.Errorf("wire: skipping unknown field %d: %w", num, err)
			}
			continue
		}
		if err := meta.validate(wt); err != nil {
			return fmt.Errorf("wire: field %d (%s): %w", num, meta.name, err)
		}
		if err := meta.decode(d, sv.Field(meta.index)); err != nil {
			return fmt.Errorf("wire: field %d (%s): %w", num, meta.name, err)
		}
	}
	return nil
}

// validate checks the wire type against the declared kind.
func (m fieldMeta) validate(wt Type) error {
	if wt != m.kind.wantWire() {
		return fmt.Errorf("%w: got %s, want %s", ErrBadWireType, wt, m.kind.wantWire())
	}
	return nil
}

func (m fieldMeta) decode(d *Decoder, fv reflect.Value) error {
	switch m.kind {
	case kindVarint:
		v, err := d.Varint()
		if err != nil {
			return err
		}
		return setVarint(fv, v)
	case kindZigZag:
		v, err := d.ZigZag64()
		if err != nil {
			return err
		}
		return setSigned(fv, v)
	case kindFixed32:
		v, err := d.Fixed32()
		if err != nil {
			return err
		}
		return setFixed32(fv, v)
	case kindFixed64:
		v, err := d.Fixed64()
		if err != nil {
			return err
		}
		return setFixed64(fv, v)
	case kindString:
		s, err := d.String()
		if err != nil {
			return err
		}
		if fv.Kind() == reflect.Slice { // repeated string
			fv.Set(reflect.Append(fv, reflect.ValueOf(s)))
			return nil
		}
		fv.SetString(s)
		return nil
	case kindBytes:
		p, err := d.Bytes()
		if err != nil {
			return err
		}
		fv.SetBytes(append(fv.Bytes()[:0:0], p...))
		return nil
	case kindMessage:
		p, err := d.Bytes()
		if err != nil {
			return err
		}
		return setMessage(fv, p)
	case kindPacked:
		return decodePacked(d, fv)
	default:
		return fmt.Errorf("wire: unhandled kind %d", int(m.kind))
	}
}

func setVarint(fv reflect.Value, v uint64) error {
	switch fv.Kind() {
	case reflect.Uint64, reflect.Uint32, reflect.Uint:
		if fv.OverflowUint(v) {
			return fmt.Errorf("wire: value %d overflows %s", v, fv.Type())
		}
		fv.SetUint(v)
	case reflect.Int64, reflect.Int32, reflect.Int:
		if fv.OverflowInt(int64(v)) {
			return fmt.Errorf("wire: value %d overflows %s", v, fv.Type())
		}
		fv.SetInt(int64(v))
	case reflect.Bool:
		fv.SetBool(v != 0)
	default:
		return fmt.Errorf("wire: cannot assign varint to %s", fv.Type())
	}
	return nil
}

func setSigned(fv reflect.Value, v int64) error {
	if fv.Kind() != reflect.Int64 && fv.Kind() != reflect.Int32 && fv.Kind() != reflect.Int {
		return fmt.Errorf("wire: cannot assign zigzag to %s", fv.Type())
	}
	if fv.OverflowInt(v) {
		return fmt.Errorf("wire: value %d overflows %s", v, fv.Type())
	}
	fv.SetInt(v)
	return nil
}

func setFixed32(fv reflect.Value, v uint32) error {
	if fv.Kind() != reflect.Uint32 && fv.Kind() != reflect.Int32 {
		return fmt.Errorf("wire: cannot assign fixed32 to %s", fv.Type())
	}
	if fv.Kind() == reflect.Uint32 {
		fv.SetUint(uint64(v))
	} else {
		fv.SetInt(int64(int32(v)))
	}
	return nil
}

func setFixed64(fv reflect.Value, v uint64) error {
	if fv.Kind() != reflect.Uint64 && fv.Kind() != reflect.Int64 {
		return fmt.Errorf("wire: cannot assign fixed64 to %s", fv.Type())
	}
	if fv.Kind() == reflect.Uint64 {
		fv.SetUint(v)
	} else {
		fv.SetInt(int64(v))
	}
	return nil
}

func setMessage(fv reflect.Value, p []byte) error {
	switch fv.Kind() {
	case reflect.Ptr:
		if fv.Type().Elem().Kind() != reflect.Struct {
			return fmt.Errorf("wire: message target must be *struct, got %s", fv.Type())
		}
		nv := reflect.New(fv.Type().Elem())
		if err := unmarshalMessage(p, nv.Elem()); err != nil {
			return err
		}
		fv.Set(nv)
		return nil
	case reflect.Struct:
		return unmarshalMessage(p, fv)
	case reflect.Slice: // repeated message
		et := fv.Type().Elem()
		if et.Kind() == reflect.Ptr {
			nv := reflect.New(et.Elem())
			if err := unmarshalMessage(p, nv.Elem()); err != nil {
				return err
			}
			fv.Set(reflect.Append(fv, nv))
			return nil
		}
		nv := reflect.New(et)
		if err := unmarshalMessage(p, nv.Elem()); err != nil {
			return err
		}
		fv.Set(reflect.Append(fv, nv.Elem()))
		return nil
	default:
		return fmt.Errorf("wire: cannot assign message to %s", fv.Type())
	}
}

// decodePacked decodes one length-delimited packed block into a numeric slice.
func decodePacked(d *Decoder, fv reflect.Value) error {
	if fv.Kind() != reflect.Slice {
		return fmt.Errorf("wire: packed target must be a slice, got %s", fv.Type())
	}
	p, err := d.Bytes()
	if err != nil {
		return err
	}
	sub := NewDecoder(p)
	for !sub.EOF() {
		v, err := sub.Varint()
		if err != nil {
			return err
		}
		elem := reflect.New(fv.Type().Elem()).Elem()
		switch elem.Kind() {
		case reflect.Int64, reflect.Int32, reflect.Int:
			s := DecodeZigZag64(v)
			if elem.OverflowInt(s) {
				return fmt.Errorf("wire: packed value %d overflows %s", s, elem.Type())
			}
			elem.SetInt(s)
		case reflect.Uint64, reflect.Uint32, reflect.Uint:
			if elem.OverflowUint(v) {
				return fmt.Errorf("wire: packed value %d overflows %s", v, elem.Type())
			}
			elem.SetUint(v)
		default:
			return fmt.Errorf("wire: packed element type %s unsupported", elem.Type())
		}
		fv.Set(reflect.Append(fv, elem))
	}
	return nil
}

// Marshal encodes a struct with `pb` field tags into protobuf wire format.
// Zero-valued scalar fields are omitted (proto3 semantics); slices that are
// empty are omitted as well.
func Marshal(in any) ([]byte, error) {
	rv := reflect.ValueOf(in)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return nil, fmt.Errorf("wire: cannot marshal nil %T", in)
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, fmt.Errorf("wire: Marshal source must be a struct, got %T", in)
	}
	fm, err := fieldMap(rv.Type())
	if err != nil {
		return nil, err
	}
	var b []byte
	for i := 0; i < rv.Type().NumField(); i++ {
		var meta fieldMeta
		found := false
		for _, m := range fm {
			if m.index == i {
				meta = m
				found = true
				break
			}
		}
		if !found {
			continue
		}
		b, err = meta.encode(b, rv.Field(i))
		if err != nil {
			return nil, fmt.Errorf("wire: field %d (%s): %w", meta.num, meta.name, err)
		}
	}
	return b, nil
}

func (m fieldMeta) encode(b []byte, fv reflect.Value) ([]byte, error) {
	switch m.kind {
	case kindVarint:
		switch fv.Kind() {
		case reflect.Bool:
			if !fv.Bool() {
				return b, nil
			}
			b = AppendTag(b, m.num, Varint)
			return AppendVarint(b, 1), nil
		case reflect.Uint64, reflect.Uint32, reflect.Uint:
			if fv.Uint() == 0 {
				return b, nil
			}
			b = AppendTag(b, m.num, Varint)
			return AppendVarint(b, fv.Uint()), nil
		case reflect.Int64, reflect.Int32, reflect.Int:
			if fv.Int() == 0 {
				return b, nil
			}
			b = AppendTag(b, m.num, Varint)
			return AppendVarint(b, uint64(fv.Int())), nil
		}
		return nil, fmt.Errorf("wire: cannot marshal %s as varint", fv.Type())
	case kindZigZag:
		if fv.Int() == 0 {
			return b, nil
		}
		b = AppendTag(b, m.num, Varint)
		return AppendVarint(b, EncodeZigZag64(fv.Int())), nil
	case kindFixed32:
		var v uint32
		switch fv.Kind() {
		case reflect.Uint32:
			v = uint32(fv.Uint())
		case reflect.Int32:
			v = uint32(int32(fv.Int()))
		default:
			return nil, fmt.Errorf("wire: cannot marshal %s as fixed32", fv.Type())
		}
		if v == 0 {
			return b, nil
		}
		b = AppendTag(b, m.num, Fixed32)
		return AppendFixed32(b, v), nil
	case kindFixed64:
		var v uint64
		switch fv.Kind() {
		case reflect.Uint64:
			v = fv.Uint()
		case reflect.Int64:
			v = uint64(fv.Int())
		default:
			return nil, fmt.Errorf("wire: cannot marshal %s as fixed64", fv.Type())
		}
		if v == 0 {
			return b, nil
		}
		b = AppendTag(b, m.num, Fixed64)
		return AppendFixed64(b, v), nil
	case kindString:
		if fv.Kind() == reflect.Slice { // repeated string
			for i := 0; i < fv.Len(); i++ {
				b = AppendTag(b, m.num, Bytes)
				b = AppendString(b, fv.Index(i).String())
			}
			return b, nil
		}
		if fv.String() == "" {
			return b, nil
		}
		b = AppendTag(b, m.num, Bytes)
		return AppendString(b, fv.String()), nil
	case kindBytes:
		if fv.Len() == 0 {
			return b, nil
		}
		b = AppendTag(b, m.num, Bytes)
		return AppendBytes(b, fv.Bytes()), nil
	case kindMessage:
		return m.encodeMessage(b, fv)
	case kindPacked:
		if fv.Len() == 0 {
			return b, nil
		}
		var p []byte
		for i := 0; i < fv.Len(); i++ {
			e := fv.Index(i)
			switch e.Kind() {
			case reflect.Int64, reflect.Int32, reflect.Int:
				p = AppendVarint(p, EncodeZigZag64(e.Int()))
			default:
				p = AppendVarint(p, e.Uint())
			}
		}
		b = AppendTag(b, m.num, Bytes)
		return AppendBytes(b, p), nil
	default:
		return nil, fmt.Errorf("wire: unhandled kind %d", int(m.kind))
	}
}

func (m fieldMeta) encodeMessage(b []byte, fv reflect.Value) ([]byte, error) {
	switch fv.Kind() {
	case reflect.Ptr:
		if fv.IsNil() {
			return b, nil
		}
		sub, err := Marshal(fv.Interface())
		if err != nil {
			return nil, err
		}
		b = AppendTag(b, m.num, Bytes)
		return AppendBytes(b, sub), nil
	case reflect.Struct:
		nv := reflect.New(fv.Type())
		nv.Elem().Set(fv)
		sub, err := Marshal(nv.Interface())
		if err != nil {
			return nil, err
		}
		b = AppendTag(b, m.num, Bytes)
		return AppendBytes(b, sub), nil
	case reflect.Slice: // repeated message
		for i := 0; i < fv.Len(); i++ {
			e := fv.Index(i)
			if e.Kind() != reflect.Ptr {
				e = e.Addr()
			}
			sub, err := Marshal(e.Interface())
			if err != nil {
				return nil, err
			}
			b = AppendTag(b, m.num, Bytes)
			b = AppendBytes(b, sub)
		}
		return b, nil
	default:
		return nil, fmt.Errorf("wire: cannot marshal %s as message", fv.Type())
	}
}
