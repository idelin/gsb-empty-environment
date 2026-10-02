package stbvh

// Point4 是时空采样点：X, Y, Z(高度), T(Unix 秒)。
type Point4 = [NDim]float64

// Segment 是一条轨迹线段（两个采样点之间的线性插值段）。
type Segment struct {
	TrajID int    // 所属轨迹 ID
	Index  int    // 在轨迹中的段序号
	P0, P1 Point4 // 起点 / 终点（P0[T] <= P1[T]）
	box    Box4   // 预计算的 4D AABB
}

// Box 返回该线段的 4D 包围盒。
func (s *Segment) Box() Box4 { return s.box }

// Trajectory 是一条按时间排序的采样点序列。
type Trajectory struct {
	ID     int
	Points []Point4
}

// Segments 将轨迹拆分为线段，跳过零时长或非法（时间倒退）的段。
func (tr *Trajectory) Segments() []Segment {
	var segs []Segment
	for i := 0; i+1 < len(tr.Points); i++ {
		p0, p1 := tr.Points[i], tr.Points[i+1]
		if p1[DimT] <= p0[DimT] {
			continue
		}
		segs = append(segs, Segment{
			TrajID: tr.ID,
			Index:  i,
			P0:     p0,
			P1:     p1,
			box:    NewBox(p0, p1),
		})
	}
	return segs
}

// Hit 是一次查询命中：命中的线段 + 裁剪到查询时间窗后的精确片段。
type Hit struct {
	Segment Segment
	// Clip0/Clip1 为线段被查询时间窗 [t1,t2] 裁剪后的端点（线性插值）。
	Clip0, Clip1 Point4
}

// clipToTimeWindow 将线段按 [t1, t2] 线性裁剪，返回裁剪后的两端点。
// 调用前需保证线段与查询盒已相交。
func (s *Segment) clipToTimeWindow(t1, t2 float64) (Point4, Point4) {
	ta, tb := s.P0[DimT], s.P1[DimT]
	lo, hi := maxF(ta, t1), minF(tb, t2)
	span := tb - ta
	at := func(t float64) Point4 {
		var p Point4
		u := (t - ta) / span
		for d := 0; d < NDim; d++ {
			p[d] = s.P0[d] + u*(s.P1[d]-s.P0[d])
		}
		p[DimT] = t
		return p
	}
	return at(lo), at(hi)
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
