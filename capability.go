package netsec

import "strings"

// matchPattern 判断具体值 value 是否落在 pattern 的授权范围内。
//
// 以 "*" 结尾的 pattern 是前缀通配（"doc/*" 匹配 "doc/a/b"），
// 其余 pattern 必须与 value 精确相等。单独的 "*" 匹配任意非空值。
func matchPattern(pattern, value string) bool {
	if pattern == "*" {
		return value != ""
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(value, prefix)
	}
	return pattern == value
}

// Covers 判断授权方能力 grant 是否覆盖被授权方能力 want：
// 资源与动作两个维度都必须落在 grant 的匹配范围内。
func Covers(grant, want Capability) bool {
	return matchPattern(grant.Resource, want.Resource) &&
		matchPattern(grant.Action, want.Action)
}

// coversAny 报告 caps 中是否存在覆盖 want 的能力。
func coversAny(caps []Capability, want Capability) bool {
	for _, grant := range caps {
		if Covers(grant, want) {
			return true
		}
	}
	return false
}

// CapabilitiesSubset 报告 child 中的每一条能力是否都被 parent 的某条能力覆盖。
//
// 委托只减不增：被委托方（child）声明的能力集合必须是委托方（parent）
// 既有能力的子集，任何放大都会返回 false。
func CapabilitiesSubset(child, parent []Capability) bool {
	for _, want := range child {
		if !coversAny(parent, want) {
			return false
		}
	}
	return true
}
