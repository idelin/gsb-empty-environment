package typepipe

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Converter 把单个源值转换为目标值；返回的 *Anomaly 非空表示产生了清洗异常日志。
type Converter func(f *Field, v any) (any, *Anomaly)

var converters = map[string]Converter{
	"bool":        convertBool,
	"int":         convertInt,
	"float":       convertFloat,
	"string":      convertString,
	"datetime":    convertDateTime,
	"enum":        convertEnum,
	"uuid":        convertUUID,
	"json":        convertJSON,
	"passthrough": func(f *Field, v any) (any, *Anomaly) { return v, nil },
}

// convertBool 处理 MySQL tinyint(1) / PostgreSQL boolean / JSON bool。
// 数值仅 0/1 合法，其余按非零即真转换并记录 out_of_domain 告警。
func convertBool(f *Field, v any) (any, *Anomaly) {
	if v == nil {
		return f.fail(v, "null_value")
	}
	switch t := v.(type) {
	case bool:
		return t, nil
	case string, []byte:
		s, _ := toString(v)
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true", "t", "yes", "on":
			return true, nil
		case "false", "f", "no", "off":
			return false, nil
		case "1":
			return true, nil
		case "0":
			return false, nil
		}
		return f.fail(v, "invalid_format")
	default:
		n, ok := toInt64(v)
		if !ok {
			return f.fail(v, "type_mismatch")
		}
		switch n {
		case 0:
			return false, nil
		case 1:
			return true, nil
		}
		out := n != 0
		return out, &Anomaly{Field: f.Name, Reason: "out_of_domain", Input: v, Action: "default_applied", Output: out}
	}
}

// convertInt 将数值收敛到 [Min, Max] 目标区间；越界钳制到边界并记录 overflow 日志。
func convertInt(f *Field, v any) (any, *Anomaly) {
	if v == nil {
		return f.fail(v, "null_value")
	}
	n, ok := toInt64(v)
	if !ok {
		return f.fail(v, "invalid_format")
	}
	if f.Min != nil && n < *f.Min {
		return *f.Min, &Anomaly{Field: f.Name, Reason: "overflow", Input: v, Action: "clamped", Output: *f.Min}
	}
	if f.Max != nil && n > *f.Max {
		return *f.Max, &Anomaly{Field: f.Name, Reason: "overflow", Input: v, Action: "clamped", Output: *f.Max}
	}
	return n, nil
}

func convertFloat(f *Field, v any) (any, *Anomaly) {
	if v == nil {
		return f.fail(v, "null_value")
	}
	x, ok := toFloat64(v)
	if !ok {
		return f.fail(v, "invalid_format")
	}
	return x, nil
}

func convertString(f *Field, v any) (any, *Anomaly) {
	if v == nil {
		return f.fail(v, "null_value")
	}
	s, ok := toString(v)
	if !ok {
		return f.fail(v, "type_mismatch")
	}
	return s, nil
}

var datetimeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02",
}

// convertDateTime 把 MySQL datetime/timestamp、PostgreSQL timestamptz、
// JSON 时间字符串或 Unix 秒统一为带 UTC 偏移的 ISO 8601（RFC 3339）字符串。
// 朴素字面量按 Field.TZOffset 指定的源时区解释，默认 UTC。
func convertDateTime(f *Field, v any) (any, *Anomaly) {
	if v == nil {
		return f.fail(v, "null_value")
	}
	loc := time.UTC
	if f.TZOffset != "" {
		l, err := parseUTCOffset(f.TZOffset)
		if err == nil {
			loc = l
		}
	}
	switch t := v.(type) {
	case time.Time:
		return t.Format(time.RFC3339), nil
	case string, []byte:
		s, _ := toString(v)
		s = strings.TrimSpace(s)
		for _, layout := range datetimeLayouts {
			if ts, err := time.Parse(layout, s); err == nil {
				// 布局含时区信息时 Parse 已绑定该时区；否则按源时区偏移解释
				if layout == time.RFC3339 || layout == time.RFC3339Nano {
					return ts.Format(time.RFC3339), nil
				}
				return time.Date(ts.Year(), ts.Month(), ts.Day(),
					ts.Hour(), ts.Minute(), ts.Second(), ts.Nanosecond(), loc).Format(time.RFC3339), nil
			}
		}
		return f.fail(v, "invalid_format")
	default:
		if n, ok := toInt64(v); ok { // Unix 秒
			return time.Unix(n, 0).UTC().Format(time.RFC3339), nil
		}
		return f.fail(v, "type_mismatch")
	}
}

// parseUTCOffset 解析 "+08:00" / "-0530" / "Z" 形式的 UTC 偏移。
func parseUTCOffset(s string) (*time.Location, error) {
	s = strings.TrimSpace(s)
	if s == "Z" || s == "UTC" || s == "+00:00" {
		return time.UTC, nil
	}
	if len(s) != 6 && len(s) != 5 {
		return nil, fmt.Errorf("bad offset %q", s)
	}
	sign := 1
	switch s[0] {
	case '+':
	case '-':
		sign = -1
	default:
		return nil, fmt.Errorf("bad offset %q", s)
	}
	digits := strings.Replace(s[1:], ":", "", 1)
	h, err := strconv.Atoi(digits[:2])
	if err != nil {
		return nil, err
	}
	m, err := strconv.Atoi(digits[2:])
	if err != nil {
		return nil, err
	}
	if h > 14 || m > 59 {
		return nil, fmt.Errorf("offset out of range %q", s)
	}
	return time.FixedZone(s, sign*(h*3600+m*60)), nil
}

// convertEnum 把 MySQL enum 字面值映射为整数字典码。
// 映射表优先取 Field.EnumMap，否则按 enum('a','b',...) 声明顺序从 1 起编号（与 MySQL 内部索引一致）。
// 未登记的枚举值落到安全默认码 0 并记录 unknown_enum 日志。
func convertEnum(f *Field, v any) (any, *Anomaly) {
	if v == nil {
		return f.fail(v, "null_value")
	}
	m := f.EnumMap
	if m == nil {
		m = ParseEnumDef(f.SourceType)
	}
	s, ok := toString(v)
	if !ok {
		return f.fail(v, "type_mismatch")
	}
	if code, ok := m[s]; ok {
		return code, nil
	}
	out := f.safeDefault()
	if !f.HasDefault {
		out = int64(0)
	}
	return out, &Anomaly{Field: f.Name, Reason: "unknown_enum", Input: v, Action: "default_applied", Output: out}
}

var enumDefRe = regexp.MustCompile(`(?i)^enum\s*\((.*)\)$`)

// ParseEnumDef 解析 enum('active','pending') 声明，返回 值->字典码（从 1 起）。
func ParseEnumDef(decl string) map[string]int64 {
	out := map[string]int64{}
	m := enumDefRe.FindStringSubmatch(strings.TrimSpace(decl))
	if m == nil {
		return out
	}
	body := m[1]
	code := int64(1)
	for i := 0; i < len(body); {
		for i < len(body) && (body[i] == ' ' || body[i] == ',') {
			i++
		}
		if i >= len(body) {
			break
		}
		if body[i] != '\'' && body[i] != '"' {
			break
		}
		quote := body[i]
		i++
		var sb strings.Builder
		for i < len(body) {
			c := body[i]
			if c == '\\' && i+1 < len(body) {
				sb.WriteByte(body[i+1])
				i += 2
				continue
			}
			if c == quote {
				// SQL 以 '' 转义单引号
				if i+1 < len(body) && body[i+1] == quote {
					sb.WriteByte(quote)
					i += 2
					continue
				}
				i++
				break
			}
			sb.WriteByte(c)
			i++
		}
		out[sb.String()] = code
		code++
	}
	return out
}

// convertUUID 在 UUID 与字符串表示间互转：校验并规范化为小写连字符形式。
// 接受无连字符的 32 位十六进制；非法值落到零 UUID 并记录 invalid_format。
func convertUUID(f *Field, v any) (any, *Anomaly) {
	if v == nil {
		return f.fail(v, "null_value")
	}
	s, ok := toString(v)
	if !ok {
		return f.fail(v, "type_mismatch")
	}
	norm, ok := NormalizeUUID(s)
	if !ok {
		return f.fail(v, "invalid_format")
	}
	return norm, nil
}

// NormalizeUUID 校验并规范化 UUID 字符串；ok=false 表示非法。
func NormalizeUUID(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	hex := strings.ReplaceAll(s, "-", "")
	if len(hex) != 32 {
		return "", false
	}
	for _, c := range hex {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return "", false
		}
	}
	if len(s) != 32 && len(s) != 36 {
		return "", false
	}
	if len(s) == 36 {
		for _, i := range []int{8, 13, 18, 23} {
			if s[i] != '-' {
				return "", false
			}
		}
	}
	return hex[0:8] + "-" + hex[8:12] + "-" + hex[12:16] + "-" + hex[16:20] + "-" + hex[20:32], true
}

func convertJSON(f *Field, v any) (any, *Anomaly) {
	if v == nil {
		return f.fail(v, "null_value")
	}
	return v, nil
}
