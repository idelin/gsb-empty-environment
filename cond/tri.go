// Package cond 为 Go 云网关提供策略条件编译与三态求值。
//
// 请求标签的值区分三种形态：缺失（Missing）、空集合、非空集合。
// 条件求值采用 Kleene 强三态逻辑（True / False / Unknown），
// 最终判定仅 True 放行，False 与 Unknown 一律拒绝。
package cond

// Tri 是三态逻辑值。
type Tri int

const (
	False   Tri = iota // 条件不成立
	True               // 条件成立
	Unknown            // 证据不足（如标签缺失），不可放行
)

func (t Tri) String() string {
	switch t {
	case True:
		return "true"
	case False:
		return "false"
	default:
		return "unknown"
	}
}

// Not 是 Kleene 否定：¬U = U。
func Not(t Tri) Tri {
	switch t {
	case True:
		return False
	case False:
		return True
	default:
		return Unknown
	}
}

// And 是 Kleene 合取：F 优先，其次 U。
func And(a, b Tri) Tri {
	if a == False || b == False {
		return False
	}
	if a == Unknown || b == Unknown {
		return Unknown
	}
	return True
}

// Or 是 Kleene 析取：T 优先，其次 U。
func Or(a, b Tri) Tri {
	if a == True || b == True {
		return True
	}
	if a == Unknown || b == Unknown {
		return Unknown
	}
	return False
}

// Allow 是最终放行判定：仅 True 放行。
func Allow(t Tri) bool { return t == True }
