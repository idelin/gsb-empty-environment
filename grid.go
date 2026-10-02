package hsengine

import "math"

// cellKey 时空网格单元的哈希键：三维空间坐标 + 时隙编号。
type cellKey struct {
	X, Y, Z int
	Slot    int
}

// SpatioTemporalGrid 时空网格哈希表。
// 空间按 cellSize 划分立方体网格，时间按 slotDuration 分片，
// 每个桶中存放该时隙内扫过该空间单元的实体索引。
type SpatioTemporalGrid struct {
	cellSize     float64
	slotDuration float64
	cells        map[cellKey][]int
}

// NewSpatioTemporalGrid 创建时空网格。
func NewSpatioTemporalGrid(cellSize, slotDuration float64) *SpatioTemporalGrid {
	return &SpatioTemporalGrid{
		cellSize:     cellSize,
		slotDuration: slotDuration,
		cells:        make(map[cellKey][]int),
	}
}

// SlotDuration 返回时隙长度（秒）。
func (g *SpatioTemporalGrid) SlotDuration() float64 { return g.slotDuration }

// floorDiv 向下取整除法（支持负坐标）。
func floorDiv(v, size float64) int {
	return int(math.Floor(v / size))
}

// InsertCapsule 将实体 idx 在时隙 slot 内扫出的胶囊体
// （线段 p0->p1 外扩半径 radius）插入其覆盖的所有网格单元。
func (g *SpatioTemporalGrid) InsertCapsule(idx int, p0, p1 Vector3, radius float64, slot int) {
	minX := math.Min(p0.X, p1.X) - radius
	minY := math.Min(p0.Y, p1.Y) - radius
	minZ := math.Min(p0.Z, p1.Z) - radius
	maxX := math.Max(p0.X, p1.X) + radius
	maxY := math.Max(p0.Y, p1.Y) + radius
	maxZ := math.Max(p0.Z, p1.Z) + radius

	x0, x1 := floorDiv(minX, g.cellSize), floorDiv(maxX, g.cellSize)
	y0, y1 := floorDiv(minY, g.cellSize), floorDiv(maxY, g.cellSize)
	z0, z1 := floorDiv(minZ, g.cellSize), floorDiv(maxZ, g.cellSize)

	for x := x0; x <= x1; x++ {
		for y := y0; y <= y1; y++ {
			for z := z0; z <= z1; z++ {
				k := cellKey{X: x, Y: y, Z: z, Slot: slot}
				g.cells[k] = append(g.cells[k], idx)
			}
		}
	}
}

// Candidates 返回与给定胶囊体共享同一时空单元的实体索引（去重）。
// 网格哈希使该查询只访问常数个桶。
func (g *SpatioTemporalGrid) Candidates(p0, p1 Vector3, radius float64, slot int) []int {
	minX := math.Min(p0.X, p1.X) - radius
	minY := math.Min(p0.Y, p1.Y) - radius
	minZ := math.Min(p0.Z, p1.Z) - radius
	maxX := math.Max(p0.X, p1.X) + radius
	maxY := math.Max(p0.Y, p1.Y) + radius
	maxZ := math.Max(p0.Z, p1.Z) + radius

	x0, x1 := floorDiv(minX, g.cellSize), floorDiv(maxX, g.cellSize)
	y0, y1 := floorDiv(minY, g.cellSize), floorDiv(maxY, g.cellSize)
	z0, z1 := floorDiv(minZ, g.cellSize), floorDiv(maxZ, g.cellSize)

	seen := make(map[int]struct{})
	out := make([]int, 0, 8)
	for x := x0; x <= x1; x++ {
		for y := y0; y <= y1; y++ {
			for z := z0; z <= z1; z++ {
				k := cellKey{X: x, Y: y, Z: z, Slot: slot}
				for _, idx := range g.cells[k] {
					if _, ok := seen[idx]; !ok {
						seen[idx] = struct{}{}
						out = append(out, idx)
					}
				}
			}
		}
	}
	return out
}
