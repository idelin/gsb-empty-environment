package ra

import "fmt"

// Row 是一行数据：列名 -> 值。nil 表示 SQL NULL。
type Row map[string]any

// Trilean 是 SQL 三值逻辑的真值。
type Trilean int8

const (
	False Trilean = iota
	Unknown
	True
)

func And3(a, b Trilean) Trilean {
	if a == False || b == False {
		return False
	}
	if a == Unknown || b == Unknown {
		return Unknown
	}
	return True
}

func Or3(a, b Trilean) Trilean {
	if a == True || b == True {
		return True
	}
	if a == Unknown || b == Unknown {
		return Unknown
	}
	return False
}

func Not3(a Trilean) Trilean {
	switch a {
	case True:
		return False
	case False:
		return True
	default:
		return Unknown
	}
}

// Eq3 是 SQL 等值：任一侧为 NULL 时结果为 Unknown。
func Eq3(a, b any) Trilean {
	if a == nil || b == nil {
		return Unknown
	}
	switch av := a.(type) {
	case int64:
		bv, ok := b.(int64)
		if !ok {
			return False
		}
		if av == bv {
			return True
		}
		return False
	case string:
		bv, ok := b.(string)
		if !ok {
			return False
		}
		if av == bv {
			return True
		}
		return False
	case bool:
		bv, ok := b.(bool)
		if !ok {
			return False
		}
		if av == bv {
			return True
		}
		return False
	}
	return False
}

// Less3 是 SQL 小于比较：任一侧为 NULL 时结果为 Unknown。
func Less3(a, b any) (Trilean, error) {
	if a == nil || b == nil {
		return Unknown, nil
	}
	switch av := a.(type) {
	case int64:
		bv, ok := b.(int64)
		if !ok {
			return Unknown, fmt.Errorf("类型不可比较: %v 与 %v", a, b)
		}
		if av < bv {
			return True, nil
		}
		return False, nil
	case string:
		bv, ok := b.(string)
		if !ok {
			return Unknown, fmt.Errorf("类型不可比较: %v 与 %v", a, b)
		}
		if av < bv {
			return True, nil
		}
		return False, nil
	}
	return Unknown, fmt.Errorf("类型不可比较: %v 与 %v", a, b)
}

// Canonical 给出值的规范字符串，用于多重集比较与 GROUP BY 键。
// NULL 在此被规范为同一个键（与 GROUP BY 把 NULL 归为一组一致）。
func Canonical(v any) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case int64:
		return fmt.Sprintf("i:%d", t)
	case string:
		return fmt.Sprintf("s:%s", t)
	case bool:
		return fmt.Sprintf("b:%v", t)
	default:
		return fmt.Sprintf("?:%v", t)
	}
}
