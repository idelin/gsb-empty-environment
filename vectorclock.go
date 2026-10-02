package causalrouter

import (
	"fmt"
	"sort"
	"strings"
)

// VectorClock 版本向量：客户端ID -> 该客户端已知的最新事件序号。
type VectorClock map[string]uint64

// Relation 两个向量时钟之间的因果关系。
type Relation int

const (
	Equal      Relation = iota // 完全相同
	Before                     // a 因果先于 b
	After                      // a 因果后于 b
	Concurrent                 // 并发（无因果关系）
)

func (r Relation) String() string {
	switch r {
	case Equal:
		return "equal"
	case Before:
		return "before"
	case After:
		return "after"
	default:
		return "concurrent"
	}
}

// Copy 返回向量时钟的深拷贝。
func (vc VectorClock) Copy() VectorClock {
	out := make(VectorClock, len(vc))
	for k, v := range vc {
		out[k] = v
	}
	return out
}

// Increment 将指定客户端的分量加一。
func (vc VectorClock) Increment(id string) {
	vc[id]++
}

// Merge 与另一个向量时钟做逐分量取最大值合并。
func (vc VectorClock) Merge(other VectorClock) {
	for k, v := range other {
		if v > vc[k] {
			vc[k] = v
		}
	}
}

// Compare 比较两个向量时钟的因果关系。
func Compare(a, b VectorClock) Relation {
	less, greater := false, false
	keys := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		keys[k] = struct{}{}
	}
	for k := range b {
		keys[k] = struct{}{}
	}
	for k := range keys {
		av, bv := a[k], b[k]
		if av < bv {
			less = true
		} else if av > bv {
			greater = true
		}
	}
	switch {
	case less && greater:
		return Concurrent
	case less:
		return Before
	case greater:
		return After
	default:
		return Equal
	}
}

// HappensBefore 报告 vc 是否因果先于 other。
func (vc VectorClock) HappensBefore(other VectorClock) bool {
	return Compare(vc, other) == Before
}

func (vc VectorClock) String() string {
	keys := make([]string, 0, len(vc))
	for k := range vc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", k, vc[k]))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}
