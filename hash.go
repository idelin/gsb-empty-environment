package crush

import (
	"reflect"
)

// HashFunc is the common one-argument 64-bit integer hash signature.
type HashFunc func(uint64) uint64

// Hash64Func hashes an ordered tuple of integer seeds.
type Hash64Func func(parts ...uint64) uint64

// Hasher can be implemented by callers that already carry a hash state.
type Hasher interface {
	Hash64(parts ...uint64) uint64
}

// SplitMix64 is the built-in deterministic mixer used when no hash is supplied.
func SplitMix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	z := x
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// DefaultHash combines an ordered tuple with SplitMix64.
func DefaultHash(parts ...uint64) uint64 {
	h := uint64(0x6a09e667f3bcc909)
	for i, part := range parts {
		h = SplitMix64(h ^ part ^ SplitMix64(uint64(i)+0x517cc1b727220a95))
	}
	return SplitMix64(h ^ 0x9e3779b97f4a7c15)
}

func combineWithSingle(h func(uint64) uint64, parts ...uint64) uint64 {
	if h == nil {
		h = SplitMix64
	}
	v := uint64(0x6a09e667f3bcc909)
	for i, part := range parts {
		v = h(v ^ part ^ SplitMix64(uint64(i)+0x517cc1b727220a95))
	}
	return h(v ^ 0x9e3779b97f4a7c15)
}

func adaptHasher(hasher any) (Hash64Func, error) {
	switch h := hasher.(type) {
	case nil:
		return DefaultHash, nil
	case HashFunc:
		return func(parts ...uint64) uint64 { return combineWithSingle(h, parts...) }, nil
	case func(uint64) uint64:
		return func(parts ...uint64) uint64 { return combineWithSingle(h, parts...) }, nil
	case Hash64Func:
		return h, nil
	case func(...uint64) uint64:
		return Hash64Func(h), nil
	case Hasher:
		return h.Hash64, nil
	}

	if hasher == nil {
		return DefaultHash, nil
	}

	v := reflect.ValueOf(hasher)
	if v.Kind() != reflect.Func {
		return nil, &UnsupportedHashError{Kind: v.Kind().String()}
	}
	t := v.Type()
	if t.NumOut() != 1 || !isIntegerKind(t.Out(0).Kind()) {
		return nil, &UnsupportedHashError{Reason: "hash function must return one 64-bit integer value"}
	}

	if t.IsVariadic() && t.NumIn() == 1 && t.In(0).Kind() == reflect.Slice && t.In(0).Elem().Kind() == reflect.Uint64 {
		return func(parts ...uint64) uint64 {
			out := v.Call([]reflect.Value{reflect.ValueOf(parts)})
			return hashOutputUint(out[0])
		}, nil
	}

	if t.NumIn() == 1 && isIntegerKind(t.In(0).Kind()) {
		return func(parts ...uint64) uint64 {
			out := v.Call([]reflect.Value{reflect.ValueOf(uint64Arg(parts)).Convert(t.In(0))})
			return hashOutputUint(out[0])
		}, nil
	}

	if t.NumIn() > 0 && t.NumIn() <= 8 {
		for i := 0; i < t.NumIn(); i++ {
			if !isIntegerKind(t.In(i).Kind()) {
				return nil, &UnsupportedHashError{Reason: "fixed-argument hash inputs must be integers"}
			}
		}
		fnType := t
		return func(parts ...uint64) uint64 {
			args := make([]reflect.Value, fnType.NumIn())
			for i := 0; i < fnType.NumIn(); i++ {
				var value uint64
				switch {
				case i == 0 && len(parts) > 0:
					value = parts[i]
				case i < len(parts) && len(parts) == fnType.NumIn():
					value = parts[i]
				default:
					value = DefaultHash(append(parts, uint64(i))...)
				}
				args[i] = reflect.ValueOf(value).Convert(fnType.In(i))
			}
			out := v.Call(args)
			return hashOutputUint(out[0])
		}, nil
	}

	return nil, &UnsupportedHashError{Reason: "unsupported hash function signature"}
}

func hashOutputUint(v reflect.Value) uint64 {
	if isUintKind(v.Kind()) {
		return v.Uint()
	}
	return uint64(v.Int())
}

// UnsupportedHashError describes an unacceptable injected hash function.
type UnsupportedHashError struct {
	Kind   string
	Reason string
}

func (e *UnsupportedHashError) Error() string {
	if e.Reason != "" {
		return "crush: unsupported hash: " + e.Reason
	}
	return "crush: unsupported hash kind: " + e.Kind
}

func isIntegerKind(k reflect.Kind) bool {
	return k >= reflect.Int && k <= reflect.Uintptr
}

func isUintKind(k reflect.Kind) bool {
	return k >= reflect.Uint && k <= reflect.Uintptr
}

func uint64Arg(parts []uint64) uint64 {
	switch len(parts) {
	case 0:
		return 0
	case 1:
		return parts[0]
	default:
		return DefaultHash(parts...)
	}
}

func normalizePGID(id any) (uint64, error) {
	switch v := id.(type) {
	case nil:
		return 0, nil
	case uint64:
		return v, nil
	case uint:
		return uint64(v), nil
	case uint32:
		return uint64(v), nil
	case int:
		return uint64(v), nil
	case int64:
		return uint64(v), nil
	case int32:
		return uint64(v), nil
	case string:
		return DefaultHash([]uint64{SplitMix64(uint64(len(v))), hashStringBytes(v)}...), nil
	default:
		rv := reflect.ValueOf(id)
		switch rv.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return uint64(rv.Int()), nil
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			return rv.Uint(), nil
		case reflect.String:
			s := rv.String()
			return DefaultHash(SplitMix64(uint64(len(s))), hashStringBytes(s)), nil
		default:
			return 0, &InvalidPGIDError{ID: id}
		}
	}
}

func hashStringBytes(s string) uint64 {
	parts := []uint64{uint64(len(s))}
	for i := 0; i < len(s) && i < 16; i++ {
		parts = append(parts, uint64(s[i])<<uint(8*(i%8)))
	}
	return DefaultHash(parts...)
}

// InvalidPGIDError is returned for non-integer/non-string PG IDs.
type InvalidPGIDError struct {
	ID any
}

func (e *InvalidPGIDError) Error() string {
	return "crush: PG id must be an integer or string"
}
