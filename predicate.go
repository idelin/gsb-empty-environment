package permdiff

import (
	"fmt"
	"sort"
	"strings"
)

// Predicate 是作用在单个属性上的原子条件。
// 属性缺失或类型不匹配时求值为 Unknown。
type Predicate interface {
	Attr() string
	Eval(a Assignment) Tri
	String() string
}

// IntRange 判断整数属性是否落在闭区间 [Min, Max] 内。
type IntRange struct {
	A        string
	Min, Max int
}

func (p IntRange) Attr() string { return p.A }

func (p IntRange) Eval(a Assignment) Tri {
	v, ok := a[p.A]
	if !ok || v.Kind != KindInt {
		return Unknown
	}
	if v.Int >= p.Min && v.Int <= p.Max {
		return True
	}
	return False
}

func (p IntRange) String() string {
	return fmt.Sprintf("%s ∈ [%d, %d]", p.A, p.Min, p.Max)
}

// BoolIs 判断布尔属性是否等于给定值。
type BoolIs struct {
	A   string
	Val bool
}

func (p BoolIs) Attr() string { return p.A }

func (p BoolIs) Eval(a Assignment) Tri {
	v, ok := a[p.A]
	if !ok || v.Kind != KindBool {
		return Unknown
	}
	if v.Bool == p.Val {
		return True
	}
	return False
}

func (p BoolIs) String() string {
	return fmt.Sprintf("%s == %t", p.A, p.Val)
}

// SetSubsetOf 判断集合属性是否为给定集合的子集（attr ⊆ set）。
type SetSubsetOf struct {
	A   string
	Set map[string]bool
}

func (p SetSubsetOf) Attr() string { return p.A }

func (p SetSubsetOf) Eval(a Assignment) Tri {
	v, ok := a[p.A]
	if !ok || v.Kind != KindSet {
		return Unknown
	}
	for e := range v.Set {
		if !p.Set[e] {
			return False
		}
	}
	return True
}

func (p SetSubsetOf) String() string {
	return fmt.Sprintf("%s ⊆ %s", p.A, formatSet(p.Set))
}

// SetContainsAll 判断集合属性是否包含给定集合的全部元素（attr ⊇ set）。
type SetContainsAll struct {
	A   string
	Set map[string]bool
}

func (p SetContainsAll) Attr() string { return p.A }

func (p SetContainsAll) Eval(a Assignment) Tri {
	v, ok := a[p.A]
	if !ok || v.Kind != KindSet {
		return Unknown
	}
	for e := range p.Set {
		if !v.Set[e] {
			return False
		}
	}
	return True
}

func (p SetContainsAll) String() string {
	return fmt.Sprintf("%s ⊇ %s", p.A, formatSet(p.Set))
}

// SetContains 判断集合属性是否包含某个元素。
type SetContains struct {
	A    string
	Elem string
}

func (p SetContains) Attr() string { return p.A }

func (p SetContains) Eval(a Assignment) Tri {
	v, ok := a[p.A]
	if !ok || v.Kind != KindSet {
		return Unknown
	}
	if v.Set[p.Elem] {
		return True
	}
	return False
}

func (p SetContains) String() string {
	return fmt.Sprintf("%s ∋ %q", p.A, p.Elem)
}

// SetIntersects 判断集合属性与给定集合是否有交集。
type SetIntersects struct {
	A   string
	Set map[string]bool
}

func (p SetIntersects) Attr() string { return p.A }

func (p SetIntersects) Eval(a Assignment) Tri {
	v, ok := a[p.A]
	if !ok || v.Kind != KindSet {
		return Unknown
	}
	for e := range v.Set {
		if p.Set[e] {
			return True
		}
	}
	return False
}

func (p SetIntersects) String() string {
	return fmt.Sprintf("%s ∩ %s ≠ ∅", p.A, formatSet(p.Set))
}

func formatSet(s map[string]bool) string {
	elems := make([]string, 0, len(s))
	for e := range s {
		elems = append(elems, e)
	}
	sort.Strings(elems)
	return "{" + strings.Join(elems, ", ") + "}"
}
