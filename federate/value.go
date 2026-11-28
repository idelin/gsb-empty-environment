package federate

import (
	"fmt"
	"strconv"
	"strings"
)

// Type is the runtime type of a Value.
type Type uint8

const (
	TypeNull Type = iota
	TypeInt
	TypeFloat
	TypeString
	TypeBool
)

func (t Type) String() string {
	switch t {
	case TypeNull:
		return "null"
	case TypeInt:
		return "int"
	case TypeFloat:
		return "float"
	case TypeString:
		return "string"
	case TypeBool:
		return "bool"
	default:
		return "unknown"
	}
}

// Value is a small, comparable SQL value. Collation is carried with string
// values so grouping, ordering and equality can honour COLLATE expressions.
type Value struct {
	Type      Type
	Int       int64
	Float     float64
	Str       string
	Bool      bool
	Collation string
}

func Int(v int64) Value     { return Value{Type: TypeInt, Int: v} }
func Float(v float64) Value { return Value{Type: TypeFloat, Float: v} }
func Str(v string) Value    { return Value{Type: TypeString, Str: v} }
func Bool(v bool) Value     { return Value{Type: TypeBool, Bool: v} }
func Null() Value           { return Value{Type: TypeNull} }

// WithCollation returns a string value annotated with a collation name.
func (v Value) WithCollation(name string) Value {
	v.Collation = name
	return v
}

func (v Value) IsNull() bool { return v.Type == TypeNull }

func (v Value) String() string {
	if v.IsNull() {
		return "NULL"
	}
	switch v.Type {
	case TypeInt:
		return strconv.FormatInt(v.Int, 10)
	case TypeFloat:
		return strconv.FormatFloat(v.Float, 'g', -1, 64)
	case TypeString:
		if v.Collation != "" {
			return fmt.Sprintf("%q COLLATE %s", v.Str, v.Collation)
		}
		return strconv.Quote(v.Str)
	case TypeBool:
		if v.Bool {
			return "true"
		}
		return "false"
	default:
		return "NULL"
	}
}

func isBinaryCollation(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "c", "binary", "bytea", "utf8_bin":
		return true
	default:
		return false
	}
}

// CollationNamesCompatible reports whether equality over two collations has the
// same equivalence classes as a bytewise comparison of the shard hash input.
// Only binary collations preserve the shard mapping performed by this package.
func CollationNamesCompatible(a, b string) bool {
	return isBinaryCollation(a) && isBinaryCollation(b)
}

func normalizeString(s, collation string) string {
	switch strings.ToLower(strings.TrimSpace(collation)) {
	case "nocase", "ci", "case_insensitive":
		return strings.ToLower(s)
	default:
		return s
	}
}

func effectiveCollation(a, b Value) string {
	if a.Collation != "" {
		return a.Collation
	}
	return b.Collation
}

// Compare compares two non-null values. Numeric types are coerced to a common
// float representation when they differ. The returned bool is false when the
// values are not comparable.
func Compare(a, b Value) (int, bool) {
	if a.IsNull() || b.IsNull() {
		return 0, false
	}
	an, bn := a.Type == TypeInt || a.Type == TypeFloat, b.Type == TypeInt || b.Type == TypeFloat
	if an && bn {
		af, bf := a.asFloat(), b.asFloat()
		switch {
		case af < bf:
			return -1, true
		case af > bf:
			return 1, true
		default:
			return 0, true
		}
	}
	if a.Type != b.Type {
		return 0, false
	}
	switch a.Type {
	case TypeString:
		coll := effectiveCollation(a, b)
		as, bs := normalizeString(a.Str, coll), normalizeString(b.Str, coll)
		return strings.Compare(as, bs), true
	case TypeInt:
		switch {
		case a.Int < b.Int:
			return -1, true
		case a.Int > b.Int:
			return 1, true
		default:
			return 0, true
		}
	case TypeFloat:
		switch {
		case a.Float < b.Float:
			return -1, true
		case a.Float > b.Float:
			return 1, true
		default:
			return 0, true
		}
	case TypeBool:
		if a.Bool == b.Bool {
			return 0, true
		}
		if !a.Bool {
			return -1, true
		}
		return 1, true
	default:
		return 0, false
	}
}

func (v Value) asFloat() float64 {
	if v.Type == TypeInt {
		return float64(v.Int)
	}
	return v.Float
}

// Equal reports SQL equality for two values, with NULL never equal to NULL.
func Equal(a, b Value) bool {
	if a.IsNull() || b.IsNull() {
		return false
	}
	c, ok := Compare(a, b)
	return ok && c == 0
}

// GroupKey is a canonical, collation-aware key used for grouping and DISTINCT.
func GroupKey(v Value) string {
	if v.IsNull() {
		return "N"
	}
	switch v.Type {
	case TypeInt:
		return "n:" + strconv.FormatFloat(float64(v.Int), 'g', -1, 64)
	case TypeFloat:
		return "n:" + strconv.FormatFloat(v.Float, 'g', -1, 64)
	case TypeString:
		s := normalizeString(v.Str, v.Collation)
		return "s:" + strconv.Itoa(len(s)) + ":" + s
	case TypeBool:
		if v.Bool {
			return "b:1"
		}
		return "b:0"
	default:
		return "N"
	}
}

// HashKey is the byte-level key used by the simulated hash distributor. It
// deliberately ignores collation: rows that are equal only under a
// case-insensitive collation may land on different shards.
func HashKey(v Value) string {
	if v.IsNull() {
		return "N;"
	}
	switch v.Type {
	case TypeInt:
		return "i:" + strconv.FormatInt(v.Int, 10) + ";"
	case TypeFloat:
		return "f:" + strconv.FormatFloat(v.Float, 'g', -1, 64) + ";"
	case TypeString:
		return "s:" + strconv.Itoa(len(v.Str)) + ":" + v.Str + ";"
	case TypeBool:
		if v.Bool {
			return "b:1;"
		}
		return "b:0;"
	default:
		return "N;"
	}
}

func addValues(a, b Value) Value {
	if a.IsNull() || b.IsNull() {
		return Null()
	}
	if a.Type == TypeFloat || b.Type == TypeFloat {
		return Float(a.asFloat() + b.asFloat())
	}
	if a.Type == TypeInt && b.Type == TypeInt {
		return Int(a.Int + b.Int)
	}
	return Null()
}

func divValues(a, b Value) Value {
	if a.IsNull() || b.IsNull() {
		return Null()
	}
	bf := b.asFloat()
	if bf == 0 {
		return Null()
	}
	return Float(a.asFloat() / bf)
}

func truth(v Value) (bool, bool) {
	if v.Type == TypeBool {
		return v.Bool, true
	}
	if v.IsNull() {
		return false, false
	}
	return false, true
}
