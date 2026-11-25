package typeverifier

import (
	"errors"
)

// RootClass 是宿主提供的类继承树的唯一根。
const RootClass = "Object"

// Hierarchy 表示以 Object 为根的有限单继承类树。
// parents 记录每个非根类到其直接父类的映射；Object 隐式存在且不得出现在键中。
type Hierarchy struct {
	parent map[string]string
	depth  map[string]int
}

// NewHierarchy 根据“类 -> 直接父类”映射构造并校验类树：
// 所有父类必须存在、不得有环、Object 之外的类最终必须能回溯到 Object。
func NewHierarchy(parents map[string]string) (*Hierarchy, error) {
	if parents == nil {
		return nil, errors.New("typeverifier: 类层次映射为 nil")
	}
	if _, ok := parents[RootClass]; ok {
		return nil, errors.New("typeverifier: 根类 Object 不能声明父类")
	}
	h := &Hierarchy{
		parent: make(map[string]string, len(parents)+1),
		depth:  map[string]int{RootClass: 0},
	}
	for class, parent := range parents {
		if class == "" {
			return nil, errors.New("typeverifier: 存在空类名")
		}
		if parent == "" {
			return nil, errors.New("typeverifier: 类 " + class + " 缺少父类")
		}
		h.parent[class] = parent
	}
	for class := range h.parent {
		seen := map[string]bool{class: true}
		cur := class
		d := 0
		for cur != RootClass {
			parent, ok := h.parent[cur]
			if !ok {
				return nil, errors.New("typeverifier: 类 " + cur + " 的父类未定义")
			}
			if seen[parent] {
				return nil, errors.New("typeverifier: 类层次在 " + parent + " 处存在环")
			}
			seen[parent] = true
			cur = parent
			d++
		}
		h.depth[class] = d
	}
	return h, nil
}

// Has 报告类是否在类树中。
func (h *Hierarchy) Has(class string) bool {
	if class == RootClass {
		return true
	}
	_, ok := h.parent[class]
	return ok
}

// IsAssignable 报告 sub 类型的引用能否赋给 super 类型的位置（同类或子类）。
func (h *Hierarchy) IsAssignable(sub, super string) bool {
	if !h.Has(sub) || !h.Has(super) {
		return false
	}
	cur := sub
	for {
		if cur == super {
			return true
		}
		if cur == RootClass {
			return false
		}
		cur = h.parent[cur]
	}
}

// LCA 返回两个已初始化类的最近公共祖先；单根类树下结果一定存在。
func (h *Hierarchy) LCA(a, b string) string {
	da, db := h.depth[a], h.depth[b]
	for da > db {
		a = h.parent[a]
		da--
	}
	for db > da {
		b = h.parent[b]
		db--
	}
	for a != b {
		a = h.parent[a]
		b = h.parent[b]
	}
	return a
}
