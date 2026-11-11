package raster

// 近平面裁剪。
//
// 裁剪在裁剪空间（齐次坐标）进行，保留满足 w >= -z（即 NDC z/w >= -1）
// 的部分，对应近平面 z_ndc = -1。只裁剪近平面：远平面与 x/y 方向由
// 视口包围盒自然裁掉。所有顶点输出（位置、深度、属性）都按交点参数
// t 线性插值，透视除法留到光栅化阶段统一处理。

// Vertex 是一个裁剪空间顶点。
// Pos 为齐次位置 (x,y,z,w)，Attr 为需要透视插值的顶点属性。
type Vertex struct {
	Pos  Vec4
	Attr []float64
}

// Vec4 是齐次坐标向量。
type Vec4 struct {
	X, Y, Z, W float64
}

// clipNear 对单个三角形做近平面裁剪，返回 0、1 或 2 个三角形的顶点。
func clipNear(verts [3]Vertex, attrCount int) [][3]Vertex {
	inside := [3]bool{}
	dist := [3]float64{} // 到近平面的有向距离 w+z
	for i := range verts {
		d := verts[i].Pos.W + verts[i].Pos.Z
		dist[i] = d
		inside[i] = d >= 0
	}

	inCount := 0
	for _, ok := range inside {
		if ok {
			inCount++
		}
	}

	switch inCount {
	case 3:
		return [][3]Vertex{verts}
	case 0:
		return nil
	case 1:
		var inIdx int
		for i := range inside {
			if inside[i] {
				inIdx = i
			}
		}
		prev := (inIdx + 2) % 3
		next := (inIdx + 1) % 3
		a := verts[inIdx]
		b := intersectNear(a, verts[prev], dist[inIdx], dist[prev], attrCount)
		c := intersectNear(a, verts[next], dist[inIdx], dist[next], attrCount)
		return [][3]Vertex{{a, b, c}}
	default: // 两个顶点在内：裁剪后为四边形，按扇形拆成两个三角形
		var outIdx int
		for i := range inside {
			if !inside[i] {
				outIdx = i
			}
		}
		prev := (outIdx + 2) % 3
		next := (outIdx + 1) % 3
		p := intersectNear(verts[prev], verts[outIdx], dist[prev], dist[outIdx], attrCount)
		n := intersectNear(verts[next], verts[outIdx], dist[next], dist[outIdx], attrCount)
		return [][3]Vertex{
			{verts[prev], verts[next], p},
			{p, verts[next], n},
		}
	}
}

// intersectNear 返回线段 a(inside)->b(outside) 与近平面的交点。
// da、db 为两端的有向距离，da >= 0 且 db < 0。
func intersectNear(a, b Vertex, da, db float64, attrCount int) Vertex {
	t := da / (da - db)
	out := Vertex{
		Pos:  lerpVec4(a.Pos, b.Pos, t),
		Attr: make([]float64, attrCount),
	}
	for k := 0; k < attrCount; k++ {
		out.Attr[k] = a.Attr[k] + t*(b.Attr[k]-a.Attr[k])
	}
	return out
}

func lerpVec4(a, b Vec4, t float64) Vec4 {
	return Vec4{
		X: a.X + t*(b.X-a.X),
		Y: a.Y + t*(b.Y-a.Y),
		Z: a.Z + t*(b.Z-a.Z),
		W: a.W + t*(b.W-a.W),
	}
}
