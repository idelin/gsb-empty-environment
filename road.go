package floodsim

import "math"

// Road 为折线道路，顶点使用栅格平面坐标。
type Road struct {
	Name   string
	Points []Point
}

// Breakpoint 描述道路被水体阻断的位置。
type Breakpoint struct {
	Road  string
	Tick  int
	Enter Point // 进入淹没区的位置
	Exit  Point // 离开淹没区的位置（若路段末端仍淹没则为道路端点）
	Cell  int   // 进入点所在栅格单元索引
}

// RoadNetwork 为道路集合。
type RoadNetwork struct {
	Roads []Road
}

// sampleStep 为折线采样步长（栅格单元边长的分数）。
const sampleStep = 0.25

// BreakpointsAt 返回历史时刻 t 所有道路被淹没的断点列表。
// 沿每条道路折线采样，连续淹没采样段构成一个断点。
func (s *Simulator) BreakpointsAt(t int, net *RoadNetwork) []Breakpoint {
	snap := s.SnapshotAt(t)
	if snap == nil {
		return nil
	}
	var out []Breakpoint
	for _, road := range net.Roads {
		out = append(out, roadBreakpoints(s.Grid, snap.Depth, s.FloodThreshold, t, road)...)
	}
	return out
}

func roadBreakpoints(g *Grid, depth []float64, threshold float64, tick int, road Road) []Breakpoint {
	var bps []Breakpoint
	var cur *Breakpoint
	floodedAt := func(p Point) bool {
		i := g.At(p.X, p.Y)
		return i >= 0 && depth[i] > threshold
	}
	visit := func(p Point) {
		if floodedAt(p) {
			if cur == nil {
				cur = &Breakpoint{Road: road.Name, Tick: tick, Enter: p, Exit: p, Cell: g.At(p.X, p.Y)}
			} else {
				cur.Exit = p
			}
		} else if cur != nil {
			bps = append(bps, *cur)
			cur = nil
		}
	}
	for k := 0; k+1 < len(road.Points); k++ {
		a, b := road.Points[k], road.Points[k+1]
		dx, dy := b.X-a.X, b.Y-a.Y
		length := math.Hypot(dx, dy)
		steps := int(length/sampleStep) + 1
		for sIdx := 0; sIdx < steps; sIdx++ {
			f := float64(sIdx) / float64(steps)
			visit(Point{a.X + dx*f, a.Y + dy*f})
		}
	}
	if len(road.Points) > 0 {
		visit(road.Points[len(road.Points)-1])
	}
	if cur != nil {
		bps = append(bps, *cur)
	}
	return bps
}
