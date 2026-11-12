package cond

import "fmt"

// Assignment 是一次穷举中各标签的取值。
type Assignment = Labels

// Enumerate 对给定标签名与有限元素全集，穷举所有取值组合：
// 每个标签依次取 缺失、空集合、全集的每个非空子集。
// 全集含 n 个元素时，每个标签有 2 + (2^n - 1) 种取值。
func Enumerate(labels []string, universe []string) []Assignment {
	values := make([]Value, 0, 2+1<<uint(len(universe)))
	values = append(values, Missing(), Set())
	for mask := 1; mask < 1<<uint(len(universe)); mask++ {
		var elems []string
		for i, u := range universe {
			if mask&(1<<uint(i)) != 0 {
				elems = append(elems, u)
			}
		}
		values = append(values, Set(elems...))
	}
	var out []Assignment
	var walk func(int, Assignment)
	walk = func(i int, cur Assignment) {
		if i == len(labels) {
			out = append(out, cur)
			return
		}
		for _, v := range values {
			next := make(Assignment, len(cur)+1)
			for k, x := range cur {
				next[k] = x
			}
			next[labels[i]] = v
			walk(i+1, next)
		}
	}
	walk(0, Assignment{})
	return out
}

// Mismatch 记录一次穷举中两表达式结果不一致的反例。
type Mismatch struct {
	Assignment Assignment
	Want, Got  Tri
}

func (m Mismatch) String() string {
	return fmt.Sprintf("assignment %v: want %s, got %s", m.Assignment, m.Want, m.Got)
}

// CheckEquivalent 在有限全集上穷举两个表达式引用的全部标签取值，
// 验证二者三态结果逐点一致。返回 nil 表示等价。
func CheckEquivalent(a, b Expr, universe []string) []Mismatch {
	labelSet := make(map[string]struct{})
	a.labels(labelSet)
	b.labels(labelSet)
	names := make([]string, 0, len(labelSet))
	for name := range labelSet {
		names = append(names, name)
	}
	var mismatches []Mismatch
	for _, as := range Enumerate(names, universe) {
		ra, _ := Eval(a, as)
		rb, _ := Eval(b, as)
		if ra != rb {
			mismatches = append(mismatches, Mismatch{Assignment: as, Want: ra, Got: rb})
		}
	}
	return mismatches
}

// CheckSimplify 验证 Simplify 在有限全集上保持三态结果。
func CheckSimplify(e Expr, universe []string) []Mismatch {
	return CheckEquivalent(e, Simplify(e), universe)
}
