package jar

import "fmt"

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// containsString 报告 s 是否存在于集合中。
func containsString(set []string, s string) bool {
	for _, v := range set {
		if v == s {
			return true
		}
	}
	return false
}
