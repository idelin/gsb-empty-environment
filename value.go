package rowconv

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Kind is the closed set of supported logical types.
type Kind string

const (
	KindNull      Kind = "null"
	KindInt       Kind = "int"
	KindDecimal   Kind = "decimal"
	KindString    Kind = "string"
	KindBytes     Kind = "bytes"
	KindTimestamp Kind = "timestamp"
)

func validKind(k Kind) bool {
	switch k {
	case KindInt, KindDecimal, KindString, KindBytes, KindTimestamp:
		return true
	}
	return false
}

// Decimal is a fixed-point decimal: value = Coef * 10^-Scale.
type Decimal struct {
	Coef  int64 `json:"coef,string"`
	Scale int32 `json:"scale"`
}

// Value is a typed cell. Timestamps have two forms: raw input text with an
// explicit offset (TsRaw, TsIsRaw=true) and converted UTC integer
// microseconds (TsMicros).
type Value struct {
	Kind     Kind
	Int      int64
	Dec      Decimal
	Str      string
	Bytes    []byte
	TsRaw    string
	TsMicros int64
	TsIsRaw  bool
}

// Null is the explicit-null singleton; it is distinct from a missing key.
func Null() Value { return Value{Kind: KindNull} }

func Int(v int64) Value    { return Value{Kind: KindInt, Int: v} }
func Str(v string) Value   { return Value{Kind: KindString, Str: v} }
func Bytes(v []byte) Value { return Value{Kind: KindBytes, Bytes: v} }
func Dec(coef int64, scale int32) Value {
	return Value{Kind: KindDecimal, Dec: Decimal{Coef: coef, Scale: scale}}
}
func TsRaw(s string) Value   { return Value{Kind: KindTimestamp, TsRaw: s, TsIsRaw: true} }
func TsMicros(v int64) Value { return Value{Kind: KindTimestamp, TsMicros: v} }

func (v Value) MarshalJSON() ([]byte, error) {
	switch v.Kind {
	case KindNull:
		return []byte("null"), nil
	case KindInt:
		return json.Marshal(v.Int)
	case KindDecimal:
		d, err := json.Marshal(v.Dec)
		if err != nil {
			return nil, err
		}
		return []byte(`{"$decimal":` + string(d) + `}`), nil
	case KindString:
		return json.Marshal(v.Str)
	case KindBytes:
		return json.Marshal(map[string]string{"$bytes": base64.StdEncoding.EncodeToString(v.Bytes)})
	case KindTimestamp:
		if v.TsIsRaw {
			return json.Marshal(map[string]string{"$ts": v.TsRaw})
		}
		return json.Marshal(map[string]int64{"$ts_micros": v.TsMicros})
	}
	return nil, fmt.Errorf("cannot marshal value of kind %q", v.Kind)
}

func (v *Value) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "null" {
		*v = Null()
		return nil
	}
	switch s[0] {
	case '"':
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return err
		}
		*v = Str(str)
		return nil
	case '{':
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(data, &obj); err != nil {
			return err
		}
		if len(obj) != 1 {
			return fmt.Errorf("typed value object must have exactly one key, got %d", len(obj))
		}
		for k, raw := range obj {
			switch k {
			case "$bytes":
				var b64 string
				if err := json.Unmarshal(raw, &b64); err != nil {
					return err
				}
				b, err := base64.StdEncoding.DecodeString(b64)
				if err != nil {
					return fmt.Errorf("$bytes: invalid base64: %v", err)
				}
				*v = Bytes(b)
			case "$decimal":
				var d Decimal
				if err := json.Unmarshal(raw, &d); err != nil {
					return err
				}
				*v = Dec(d.Coef, d.Scale)
			case "$ts":
				var ts string
				if err := json.Unmarshal(raw, &ts); err != nil {
					return err
				}
				*v = TsRaw(ts)
			case "$ts_micros":
				var m int64
				if err := json.Unmarshal(raw, &m); err != nil {
					return err
				}
				*v = TsMicros(m)
			default:
				return fmt.Errorf("unknown typed value key %q", k)
			}
		}
		return nil
	default:
		// JSON number: integers only.
		var n json.Number
		if err := json.Unmarshal(data, &n); err != nil {
			return fmt.Errorf("unsupported JSON value: %s", s)
		}
		i, err := strconv.ParseInt(n.String(), 10, 64)
		if err != nil {
			return fmt.Errorf("invalid integer %q", n.String())
		}
		*v = Int(i)
		return nil
	}
}
