package permdiff

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Kind 是属性值的类型。
type Kind int

const (
	KindInt Kind = iota
	KindSet
	KindBool
)

// Value 是一个具体的属性值：整数、标签集合或布尔。
type Value struct {
	Kind Kind
	Int  int
	Set  map[string]bool
	Bool bool
}

// IntValue 构造整数值。
func IntValue(v int) Value { return Value{Kind: KindInt, Int: v} }

// BoolValue 构造布尔值。
func BoolValue(v bool) Value { return Value{Kind: KindBool, Bool: v} }

// SetValue 构造标签集合值。
func SetValue(elems ...string) Value {
	s := make(map[string]bool, len(elems))
	for _, e := range elems {
		s[e] = true
	}
	return Value{Kind: KindSet, Set: s}
}

// Elems 以有序切片返回集合元素。
func (v Value) Elems() []string {
	out := make([]string, 0, len(v.Set))
	for e := range v.Set {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

func (v Value) String() string {
	switch v.Kind {
	case KindInt:
		return fmt.Sprintf("%d", v.Int)
	case KindBool:
		return fmt.Sprintf("%t", v.Bool)
	default:
		return fmt.Sprintf("%v", v.Elems())
	}
}

// Assignment 是主体/资源属性的部分赋值。
// 未出现的键表示该属性缺失，求值时产生 Unknown。
type Assignment map[string]Value

// Clone 返回赋值的深拷贝。
func (a Assignment) Clone() Assignment {
	out := make(Assignment, len(a))
	for k, v := range a {
		out[k] = v
	}
	return out
}

// Keys 以有序切片返回已赋值的属性名。
func (a Assignment) Keys() []string {
	out := make([]string, 0, len(a))
	for k := range a {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// MarshalJSON 把集合值编码为有序数组，保证输出确定。
func (a Assignment) MarshalJSON() ([]byte, error) {
	m := make(map[string]interface{}, len(a))
	for k, v := range a {
		switch v.Kind {
		case KindInt:
			m[k] = v.Int
		case KindBool:
			m[k] = v.Bool
		default:
			m[k] = v.Elems()
		}
	}
	return json.Marshal(m)
}

// UnmarshalJSON 解析 {"level": 5, "mfa": true, "groups": ["eng"]} 形式的赋值。
func (a *Assignment) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out := make(Assignment, len(raw))
	for k, rv := range raw {
		var b bool
		if err := json.Unmarshal(rv, &b); err == nil {
			out[k] = BoolValue(b)
			continue
		}
		var n int
		if err := json.Unmarshal(rv, &n); err == nil {
			out[k] = IntValue(n)
			continue
		}
		var s []string
		if err := json.Unmarshal(rv, &s); err == nil {
			out[k] = SetValue(s...)
			continue
		}
		return fmt.Errorf("属性 %q 的值必须是整数、布尔或字符串数组", k)
	}
	*a = out
	return nil
}
