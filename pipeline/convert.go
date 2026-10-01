package pipeline

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// isoLayout renders timestamps as ISO-8601 with an explicit UTC offset,
// e.g. 2024-03-01T04:30:00+00:00.
const isoLayout = "2006-01-02T15:04:05-07:00"

// nilUUID is the safe default for unconvertible UUID values.
const nilUUID = "00000000-0000-0000-0000-000000000000"

// naiveTimeLayouts are tried, in order, for zone-less datetime strings.
var naiveTimeLayouts = []string{
	"2006-01-02 15:04:05.999999",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

func convert(rule Rule, f Field, v any) (any, *Issue) {
	switch rule.Target {
	case TypeBool:
		return toBool(rule, f, v)
	case TypeISO8601:
		return toISO8601(rule, f, v)
	case TypeDictCode:
		return toDictCode(rule, f, v)
	case TypeUUID:
		return toUUID(rule, f, v)
	case TypeString:
		return toString(rule, f, v)
	case TypeInt:
		return toInt(rule, f, v)
	}
	return v, &Issue{Field: f.Name, Type: f.Type.String(), Value: v,
		Reason: "unknown target type " + strconv.Quote(rule.Target), Effect: "passed through"}
}

// defaultFor resolves the safe default for a rule.
func defaultFor(rule Rule) any {
	if rule.Default != nil {
		return rule.Default
	}
	switch rule.Target {
	case TypeBool:
		return false
	case TypeISO8601:
		return time.Unix(0, 0).UTC().Format(isoLayout)
	case TypeDictCode:
		return 0
	case TypeUUID:
		return nilUUID
	case TypeString:
		return ""
	case TypeInt:
		return int64(0)
	}
	return nil
}

func badValue(rule Rule, f Field, v any, reason string) (any, *Issue) {
	d := defaultFor(rule)
	return d, &Issue{Field: f.Name, Type: f.Type.String(), Value: v,
		Reason: reason, Effect: fmt.Sprintf("default %v", d)}
}

// toBool maps MySQL tinyint(1) and friends onto a boolean.
func toBool(rule Rule, f Field, v any) (any, *Issue) {
	switch t := v.(type) {
	case bool:
		return t, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "1":
			return true, nil
		case "false", "0":
			return false, nil
		}
	default:
		if n, ok := toInt64(v); ok {
			switch n {
			case 0:
				return false, nil
			case 1:
				return true, nil
			}
		}
	}
	return badValue(rule, f, v, "cannot interpret as boolean")
}

// toISO8601 parses a datetime and renders it in UTC with a +00:00 offset.
// Naive datetimes are interpreted in rule.Location (default UTC).
func toISO8601(rule Rule, f Field, v any) (any, *Issue) {
	loc := rule.Location
	if loc == nil {
		loc = time.UTC
	}
	var t time.Time
	ok := true
	switch tv := v.(type) {
	case time.Time:
		t = tv
	case string:
		s := strings.TrimSpace(tv)
		ok = false
		if rt, err := time.Parse(time.RFC3339, s); err == nil {
			t, ok = rt, true
			break
		}
		for _, layout := range naiveTimeLayouts {
			if pt, err := time.ParseInLocation(layout, s, loc); err == nil {
				t, ok = pt, true
				break
			}
		}
	default:
		if n, nok := toInt64(v); nok {
			t = time.Unix(n, 0) // unix epoch seconds
		} else {
			ok = false
		}
	}
	if !ok {
		return badValue(rule, f, v, "cannot parse as datetime")
	}
	return t.UTC().Format(isoLayout), nil
}

// toDictCode maps an enum label to its integer dictionary code. When the
// rule carries no explicit Dict, codes are derived 1-based from the enum
// members declared in the column type.
func toDictCode(rule Rule, f Field, v any) (any, *Issue) {
	dict := rule.Dict
	if dict == nil && len(f.Type.Args) > 0 {
		dict = make(map[string]int, len(f.Type.Args))
		for i, member := range f.Type.Args {
			dict[member] = i + 1
		}
	}
	if s, ok := v.(string); ok {
		if code, found := dict[s]; found {
			return code, nil
		}
	}
	return badValue(rule, f, v, "value not in enum dictionary")
}

// toUUID validates and normalizes a UUID string (lowercase, hyphenated).
func toUUID(rule Rule, f Field, v any) (any, *Issue) {
	s, ok := v.(string)
	if ok {
		if norm, valid := normalizeUUID(s); valid {
			return norm, nil
		}
	}
	return badValue(rule, f, v, "invalid UUID format")
}

// toString renders any value as a string (e.g. PostgreSQL uuid -> string).
func toString(rule Rule, f Field, v any) (any, *Issue) {
	switch s := v.(type) {
	case string:
		return s, nil
	case []byte:
		return string(s), nil
	case fmt.Stringer:
		return s.String(), nil
	}
	return fmt.Sprintf("%v", v), nil
}

// toInt coerces a value to int64 and enforces the rule's [Min, Max] range,
// applying the configured overflow policy.
func toInt(rule Rule, f Field, v any) (any, *Issue) {
	n, ok := toInt64(v)
	if !ok {
		return badValue(rule, f, v, "cannot interpret as integer")
	}
	if n < rule.Min || n > rule.Max {
		reason := fmt.Sprintf("value %d out of range [%d,%d]", n, rule.Min, rule.Max)
		if rule.Overflow == UseDefault {
			d := defaultFor(rule)
			return d, &Issue{Field: f.Name, Type: f.Type.String(), Value: v,
				Reason: reason, Effect: fmt.Sprintf("default %v", d)}
		}
		clamped := n
		if clamped < rule.Min {
			clamped = rule.Min
		}
		if clamped > rule.Max {
			clamped = rule.Max
		}
		return clamped, &Issue{Field: f.Name, Type: f.Type.String(), Value: v,
			Reason: reason, Effect: fmt.Sprintf("clamped to %d", clamped)}
	}
	return n, nil
}

// toInt64 extracts an int64 from common driver/JSON value shapes.
func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint:
		return uintToInt64(uint64(n))
	case uint8:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint64:
		return uintToInt64(n)
	case float32:
		return floatToInt64(float64(n))
	case float64:
		return floatToInt64(n)
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i, true
		}
		if fl, err := n.Float64(); err == nil {
			return floatToInt64(fl)
		}
		return 0, false
	case string:
		if i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64); err == nil {
			return i, true
		}
	}
	return 0, false
}

func uintToInt64(u uint64) (int64, bool) {
	if u > math.MaxInt64 {
		return 0, false
	}
	return int64(u), true
}

func floatToInt64(fl float64) (int64, bool) {
	if fl != math.Trunc(fl) || math.Abs(fl) >= 9.223372036854776e18 {
		return 0, false
	}
	return int64(fl), true
}

// normalizeUUID validates the 8-4-4-4-12 hex shape and lowercases it.
func normalizeUUID(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) != 36 {
		return "", false
	}
	for i := 0; i < 36; i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return "", false
			}
		default:
			if !isHexDigit(c) {
				return "", false
			}
		}
	}
	return strings.ToLower(s), true
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
