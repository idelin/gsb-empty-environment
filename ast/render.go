package ast

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func quoteIdent(name string) string {
	return "\"" + strings.ReplaceAll(name, "\"", "\"\"") + "\""
}

func renderLiteral(value any) string {
	switch v := value.(type) {
	case nil:
		return "NULL"
	case string:
		return "'" + strings.ReplaceAll(strings.ReplaceAll(v, "'", "''"), "\x00", "") + "'"
	case []byte:
		return "X'" + strings.ToUpper(fmt.Sprintf("%x", v)) + "'"
	case bool:
		if v {
			return "TRUE"
		}
		return "FALSE"
	case int64:
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// SortedKeys returns the sorted keys of a row-shaped map, used to make
// rendered and asserted output deterministic.
func SortedKeys(row map[string]any) []string {
	keys := make([]string, 0, len(row))
	for k := range row {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
