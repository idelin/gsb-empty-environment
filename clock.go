package leaseaudit

import (
	"fmt"
	"sort"
	"strings"
)

// VectorClock 向量时钟，用于判定事件间的 happens-before 因果关系。
type VectorClock map[string]uint64

// Copy 返回向量时钟的深拷贝。
func (vc VectorClock) Copy() VectorClock {
	out := make(VectorClock, len(vc))
	for k, v := range vc {
		out[k] = v
	}
	return out
}

// Tick 将本节点的逻辑计数加一。
func (vc VectorClock) Tick(node string) {
	vc[node]++
}

// Merge 合并来自其他节点的向量时钟（逐分量取最大值）。
func (vc VectorClock) Merge(other VectorClock) {
	for k, v := range other {
		if v > vc[k] {
			vc[k] = v
		}
	}
}

func (vc VectorClock) counter(node string) uint64 {
	return vc[node]
}

// HappensBefore 判定 a 是否在因果上先于 b（a <= b 且 a != b）。
func HappensBefore(a, b VectorClock) bool {
	less := false
	for k, av := range a {
		if av > b.counter(k) {
			return false
		}
		if av < b.counter(k) {
			less = true
		}
	}
	for k, bv := range b {
		if _, ok := a[k]; !ok && bv > 0 {
			less = true
		}
	}
	return less
}

// Concurrent 判定两个事件是否因果并发（互不 happens-before），
// 这是竞态的严格判据。
func Concurrent(a, b VectorClock) bool {
	return !HappensBefore(a, b) && !HappensBefore(b, a)
}

// String 以确定性顺序输出向量时钟，便于审计日志比对。
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
	return "{" + strings.Join(parts, " ") + "}"
}
