package spacetime

import "math"

// segRef 指向某条轨迹的第 seg 个线段(Samples[seg] -> Samples[seg+1])。
type segRef struct {
	traj, seg int
}

// bucketKey 倒排桶键:时间切片编号 + 三维网格单元坐标。
type bucketKey struct {
	slice      int64
	cx, cy, cz int32
}

// GridIndex 时间切片倒排网格索引。
//
// 将每条轨迹的每个线段,按其时间跨度落入的每个时间切片、
// 及其空间包围盒覆盖的每个网格单元,登记进倒排表:
//
//	(时间切片, 网格单元) -> [线段引用...]
//
// 查询时只需把实体 A 的线段包围盒外扩阈值 r,
// 在对应桶中收集实体 B 的候选线段对,再做精确求交,
// 避免 O(N*M) 的全量两两比较。
type GridIndex struct {
	sliceDur int64   // 时间切片宽度(秒)
	cellSize float64 // 网格单元边长(米)
	trajs    []*Trajectory
	buckets  map[bucketKey][]segRef
}

// NewGridIndex 创建索引;sliceDur 为时间切片宽度(秒),cellSize 为网格边长(米)。
func NewGridIndex(sliceDur int64, cellSize float64) *GridIndex {
	return &GridIndex{
		sliceDur: sliceDur,
		cellSize: cellSize,
		buckets:  make(map[bucketKey][]segRef),
	}
}

// Build 将轨迹登记入索引,返回其在索引内部的编号(供 Prove/Contacts 使用)。
func (ix *GridIndex) Build(trajs ...*Trajectory) []int {
	ids := make([]int, 0, len(trajs))
	for _, tr := range trajs {
		ti := len(ix.trajs)
		ix.trajs = append(ix.trajs, tr)
		for si := 0; si+1 < len(tr.Samples); si++ {
			ix.insert(ti, si, tr.Samples[si], tr.Samples[si+1], 0)
		}
		ids = append(ids, ti)
	}
	return ids
}

// Traj 按内部编号取回轨迹。
func (ix *GridIndex) Traj(i int) *Trajectory { return ix.trajs[i] }

func (ix *GridIndex) cellOf(v float64) int32 {
	return int32(math.Floor(v / ix.cellSize))
}

func (ix *GridIndex) insert(ti, si int, a, b Sample, pad float64) {
	s0, s1 := a.T/ix.sliceDur, b.T/ix.sliceDur
	minX := math.Min(a.P.X, b.P.X) - pad
	maxX := math.Max(a.P.X, b.P.X) + pad
	minY := math.Min(a.P.Y, b.P.Y) - pad
	maxY := math.Max(a.P.Y, b.P.Y) + pad
	minZ := math.Min(a.P.Z, b.P.Z) - pad
	maxZ := math.Max(a.P.Z, b.P.Z) + pad
	for s := s0; s <= s1; s++ {
		for cx := ix.cellOf(minX); cx <= ix.cellOf(maxX); cx++ {
			for cy := ix.cellOf(minY); cy <= ix.cellOf(maxY); cy++ {
				for cz := ix.cellOf(minZ); cz <= ix.cellOf(maxZ); cz++ {
					k := bucketKey{s, cx, cy, cz}
					ix.buckets[k] = append(ix.buckets[k], segRef{ti, si})
				}
			}
		}
	}
}

// candidatePairs 通过倒排索引收集 (A 线段, B 线段) 候选对。
// A 的每个线段包围盒外扩 radius 后,与 B 落在同一 (切片, 单元) 桶中的线段即为候选。
func (ix *GridIndex) candidatePairs(ai, bi int, radius float64) [][2]int {
	tr := ix.trajs[ai]
	seen := make(map[[2]int]struct{})
	var pairs [][2]int
	for si := 0; si+1 < len(tr.Samples); si++ {
		a, b := tr.Samples[si], tr.Samples[si+1]
		s0, s1 := a.T/ix.sliceDur, b.T/ix.sliceDur
		minX := math.Min(a.P.X, b.P.X) - radius
		maxX := math.Max(a.P.X, b.P.X) + radius
		minY := math.Min(a.P.Y, b.P.Y) - radius
		maxY := math.Max(a.P.Y, b.P.Y) + radius
		minZ := math.Min(a.P.Z, b.P.Z) - radius
		maxZ := math.Max(a.P.Z, b.P.Z) + radius
		for s := s0; s <= s1; s++ {
			for cx := ix.cellOf(minX); cx <= ix.cellOf(maxX); cx++ {
				for cy := ix.cellOf(minY); cy <= ix.cellOf(maxY); cy++ {
					for cz := ix.cellOf(minZ); cz <= ix.cellOf(maxZ); cz++ {
						for _, ref := range ix.buckets[bucketKey{s, cx, cy, cz}] {
							if ref.traj != bi {
								continue
							}
							p := [2]int{si, ref.seg}
							if _, ok := seen[p]; !ok {
								seen[p] = struct{}{}
								pairs = append(pairs, p)
							}
						}
					}
				}
			}
		}
	}
	return pairs
}
