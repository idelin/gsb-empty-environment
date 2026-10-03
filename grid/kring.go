package grid

// K-Ring 拓扑邻域遍历。
//
// 全部计算在整数格点空间完成（Eisenstein 整数 / 轴向坐标），
// 不含任何浮点三角函数：邻居偏移为常量整数向量，
// 环距离为立方坐标切比雪夫距离。

// kRingOffsets 预生成各半径的环内偏移（含中心），按 (dq, dr) 字典序。
var kRingOffsets [16][]axial

func init() {
	for k := 0; k < len(kRingOffsets); k++ {
		var offs []axial
		for dq := -k; dq <= k; dq++ {
			lo, hi := -k, k
			if -dq-k > lo {
				lo = -dq - k
			}
			if -dq+k < hi {
				hi = -dq + k
			}
			for dr := lo; dr <= hi; dr++ {
				offs = append(offs, axial{int64(dq), int64(dr)})
			}
		}
		kRingOffsets[k] = offs
	}
}

// KRingSize 半径 k 环内的单元总数：1 + 3k(k+1)。
func KRingSize(k int) int { return 1 + 3*k*(k+1) }

// KRing 返回以 h 为中心、半径 k 环内的全部单元（含中心）。
// 结果按拓扑偏移排序，中心在前；大小为 1+3k(k+1)。
// 纯整数遍历，无浮点三角计算。
func KRing(h Index, k int) []Index {
	if k < 0 {
		return nil
	}
	if k >= len(kRingOffsets) {
		k = len(kRingOffsets) - 1
	}
	center := localAxial(h)
	res, bc := h.Res(), h.BaseCell()
	offs := kRingOffsets[k]
	out := make([]Index, 0, len(offs))
	for _, o := range offs {
		idx, err := axialToIndex(bc, res, axial{center.q + o.q, center.r + o.r})
		if err == nil {
			out = append(out, idx)
		}
	}
	return out
}

// Ring 返回恰好距离 k 环上的单元（k=0 时仅中心），共 6k 个。
func Ring(h Index, k int) []Index {
	if k < 0 {
		return nil
	}
	if k == 0 {
		return []Index{h}
	}
	center := localAxial(h)
	res, bc := h.Res(), h.BaseCell()
	out := make([]Index, 0, 6*k)
	// 沿六条边行走：从方向 5 的 k 步顶点出发，每边 k 步。
	cursor := axial{center.q + int64(k)*unitDirs[4].q, center.r + int64(k)*unitDirs[4].r}
	for side := 0; side < 6; side++ {
		d := unitDirs[side]
		for step := 0; step < k; step++ {
			if idx, err := axialToIndex(bc, res, cursor); err == nil {
				out = append(out, idx)
			}
			cursor.q += d.q
			cursor.r += d.r
		}
	}
	return out
}

// Neighbors 返回直接相邻的 6 个单元（1 环，不含中心）。
func Neighbors(h Index) []Index { return Ring(h, 1) }

// GridDistance 两单元间的拓扑环距离（同一基单元内，纯整数）。
// 跨基单元时返回 -1（简化实现：不做跨面折叠）。
func GridDistance(a, b Index) int {
	if a.BaseCell() != b.BaseCell() || a.Res() != b.Res() {
		return -1
	}
	ta, tb := localAxial(a), localAxial(b)
	return hexDistance(ta, tb)
}
