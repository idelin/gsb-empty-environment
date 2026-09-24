// Package canonkey canonicalizes structured requests into unambiguous,
// order-independent keys that embed tenant, parameter types, and the
// config version that affects results.
//
// Explicitly supported scope (anything else is rejected, never guessed):
//
//   - Values are JSON-shaped: nil/null, bool, string, json.Number,
//     []any, map[string]any, plus exact Go numeric scalars.
//   - Reference semantics: value semantics only. Shared (non-cyclic)
//     substructures are expanded by value; cyclic structures are
//     rejected with ErrCycle. Different reference semantics are never
//     encoded into the same key because only value semantics exist here.
//   - Unicode strategy: byte-exact UTF-8. Strings are encoded as raw
//     UTF-8 bytes with a length prefix; invalid UTF-8 is rejected with
//     ErrInvalidUTF8. No normalization is applied, so two canonically
//     distinct Unicode spellings (e.g. composed vs. decomposed) yield
//     distinct keys. This policy is deterministic and verifiable.
//   - Number strategy: exact decimal canonical form. Numbers are
//     normalized to a unique scientific decimal ("<digits>e<exp>") with
//     no precision loss; 1, 1.0 and 1e0 are the same number, while the
//     number 1 and the string "1" can never collide (distinct type tags).
//   - Object fields are sorted by raw UTF-8 byte order; array order is
//     significant and preserved.
package canonkey

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"unicode/utf8"
)

// Limits bound the resources a single canonicalization may consume, so
// huge inputs stay budget-controlled.
type Limits struct {
	MaxDepth      int // maximum nesting depth
	MaxNodes      int // maximum number of encoded values
	MaxStringLen  int // maximum byte length of any string
	MaxNumberLen  int // maximum byte length of any number literal
	MaxInputBytes int // maximum bytes accepted by ParseParams
}

// DefaultLimits returns sane defaults for request-sized payloads.
func DefaultLimits() Limits {
	return Limits{
		MaxDepth:      100,
		MaxNodes:      1_000_000,
		MaxStringLen:  1 << 20,
		MaxNumberLen:  4096,
		MaxInputBytes: 16 << 20,
	}
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxDepth <= 0 {
		l.MaxDepth = d.MaxDepth
	}
	if l.MaxNodes <= 0 {
		l.MaxNodes = d.MaxNodes
	}
	if l.MaxStringLen <= 0 {
		l.MaxStringLen = d.MaxStringLen
	}
	if l.MaxNumberLen <= 0 {
		l.MaxNumberLen = d.MaxNumberLen
	}
	if l.MaxInputBytes <= 0 {
		l.MaxInputBytes = d.MaxInputBytes
	}
	return l
}

var (
	// ErrCycle reports a cyclic structure; cycles are out of scope.
	ErrCycle = errors.New("canonkey: cyclic structures are not supported")
	// ErrBudget reports that an input exceeded a configured budget.
	ErrBudget = errors.New("canonkey: input exceeds budget")
	// ErrInvalidUTF8 reports a string that is not valid UTF-8.
	ErrInvalidUTF8 = errors.New("canonkey: string is not valid UTF-8")
	// ErrUnsupportedType reports a Go type outside the supported scope.
	ErrUnsupportedType = errors.New("canonkey: unsupported type")
	// ErrInvalidNumber reports a malformed or non-finite number.
	ErrInvalidNumber = errors.New("canonkey: invalid number")
)

// Null is an explicit JSON null. Use it for Request.Params to distinguish
// an explicit null from an absent parameter set (nil).
var Null = nullT{}

type nullT struct{}

// Canonical returns the canonical byte encoding of v.
func Canonical(v any, lim Limits) ([]byte, error) {
	st := newCState(lim)
	if err := st.encode(v, 0); err != nil {
		return nil, err
	}
	return st.buf.Bytes(), nil
}

type visit struct {
	typ reflect.Type
	ptr uintptr
}

type cstate struct {
	buf      bytes.Buffer
	lim      Limits
	nodes    int
	visiting map[visit]bool
}

func newCState(lim Limits) *cstate {
	return &cstate{lim: lim.withDefaults(), visiting: make(map[visit]bool)}
}

func (s *cstate) encode(v any, depth int) error {
	if depth > s.lim.MaxDepth {
		return fmt.Errorf("%w: depth > %d", ErrBudget, s.lim.MaxDepth)
	}
	s.nodes++
	if s.nodes > s.lim.MaxNodes {
		return fmt.Errorf("%w: nodes > %d", ErrBudget, s.lim.MaxNodes)
	}
	switch t := v.(type) {
	case nil:
		s.buf.WriteByte('z')
		return nil
	case nullT:
		s.buf.WriteByte('z')
		return nil
	case bool:
		if t {
			s.buf.WriteString("bt")
		} else {
			s.buf.WriteString("bf")
		}
		return nil
	case string:
		return s.encString(t)
	case jsonNumber:
		return s.encNumber(string(t))
	case int:
		return s.encNumber(strconv.FormatInt(int64(t), 10))
	case int8:
		return s.encNumber(strconv.FormatInt(int64(t), 10))
	case int16:
		return s.encNumber(strconv.FormatInt(int64(t), 10))
	case int32:
		return s.encNumber(strconv.FormatInt(int64(t), 10))
	case int64:
		return s.encNumber(strconv.FormatInt(t, 10))
	case uint:
		return s.encNumber(strconv.FormatUint(uint64(t), 10))
	case uint8:
		return s.encNumber(strconv.FormatUint(uint64(t), 10))
	case uint16:
		return s.encNumber(strconv.FormatUint(uint64(t), 10))
	case uint32:
		return s.encNumber(strconv.FormatUint(uint64(t), 10))
	case uint64:
		return s.encNumber(strconv.FormatUint(t, 10))
	case float32:
		return s.encFloat(float64(t), 32)
	case float64:
		return s.encFloat(t, 64)
	case []any:
		leave, err := s.enter(t)
		if err != nil {
			return err
		}
		defer leave()
		fmt.Fprintf(&s.buf, "a%d[", len(t))
		for _, e := range t {
			if err := s.encode(e, depth+1); err != nil {
				return err
			}
		}
		s.buf.WriteByte(']')
		return nil
	case map[string]any:
		leave, err := s.enter(t)
		if err != nil {
			return err
		}
		defer leave()
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		// Verifiable field ordering: raw UTF-8 byte order.
		sort.Strings(keys)
		fmt.Fprintf(&s.buf, "o%d{", len(t))
		for _, k := range keys {
			if err := s.encString(k); err != nil {
				return err
			}
			if err := s.encode(t[k], depth+1); err != nil {
				return err
			}
		}
		s.buf.WriteByte('}')
		return nil
	default:
		return fmt.Errorf("%w: %T", ErrUnsupportedType, v)
	}
}

// enter guards one map/slice against cycles along the current path.
// Shared substructures on different paths are expanded by value.
func (s *cstate) enter(v any) (func(), error) {
	rv := reflect.ValueOf(v)
	vis := visit{rv.Type(), rv.Pointer()}
	if rv.Pointer() != 0 {
		if s.visiting[vis] {
			return nil, ErrCycle
		}
		s.visiting[vis] = true
	}
	return func() { delete(s.visiting, vis) }, nil
}

func (s *cstate) encString(str string) error {
	if !utf8.ValidString(str) {
		return ErrInvalidUTF8
	}
	if len(str) > s.lim.MaxStringLen {
		return fmt.Errorf("%w: string length > %d", ErrBudget, s.lim.MaxStringLen)
	}
	fmt.Fprintf(&s.buf, "s%d:", len(str))
	s.buf.WriteString(str)
	return nil
}

func (s *cstate) encFloat(f float64, bitSize int) error {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return fmt.Errorf("%w: non-finite float", ErrInvalidNumber)
	}
	return s.encNumber(strconv.FormatFloat(f, 'g', -1, bitSize))
}

func (s *cstate) encNumber(lit string) error {
	if len(lit) > s.lim.MaxNumberLen {
		return fmt.Errorf("%w: number length > %d", ErrBudget, s.lim.MaxNumberLen)
	}
	norm, err := canonNumber(lit)
	if err != nil {
		return err
	}
	s.buf.WriteByte('n')
	s.buf.WriteString(norm)
	return nil
}
