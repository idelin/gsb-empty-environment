package ra

import (
	"sort"
	"strings"
)

// CanonicalRow 把一行规范为字符串：键排序、值带类型标签，NULL 与 0/"" 可区分。
func CanonicalRow(r Row) string {
	keys := make([]string, 0, len(r))
	for k := range r {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, k+"="+Canonical(r[k]))
	}
	return strings.Join(parts, ",")
}

// MultisetEqual 按行多重集比较两个结果（bag 语义：行序无关，重复计数敏感）。
func MultisetEqual(a, b []Row) bool {
	if len(a) != len(b) {
		return false
	}
	ca := make([]string, len(a))
	for i, r := range a {
		ca[i] = CanonicalRow(r)
	}
	cb := make([]string, len(b))
	for i, r := range b {
		cb[i] = CanonicalRow(r)
	}
	sort.Strings(ca)
	sort.Strings(cb)
	for i := range ca {
		if ca[i] != cb[i] {
			return false
		}
	}
	return true
}
