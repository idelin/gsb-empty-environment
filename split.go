package netsec

import "strings"

// splitConfigList splits the comma- and/or whitespace-separated lists used by
// rsyncd.conf parameters such as hosts allow and refuse options.
func splitConfigList(values []string) []string {
	var result []string
	for _, value := range values {
		result = append(result, strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t' || r == '\r' || r == '\n'
		})...)
	}
	return result
}
