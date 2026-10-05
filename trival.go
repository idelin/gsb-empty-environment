package permdiff

// Tri 是三值逻辑（Kleene 逻辑）的真值。
// 缺失属性使谓词求值为 Unknown；授权判定只在结果为 True 时通过，
// 即 Unknown 最终被视为拒绝，不能按普通二值逻辑化简。
type Tri int

const (
	False Tri = iota
	Unknown
	True
)

// AndTri 是 Kleene 合取：任一为 False 则 False，否则任一为 Unknown 则 Unknown。
func AndTri(a, b Tri) Tri {
	if a < b {
		return a
	}
	return b
}

// OrTri 是 Kleene 析取：任一为 True 则 True，否则任一为 Unknown 则 Unknown。
func OrTri(a, b Tri) Tri {
	if a > b {
		return a
	}
	return b
}

// NotTri 是 Kleene 否定：Unknown 的否定仍是 Unknown。
func NotTri(a Tri) Tri {
	switch a {
	case True:
		return False
	case False:
		return True
	default:
		return Unknown
	}
}

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

// MarshalJSON 让 Tri 以字符串形式出现在 JSON 输出中。
func (t Tri) MarshalJSON() ([]byte, error) {
	return []byte(`"` + t.String() + `"`), nil
}
