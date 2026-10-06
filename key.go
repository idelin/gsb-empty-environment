package taskgraph

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// canonicalKey renders a task name plus its parameters into a canonical,
// collision-resistant string. Every value is type-tagged and length-prefixed
// so distinct parameter trees never encode to the same key, while
// semantically equal trees (e.g. maps written in different key order)
// always encode to the same key.
func canonicalKey(name string, params map[string]interface{}) string {
	var sb strings.Builder
	sb.WriteString("n")
	sb.WriteString(strconv.Itoa(len(name)))
	sb.WriteByte(':')
	sb.WriteString(name)
	sb.WriteByte(';')
	encodeValue(&sb, params)
	return sb.String()
}

func encodeValue(sb *strings.Builder, v interface{}) {
	switch t := v.(type) {
	case nil:
		sb.WriteString("0;")
	case bool:
		if t {
			sb.WriteString("b1;")
		} else {
			sb.WriteString("b0;")
		}
	case string:
		sb.WriteString("s")
		sb.WriteString(strconv.Itoa(len(t)))
		sb.WriteByte(':')
		sb.WriteString(t)
		sb.WriteByte(';')
	case int:
		sb.WriteString("i")
		sb.WriteString(strconv.Itoa(t))
		sb.WriteByte(';')
	case int64:
		sb.WriteString("i")
		sb.WriteString(strconv.FormatInt(t, 10))
		sb.WriteByte(';')
	case float64:
		sb.WriteString("f")
		sb.WriteString(strconv.FormatFloat(t, 'g', -1, 64))
		sb.WriteByte(';')
	case []interface{}:
		sb.WriteString("l[")
		for _, e := range t {
			encodeValue(sb, e)
		}
		sb.WriteByte(']')
	case map[string]interface{}:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		sb.WriteString("m{")
		for _, k := range keys {
			encodeValue(sb, k)
			encodeValue(sb, t[k])
		}
		sb.WriteByte('}')
	default:
		// Deterministic fallback for less common concrete types.
		fmt.Fprintf(sb, "?%T:%v;", v, v)
	}
}
