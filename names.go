package netsec

import "strings"

// canonicalName 把主机名规范化为小写、去掉结尾根点的形式。
// 根区本身（"." 或 ""）统一表示为 ""。
func canonicalName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(name, ".")
	return strings.ToLower(name)
}

// isSubDomain 判断 name 是否为 parent 的下属名字（不含相等）。
// 两个参数都应已经过 canonicalName 处理。
func isSubDomain(name, parent string) bool {
	if parent == "" {
		return name != ""
	}
	if name == parent || !strings.HasSuffix(name, "."+parent) {
		return false
	}
	return true
}
