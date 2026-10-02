package spacetime

import (
	"math"
	"sort"
)

// cellKey 是空间网格单元坐标。
type cellKey struct {
	ix, iy int
}

// GridIndex 是时间切片倒排网格表:
//
//	时间切片 -> 空间网格单元 -> 该切片内经过该单元的实体集合
//
// 时间被切分为固定长度的切片，空间被划分为固定边长的网格。
// 查询时只需检查两实体共同出现(或相邻出现)的切片，
// 从而把精确求交的计算量裁剪到候选时间窗内。
type GridIndex struct {
	sliceDur float64 // 时间切片长度(秒)
	cellSize float64 // 网格边长(米)
	slices   map[int64]map[cellKey]map[string]struct{}
}

// NewGridIndex 创建索引。sliceDur 为时间切片长度(秒),
// cellSize 为空间网格边长(米);cellSize 不小于常用查询阈值时
// 检索效率最佳。
func NewGridIndex(sliceDur, cellSize float64) *GridIndex {
	return &GridIndex{
		sliceDur: sliceDur,
		cellSize: cellSize,
		slices:   make(map[int64]map[cellKey]map[string]struct{}),
	}
}

// Add 把一条轨迹写入索引(按采样段覆盖的时间切片与网格单元)。
func (g *GridIndex) Add(tr *Trajectory) {
	for i := 0; i+1 < len(tr.Samples); i++ {
		s0, s1 := tr.Samples[i], tr.Samples[i+1]
		first := int64(math.Floor(s0.T / g.sliceDur))
		last := int64(math.Floor(s1.T / g.sliceDur))
		minX, maxX := math.Min(s0.X, s1.X), math.Max(s0.X, s1.X)
		minY, maxY := math.Min(s0.Y, s1.Y), math.Max(s0.Y, s1.Y)
		cx0 := int(math.Floor(minX / g.cellSize))
		cx1 := int(math.Floor(maxX / g.cellSize))
		cy0 := int(math.Floor(minY / g.cellSize))
		cy1 := int(math.Floor(maxY / g.cellSize))
		for slice := first; slice <= last; slice++ {
			cells := g.slices[slice]
			if cells == nil {
				cells = make(map[cellKey]map[string]struct{})
				g.slices[slice] = cells
			}
			for ix := cx0; ix <= cx1; ix++ {
				for iy := cy0; iy <= cy1; iy++ {
					key := cellKey{ix, iy}
					set := cells[key]
					if set == nil {
						set = make(map[string]struct{})
						cells[key] = set
					}
					set[tr.EntityID] = struct{}{}
				}
			}
		}
	}
}

// CandidateRanges 返回实体 a、b 可能相距小于 threshold 的
// 候选时间区间(已合并相邻切片)。只要某时刻两实体距离小于阈值，
// 该时刻所在切片必然命中候选，因此裁剪不会漏报。
func (g *GridIndex) CandidateRanges(a, b string, threshold float64) [][2]float64 {
	ring := int(math.Ceil(threshold / g.cellSize))
	var hits []int64
	for slice, cells := range g.slices {
		if sliceHit(cells, a, b, ring) {
			hits = append(hits, slice)
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i] < hits[j] })

	var ranges [][2]float64
	for _, slice := range hits {
		start := float64(slice) * g.sliceDur
		end := start + g.sliceDur
		if n := len(ranges); n > 0 && start <= ranges[n-1][1] {
			ranges[n-1][1] = end
		} else {
			ranges = append(ranges, [2]float64{start, end})
		}
	}
	return ranges
}

// sliceHit 判断单个时间切片内，实体 a、b 的网格单元是否
// 处于 ring 邻域之内。
func sliceHit(cells map[cellKey]map[string]struct{}, a, b string, ring int) bool {
	for key, set := range cells {
		if _, ok := set[a]; !ok {
			continue
		}
		for dx := -ring; dx <= ring; dx++ {
			for dy := -ring; dy <= ring; dy++ {
				near, ok := cells[cellKey{key.ix + dx, key.iy + dy}]
				if !ok {
					continue
				}
				if _, ok := near[b]; ok {
					return true
				}
			}
		}
	}
	return false
}
