package netsec

import (
	"sort"
	"time"
)

// stringSet 是节点 ID 的无序集合，供增量传播在内部追踪受影响闭包。
type stringSet struct {
	m map[string]struct{}
}

func newStringSet() *stringSet {
	return &stringSet{m: make(map[string]struct{})}
}

func (s *stringSet) add(id string) bool {
	if _, ok := s.m[id]; ok {
		return false
	}
	s.m[id] = struct{}{}
	return true
}

func (s *stringSet) addAll(other *stringSet) {
	for id := range other.m {
		s.m[id] = struct{}{}
	}
}

func (s *stringSet) contains(id string) bool {
	_, ok := s.m[id]
	return ok
}

func (s *stringSet) len() int {
	return len(s.m)
}

func (s *stringSet) sorted() []string {
	return setToSorted(s.m)
}

// setToSorted 将 map 形式的集合转换为按字典序排列的切片，
// 保证推导与解释输出的确定性。
func setToSorted(set map[string]struct{}) []string {
	if len(set) == 0 {
		return []string{}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// uniqueSorted 对输入去重并排序；nil 输入返回空切片而非 nil。
func uniqueSorted(ids []string) []string {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return setToSorted(set)
}

// minTime 返回更早的期限。零值 time.Time 表示“不受约束（无限期）”，
// 因此任何非零期限都严格早于零值；两个零值取零值。
func minTime(a, b time.Time) time.Time {
	if a.IsZero() {
		return b
	}
	if b.IsZero() {
		return a
	}
	if a.Before(b) {
		return a
	}
	return b
}
