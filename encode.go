package graphser

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
)

// Marshal serializes the object graph rooted at v into the graphser wire
// format. Shared pointers and maps are encoded once as objects with stable
// IDs and referenced elsewhere; cycles are handled. Unsupported types
// (interfaces, chans, funcs, ...) produce an error.
func Marshal(v any) ([]byte, error) {
	if v == nil {
		return nil, fmt.Errorf("graphser: cannot marshal nil")
	}
	enc := &encoder{ids: make(map[ident]uint64)}
	root, err := enc.value(reflect.ValueOf(v), 0)
	if err != nil {
		return nil, err
	}
	sort.Slice(enc.objects, func(i, j int) bool { return enc.objects[i].ID < enc.objects[j].ID })
	doc := Document{Version: Version, Root: root, Objects: enc.objects}
	var buf bytes.Buffer
	w := json.NewEncoder(&buf)
	w.SetIndent("", "  ")
	if err := w.Encode(doc); err != nil {
		return nil, fmt.Errorf("graphser: %v", err)
	}
	return buf.Bytes(), nil
}

// ident keys the identity table: same dynamic type, same address.
type ident struct {
	typ reflect.Type
	ptr uintptr
}

type encoder struct {
	ids     map[ident]uint64
	objects []Object
	next    uint64
}

var nullValue = Value{Kind: KindNull}

func (e *encoder) value(rv reflect.Value, depth int) (Value, error) {
	if depth > maxDepth {
		return Value{}, fmt.Errorf("graphser: value graph exceeds max depth %d", maxDepth)
	}
	switch rv.Kind() {
	case reflect.Bool:
		return Value{Kind: KindBool, Bool: rv.Bool()}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return Value{Kind: KindInt, Int: rv.Int()}, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return Value{Kind: KindUint, Uint: rv.Uint()}, nil
	case reflect.Float32, reflect.Float64:
		return Value{Kind: KindFloat, Float: rv.Float()}, nil
	case reflect.String:
		return Value{Kind: KindString, Str: rv.String()}, nil
	case reflect.Ptr, reflect.Map:
		if rv.IsNil() {
			return nullValue, nil
		}
		return e.reference(rv, depth)
	case reflect.Slice:
		if rv.IsNil() {
			return nullValue, nil
		}
		elems, err := e.valueList(rv, depth)
		if err != nil {
			return Value{}, err
		}
		return Value{Kind: KindSlice, Type: rv.Type().String(), Elems: elems}, nil
	case reflect.Array:
		elems, err := e.valueList(rv, depth)
		if err != nil {
			return Value{}, err
		}
		return Value{Kind: KindArray, Type: rv.Type().String(), Elems: elems}, nil
	case reflect.Struct:
		return e.structValue(rv, depth)
	default:
		return Value{}, fmt.Errorf("graphser: unsupported type %s (kind %s)", rv.Type(), rv.Kind())
	}
}

// reference encodes a non-nil pointer or map as an object with a stable ID,
// registering the ID before encoding the body so cycles back to it resolve.
func (e *encoder) reference(rv reflect.Value, depth int) (Value, error) {
	key := ident{typ: rv.Type(), ptr: rv.Pointer()}
	if id, ok := e.ids[key]; ok {
		return Value{Kind: KindRef, Ref: id}, nil
	}
	e.next++
	id := e.next
	e.ids[key] = id
	var body Value
	var typ string
	var err error
	if rv.Kind() == reflect.Ptr {
		typ = rv.Type().Elem().String()
		body, err = e.value(rv.Elem(), depth+1)
	} else {
		typ = rv.Type().String()
		body, err = e.mapValue(rv, depth+1)
	}
	if err != nil {
		return Value{}, err
	}
	e.objects = append(e.objects, Object{ID: id, Type: typ, Value: body})
	return Value{Kind: KindRef, Ref: id}, nil
}

func (e *encoder) structValue(rv reflect.Value, depth int) (Value, error) {
	t := rv.Type()
	fields := make([]Field, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.PkgPath != "" { // unexported
			continue
		}
		fv, err := e.value(rv.Field(i), depth+1)
		if err != nil {
			return Value{}, err
		}
		fields = append(fields, Field{Name: sf.Name, Value: fv})
	}
	return Value{Kind: KindStruct, Type: t.String(), Fields: fields}, nil
}

func (e *encoder) valueList(rv reflect.Value, depth int) ([]Value, error) {
	n := rv.Len()
	elems := make([]Value, 0, n)
	for i := 0; i < n; i++ {
		ev, err := e.value(rv.Index(i), depth+1)
		if err != nil {
			return nil, err
		}
		elems = append(elems, ev)
	}
	return elems, nil
}

func (e *encoder) mapValue(rv reflect.Value, depth int) (Value, error) {
	kt := rv.Type().Key()
	switch kt.Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64, reflect.String:
	default:
		return Value{}, fmt.Errorf("graphser: unsupported map key type %s", kt)
	}
	keys := rv.MapKeys()
	sort.Slice(keys, func(i, j int) bool { return lessScalar(keys[i], keys[j]) })
	entries := make([]Entry, 0, len(keys))
	for _, k := range keys {
		kv, err := e.value(k, depth+1)
		if err != nil {
			return Value{}, err
		}
		vv, err := e.value(rv.MapIndex(k), depth+1)
		if err != nil {
			return Value{}, err
		}
		entries = append(entries, Entry{Key: kv, Val: vv})
	}
	return Value{Kind: KindMap, Type: rv.Type().String(), Entries: entries}, nil
}

// lessScalar orders map keys deterministically so IDs and output bytes are
// stable. Both values have the same scalar kind.
func lessScalar(a, b reflect.Value) bool {
	switch a.Kind() {
	case reflect.Bool:
		return !a.Bool() && b.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return a.Int() < b.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return a.Uint() < b.Uint()
	case reflect.Float32, reflect.Float64:
		return a.Float() < b.Float()
	default:
		return a.String() < b.String()
	}
}
