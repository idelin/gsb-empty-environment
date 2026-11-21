package admission

import "slices"

// intervalMap 是只保存边界的分段常量映射：segments 有序、互不重叠且完整覆盖
// [0, MaxPage]。页数为 2^48，因此严禁按页展开；所有操作复杂度只与边界数相关。
type intervalMap[T comparable] struct {
	bounds []uint64 // 每段起始页，bounds[0] 必须为 0
	values []T      // 与 bounds 等长，values[i] 作用于 [bounds[i], bounds[i+1)-1]
}

func newIntervalMap[T comparable](base T) *intervalMap[T] {
	return &intervalMap[T]{
		bounds: []uint64{0},
		values: []T{base},
	}
}

// segmentIndex 返回包含页 p 的段下标。
func (m *intervalMap[T]) segmentIndex(p uint64) int {
	idx, _ := slices.BinarySearch(m.bounds, p)
	if idx == len(m.bounds) || m.bounds[idx] > p {
		idx--
	}
	return idx
}

// get 取单页当前值。
func (m *intervalMap[T]) get(p uint64) T {
	return m.values[m.segmentIndex(p)]
}

// split 在 p 处插入边界（若已存在则不变），返回 p 所在段下标。
func (m *intervalMap[T]) split(p uint64) int {
	idx, found := slices.BinarySearch(m.bounds, p)
	if found {
		return idx
	}
	// 插入前，p 落在原第 idx-1 段；新段 idx 必须继承该段的值。
	inherited := m.values[idx-1]
	m.bounds = slices.Insert(m.bounds, idx, p)
	var zero T
	m.values = slices.Insert(m.values, idx, zero)
	m.values[idx] = inherited
	return idx
}

// overlay 将 [r.Start, r.End] 内每一段替换为 value，相邻等值段自动合并。
func (m *intervalMap[T]) overlay(r Range, value T) {
	startIdx := m.split(r.Start)
	endCut := r.End + 1 // r.End < MaxPage 的前提由调用方保证，否则在 MaxPage 截断
	if r.End == MaxPage {
		endCut = 0
	}
	if endCut != 0 {
		m.split(endCut)
	}
	for i := startIdx; i < len(m.bounds); i++ {
		if endCut != 0 && m.bounds[i] >= endCut {
			break
		}
		m.values[i] = value
	}
	m.compact()
}

// modifySegment 对包含页 p 的整段应用 fn，用于只更新状态中的某个字段。
func (m *intervalMap[T]) modifySegment(p uint64, fn func(T) T) {
	idx := m.segmentIndex(p)
	m.values[idx] = fn(m.values[idx])
}

// compact 合并相邻且等值的段，保持边界最小化。
func (m *intervalMap[T]) compact() {
	if len(m.bounds) <= 1 {
		return
	}
	write := 1
	for read := 1; read < len(m.bounds); read++ {
		if m.values[read] == m.values[write-1] {
			continue
		}
		if write != read {
			m.bounds[write] = m.bounds[read]
			m.values[write] = m.values[read]
		}
		write++
	}
	m.bounds = m.bounds[:write]
	m.values = m.values[:write]
}

// clone 深拷贝，供 DAG 分支各持独立状态。
func (m *intervalMap[T]) clone() *intervalMap[T] {
	return &intervalMap[T]{
		bounds: slices.Clone(m.bounds),
		values: slices.Clone(m.values),
	}
}

// boundaries 返回与 cut 集合合并后的全部段边界，保证所有历史在同一组
// 初等区间上被逐段对照，避免跨历史拼接签名范围。
func (m *intervalMap[T]) boundaries(cut map[uint64]struct{}) {
	for _, b := range m.bounds {
		cut[b] = struct{}{}
	}
}
