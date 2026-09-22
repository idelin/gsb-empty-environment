package bitlayout

import (
	"encoding/json"
	"math"
)

// asUint coerces a JSON-decoded value to uint64.
func asUint(v any) (uint64, bool) {
	switch t := v.(type) {
	case uint64:
		return t, true
	case uint:
		return uint64(t), true
	case uint32:
		return uint64(t), true
	case int:
		if t >= 0 {
			return uint64(t), true
		}
	case int64:
		if t >= 0 {
			return uint64(t), true
		}
	case int32:
		if t >= 0 {
			return uint64(t), true
		}
	case float64:
		if t >= 0 && t == math.Trunc(t) && t < 1<<63 {
			return uint64(t), true
		}
	case json.Number:
		if u, err := parseUintString(t.String()); err == nil {
			return u, true
		}
	}
	return 0, false
}

// asInt coerces a JSON-decoded value to int64.
func asInt(v any) (int64, bool) {
	switch t := v.(type) {
	case int64:
		return t, true
	case int:
		return int64(t), true
	case int32:
		return int64(t), true
	case uint64:
		if t <= math.MaxInt64 {
			return int64(t), true
		}
	case uint:
		if uint64(t) <= math.MaxInt64 {
			return int64(t), true
		}
	case float64:
		if t == math.Trunc(t) && t >= math.MinInt64 && t <= math.MaxInt64 {
			return int64(t), true
		}
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i, true
		}
	}
	return 0, false
}

func parseUintString(s string) (uint64, error) {
	var v uint64
	if s == "" {
		return 0, errBadNumber
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errBadNumber
		}
		d := uint64(c - '0')
		if v > (math.MaxUint64-d)/10 {
			return 0, errBadNumber
		}
		v = v*10 + d
	}
	return v, nil
}

var errBadNumber = errorString("not an unsigned integer")

type errorString string

func (e errorString) Error() string { return string(e) }
