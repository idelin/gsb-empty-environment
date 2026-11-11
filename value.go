package netsec

// Value 是单个原子事实或规则节点的三值结果。
type Value int8

const (
	// Unknown 表示认证结果缺失或无法判定，区别于明确失败。
	Unknown Value = 0
	// Pass 表示认证通过。
	Pass Value = 1
	// Fail 表示认证失败（拒收依据）。
	Fail Value = -1
)

// String 返回三值的规范名称。
func (v Value) String() string {
	switch v {
	case Pass:
		return "pass"
	case Fail:
		return "fail"
	default:
		return "unknown"
	}
}

// ParseValue 解析不区分大小写的 "pass"、"fail"、"unknown"，其他输入返回 Unknown。
func ParseValue(s string) Value {
	switch s {
	case "pass", "PASS", "Pass":
		return Pass
	case "fail", "FAIL", "Fail":
		return Fail
	default:
		return Unknown
	}
}

// Not 是强 Kleene 否定：Pass 与 Fail 互换，Unknown 保持 Unknown。
func Not(v Value) Value {
	switch v {
	case Pass:
		return Fail
	case Fail:
		return Pass
	default:
		return Unknown
	}
}

// And 是强 Kleene 合取：
// 任一子式为 Fail 即 Fail；否则任一为 Unknown 即 Unknown；全部 Pass 才 Pass。
func And(vs ...Value) Value {
	hasUnknown := false
	for _, v := range vs {
		if v == Fail {
			return Fail
		}
		if v == Unknown {
			hasUnknown = true
		}
	}
	if hasUnknown {
		return Unknown
	}
	return Pass
}

// Or 是强 Kleene 析取：
// 任一子式为 Pass 即 Pass；否则任一为 Unknown 即 Unknown；全部 Fail 才 Fail。
func Or(vs ...Value) Value {
	hasUnknown := false
	for _, v := range vs {
		if v == Pass {
			return Pass
		}
		if v == Unknown {
			hasUnknown = true
		}
	}
	if hasUnknown {
		return Unknown
	}
	return Fail
}
