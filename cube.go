// Package taxiheat 实现流式人流/车流时空密度分析器。
//
// 将地理空间按瓦片网格离散化，按小时维护时空立方体（Data Cube），
// 支持流式轨迹点快速落桶累加，并可在任意时空窗口内查询 Top-K 拥堵热点。
//
// 内存模型：每个小时一层 float32 稠密网格，
// 总内存 = 小时层数 × NX × NY × 4 字节，与导入点数无关（严格有界）。
package taxiheat

import (
	"container/heap"
	"fmt"
	"math"
	"time"
)

// Grid 定义等经纬度瓦片网格。
type Grid struct {
	MinLat  float64 // 网格南边界（度）
	MinLng  float64 // 网格西边界（度）
	CellDeg float64 // 单元格边长（度）
	NX      int     // 经度方向格数
	NY      int     // 纬度方向格数
}

// NewGrid 构造网格。cellDeg 必须为正，nx、ny 必须大于 0。
func NewGrid(minLat, minLng, cellDeg float64, nx, ny int) (Grid, error) {
	if cellDeg <= 0 {
		return Grid{}, fmt.Errorf("cellDeg must be positive, got %v", cellDeg)
	}
	if nx <= 0 || ny <= 0 {
		return Grid{}, fmt.Errorf("grid dimensions must be positive, got %dx%d", nx, ny)
	}
	return Grid{MinLat: minLat, MinLng: minLng, CellDeg: cellDeg, NX: nx, NY: ny}, nil
}

// Cell 返回经纬度所属单元格的一维索引；越界返回 ok=false。
func (g Grid) Cell(lat, lng float64) (idx int, ok bool) {
	ix := int(math.Floor((lng - g.MinLng) / g.CellDeg))
	iy := int(math.Floor((lat - g.MinLat) / g.CellDeg))
	if ix < 0 || ix >= g.NX || iy < 0 || iy >= g.NY {
		return 0, false
	}
	return iy*g.NX + ix, true
}

// Center 返回单元格中心经纬度。
func (g Grid) Center(idx int) (lat, lng float64) {
	ix := idx % g.NX
	iy := idx / g.NX
	return g.MinLat + (float64(iy)+0.5)*g.CellDeg,
		g.MinLng + (float64(ix)+0.5)*g.CellDeg
}

// Cells 返回网格总单元数。
func (g Grid) Cells() int { return g.NX * g.NY }

func hourKey(t time.Time) int64 { return t.Unix() / 3600 }

// Cube 是时空数据立方体：小时 -> 稠密权重网格层。
type Cube struct {
	Grid   Grid
	layers map[int64][]float32
	total  uint64 // 成功落桶的点数（含权重累加次数）
	drops  uint64 // 越界丢弃的点数
}

// NewCube 构造立方体。
func NewCube(g Grid) *Cube {
	return &Cube{Grid: g, layers: make(map[int64][]float32)}
}

// Add 将一个轨迹点按权重 w 累加进对应 (小时, 网格) 桶。
// 越界点被丢弃并计数。返回是否成功落桶。
func (c *Cube) Add(lat, lng float64, t time.Time, w float32) bool {
	idx, ok := c.Grid.Cell(lat, lng)
	if !ok {
		c.drops++
		return false
	}
	key := hourKey(t)
	layer, exists := c.layers[key]
	if !exists {
		layer = make([]float32, c.Grid.Cells())
		c.layers[key] = layer
	}
	layer[idx] += w
	c.total++
	return true
}

// Stats 返回统计信息：落桶点数、丢弃点数、小时层数、估算内存字节数。
func (c *Cube) Stats() (added, dropped uint64, hours int, memBytes int64) {
	return c.total, c.drops, len(c.layers),
		int64(len(c.layers)) * int64(c.Grid.Cells()) * 4
}

// Hours 返回当前立方体内所有小时层的 Unix 小时键（无序）。
func (c *Cube) Hours() []int64 {
	keys := make([]int64, 0, len(c.layers))
	for k := range c.layers {
		keys = append(keys, k)
	}
	return keys
}

// Hotspot 表示一个热点网格。
type Hotspot struct {
	Idx    int     // 网格一维索引
	Lat    float64 // 网格中心纬度
	Lng    float64 // 网格中心经度
	Weight float64 // 窗口内累计权重
}

// Aggregate 将 [start, end) 时间窗口内所有小时层累加进 dst（长度须为 Grid.Cells()）。
// 返回参与聚合的小时层数。dst 会被先清零。
func (c *Cube) Aggregate(dst []float32, start, end time.Time) int {
	for i := range dst {
		dst[i] = 0
	}
	lo, hi := hourKey(start), hourKey(end.Add(-1)) // [start,end) 覆盖的小时键闭区间
	n := 0
	for key := lo; key <= hi; key++ {
		if layer, ok := c.layers[key]; ok {
			for i, v := range layer {
				dst[i] += v
			}
			n++
		}
	}
	return n
}

// hotspotHeap 是按权重的小顶堆，容量固定为 k。
type hotspotHeap []Hotspot

func (h hotspotHeap) Len() int            { return len(h) }
func (h hotspotHeap) Less(i, j int) bool  { return h[i].Weight < h[j].Weight }
func (h hotspotHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *hotspotHeap) Push(x interface{}) { *h = append(*h, x.(Hotspot)) }
func (h *hotspotHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// TopK 查询 [start, end) 时间窗口内累计权重最高的前 k 个网格。
// 结果按权重降序返回。k<=0 时返回空。
func (c *Cube) TopK(start, end time.Time, k int) []Hotspot {
	if k <= 0 {
		return nil
	}
	agg := make([]float32, c.Grid.Cells())
	c.Aggregate(agg, start, end)

	if k > len(agg) {
		k = len(agg)
	}
	h := &hotspotHeap{}
	heap.Init(h)
	for idx, w := range agg {
		if w <= 0 {
			continue
		}
		if h.Len() < k {
			heap.Push(h, Hotspot{Idx: idx, Weight: float64(w)})
		} else if float64(w) > (*h)[0].Weight {
			(*h)[0] = Hotspot{Idx: idx, Weight: float64(w)}
			heap.Fix(h, 0)
		}
	}
	out := make([]Hotspot, h.Len())
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = heap.Pop(h).(Hotspot)
	}
	for i := range out {
		out[i].Lat, out[i].Lng = c.Grid.Center(out[i].Idx)
	}
	return out
}
