package deltacrdt

// Dot 唯一标识一次因果更新事件（站点ID + 单调递增计数器）。
type Dot struct {
	Site    uint64
	Counter uint64
}

// less 定义 RGA 兄弟节点排序：计数器大者优先，平局时站点ID大者优先。
func (d Dot) less(o Dot) bool {
	if d.Counter != o.Counter {
		return d.Counter < o.Counter
	}
	return d.Site < o.Site
}

// VersionVector 记录每个站点已知的最大计数器，即"确认水位"。
type VersionVector map[uint64]uint64

// Contains 判断一个 Dot 是否已被该水位覆盖。
func (vv VersionVector) Contains(d Dot) bool {
	return vv[d.Site] >= d.Counter
}

// Observe 将一个 Dot 并入水位。
func (vv VersionVector) Observe(d Dot) {
	if d.Counter > vv[d.Site] {
		vv[d.Site] = d.Counter
	}
}

// Merge 取两个水位的逐站点最大值。
func (vv VersionVector) Merge(other VersionVector) {
	for site, counter := range other {
		if counter > vv[site] {
			vv[site] = counter
		}
	}
}

// Clone 深拷贝水位。
func (vv VersionVector) Clone() VersionVector {
	out := make(VersionVector, len(vv))
	for site, counter := range vv {
		out[site] = counter
	}
	return out
}

// Equal 判断两个水位是否完全一致。
func (vv VersionVector) Equal(other VersionVector) bool {
	if len(vv) != len(other) {
		return false
	}
	for site, counter := range vv {
		if other[site] != counter {
			return false
		}
	}
	return true
}
