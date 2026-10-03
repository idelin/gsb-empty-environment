package typepipe

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// toInt64 将驱动返回的常见类型归一化为 int64。
// 字符串解析失败与浮点越界均返回 ok=false。
func toInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case int:
		return int64(t), true
	case int8:
		return int64(t), true
	case int16:
		return int64(t), true
	case int32:
		return int64(t), true
	case int64:
		return t, true
	case uint:
		return uintToInt64(uint64(t))
	case uint8:
		return int64(t), true
	case uint16:
		return int64(t), true
	case uint32:
		return int64(t), true
	case uint64:
		return uintToInt64(t)
	case float32:
		return floatToInt64(float64(t))
	case float64:
		return floatToInt64(t)
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case string:
		return parseIntString(t)
	case []byte:
		return parseIntString(string(t))
	default:
		return 0, false
	}
}

func uintToInt64(u uint64) (int64, bool) {
	if u > math.MaxInt64 {
		return 0, false
	}
	return int64(u), true
}

func floatToInt64(f float64) (int64, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
		return 0, false
	}
	if f >= math.MaxInt64 || f <= math.MinInt64 {
		return 0, false
	}
	return int64(f), true
}

func parseIntString(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, true
	} else if errors.Is(err, strconv.ErrRange) {
		return 0, false
	}
	// 兼容 "42.0" 这类来自 JSON/decimal 的字面量
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return floatToInt64(f)
	}
	return 0, false
}

func toFloat64(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return 0, false
		}
		return f, true
	case []byte:
		return toFloat64(string(t))
	default:
		if n, ok := toInt64(v); ok {
			return float64(n), true
		}
		return 0, false
	}
}

func toString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case []byte:
		return string(t), true
	case bool:
		return strconv.FormatBool(t), true
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", t), true
	case float32, float64:
		return fmt.Sprintf("%v", t), true
	default:
		return "", false
	}
}
