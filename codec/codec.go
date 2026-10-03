// Package codec is a reflection-driven validation engine that marshals and
// unmarshals Go structs to/from the Protocol Buffers binary wire format
// using only the standard library.
//
// Struct fields are declared with a `pb:"<num>[,opts]"` tag. Supported Go
// field types:
//
//	bool, int32, int64, uint32, uint64   varint
//	codec.SInt32, codec.SInt64           zigzag varint
//	codec.Fixed32, codec.SFixed32        fixed32
//	codec.Fixed64, codec.SFixed64        fixed64
//	string, []byte                       length-delimited (zero-copy on decode)
//	struct / *struct                     nested length-delimited message
//	[]scalar                             repeated; packed unless ",rep" is set
//	[]*struct                            repeated nested messages
//
// Options: "packed" forces packed encoding, "rep" forces one-tag-per-element
// encoding for repeated scalars. Decoding accepts both forms regardless.
//
// Forward compatibility: embed codec.UnknownFields in a struct to capture
// and re-emit fields the schema does not know. Backward compatibility:
// unknown fields in the stream are always skipped safely.
//
// Unmarshal is zero-copy for string and []byte fields: they alias the input
// buffer, which must therefore stay alive and unmutated while decoded values
// are in use. All functions are safe for concurrent use.
package codec

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"pbcodec/wire"
)

// Named scalar types selecting non-default wire encodings.
type (
	SInt32   int32 // zigzag varint
	SInt64   int64 // zigzag varint
	Fixed32  uint32
	Fixed64  uint64
	SFixed32 int32
	SFixed64 int64
)

// MaxDepth bounds nested-message recursion so adversarial input cannot
// exhaust the stack.
const MaxDepth = 100

var (
	// ErrMaxDepth is returned when nesting exceeds MaxDepth.
	ErrMaxDepth = errors.New("codec: message nesting too deep")
	// ErrWireType is returned when a field's wire type does not match its schema.
	ErrWireType = errors.New("codec: wire type mismatch")
	// ErrBadType is returned for unsupported Go field types.
	ErrBadType = errors.New("codec: unsupported field type")
	// ErrNotMessage is returned when the target is not a struct or *struct.
	ErrNotMessage = errors.New("codec: target must be a struct or pointer to struct")
)

// UnknownFields stores the raw wire encoding of fields not present in the
// schema. Embed it to make decode->encode round trips lossless across
// schema versions (forward compatibility).
type UnknownFields struct {
	Raw []byte
}

var (
	typeSInt32   = reflect.TypeOf(SInt32(0))
	typeSInt64   = reflect.TypeOf(SInt64(0))
	typeFixed32  = reflect.TypeOf(Fixed32(0))
	typeFixed64  = reflect.TypeOf(Fixed64(0))
	typeSFixed32 = reflect.TypeOf(SFixed32(0))
	typeSFixed64 = reflect.TypeOf(SFixed64(0))
	typeUnknown  = reflect.TypeOf(UnknownFields{})
)

// scalarKind enumerates the concrete scalar encodings.
type scalarKind int

const (
	kVarint scalarKind = iota // bool, int32/64, uint32/64
	kZigzag32
	kZigzag64
	kFixed32
	kSFixed32
	kFixed64
	kSFixed64
)

// wireType returns the natural wire type of a scalar kind.
func (k scalarKind) wireType() int {
	switch k {
	case kFixed32, kSFixed32:
		return wire.WireFixed32
	case kFixed64, kSFixed64:
		return wire.WireFixed64
	default:
		return wire.WireVarint
	}
}

type fieldInfo struct {
	index   int
	num     int
	scalar  scalarKind // valid when isScalar or slice of scalars
	isSlice bool
	isMsg   bool // struct or *struct (or slice thereof)
	isBytes bool // string or []byte
	packed  bool
}

type structInfo struct {
	fields     []*fieldInfo // sorted by field number
	byNum      map[int]*fieldInfo
	unknownIdx int // index of embedded UnknownFields, -1 if absent
}

var cache sync.Map // reflect.Type -> *structInfo

func cachedInfo(t reflect.Type) (*structInfo, error) {
	if v, ok := cache.Load(t); ok {
		return v.(*structInfo), nil
	}
	si, err := buildInfo(t)
	if err != nil {
		return nil, err
	}
	actual, _ := cache.LoadOrStore(t, si)
	return actual.(*structInfo), nil
}

func buildInfo(t reflect.Type) (*structInfo, error) {
	si := &structInfo{byNum: make(map[int]*fieldInfo), unknownIdx: -1}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous && f.Type == typeUnknown {
			si.unknownIdx = i
			continue
		}
		tag, ok := f.Tag.Lookup("pb")
		if !ok || tag == "-" {
			continue
		}
		num, opts, err := parseTag(tag)
		if err != nil {
			return nil, fmt.Errorf("codec: field %s: %w", f.Name, err)
		}
		if num <= 0 || num > wire.MaxFieldNumber {
			return nil, fmt.Errorf("codec: field %s: %w", f.Name, wire.ErrBadFieldNumber)
		}
		fi := &fieldInfo{index: i, num: num}
		if err := classify(f.Type, fi); err != nil {
			return nil, fmt.Errorf("codec: field %s: %w", f.Name, err)
		}
		if fi.isSlice && !fi.isMsg && !fi.isBytes {
			fi.packed = !opts["rep"]
			if opts["packed"] {
				fi.packed = true
			}
		}
		if si.byNum[num] != nil {
			return nil, fmt.Errorf("codec: duplicate field number %d", num)
		}
		si.byNum[num] = fi
		si.fields = append(si.fields, fi)
	}
	// Fields are emitted in field-number order for deterministic output.
	for i := 1; i < len(si.fields); i++ {
		for j := i; j > 0 && si.fields[j-1].num > si.fields[j].num; j-- {
			si.fields[j-1], si.fields[j] = si.fields[j], si.fields[j-1]
		}
	}
	return si, nil
}

func parseTag(tag string) (int, map[string]bool, error) {
	opts := make(map[string]bool)
	parts := strings.Split(tag, ",")
	num, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, nil, fmt.Errorf("bad tag %q", tag)
	}
	for _, p := range parts[1:] {
		if p != "" {
			opts[p] = true
		}
	}
	return num, opts, nil
}

// classify fills fi based on the Go type of the field.
func classify(t reflect.Type, fi *fieldInfo) error {
	switch t {
	case typeSInt32:
		fi.scalar = kZigzag32
		return nil
	case typeSInt64:
		fi.scalar = kZigzag64
		return nil
	case typeFixed32:
		fi.scalar = kFixed32
		return nil
	case typeFixed64:
		fi.scalar = kFixed64
		return nil
	case typeSFixed32:
		fi.scalar = kSFixed32
		return nil
	case typeSFixed64:
		fi.scalar = kSFixed64
		return nil
	}
	switch t.Kind() {
	case reflect.Bool, reflect.Int32, reflect.Int64,
		reflect.Uint32, reflect.Uint64:
		fi.scalar = kVarint
		return nil
	case reflect.String:
		fi.isBytes = true
		return nil
	case reflect.Struct:
		fi.isMsg = true
		return nil
	case reflect.Ptr:
		if t.Elem().Kind() == reflect.Struct {
			fi.isMsg = true
			return nil
		}
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			fi.isBytes = true // []byte
			return nil
		}
		return classifySlice(t.Elem(), fi)
	}
	return ErrBadType
}

func classifySlice(e reflect.Type, fi *fieldInfo) error {
	fi.isSlice = true
	switch e {
	case typeSInt32:
		fi.scalar = kZigzag32
		return nil
	case typeSInt64:
		fi.scalar = kZigzag64
		return nil
	case typeFixed32:
		fi.scalar = kFixed32
		return nil
	case typeFixed64:
		fi.scalar = kFixed64
		return nil
	case typeSFixed32:
		fi.scalar = kSFixed32
		return nil
	case typeSFixed64:
		fi.scalar = kSFixed64
		return nil
	}
	switch e.Kind() {
	case reflect.Bool, reflect.Int32, reflect.Int64,
		reflect.Uint32, reflect.Uint64:
		fi.scalar = kVarint
		return nil
	case reflect.String:
		fi.isBytes = true
		return nil
	case reflect.Ptr:
		if e.Elem().Kind() == reflect.Struct {
			fi.isMsg = true
			return nil
		}
	case reflect.Struct:
		fi.isMsg = true
		return nil
	}
	return ErrBadType
}
