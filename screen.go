package raster

// screenTri 是完成透视除法、视口变换与透视插值预计算后的三角形。
type screenTri struct {
	edges [3]edge
	area  fixed // 规整后的正面积（边函数单位）
	// 三角形像素包围盒（像素坐标，闭区间），已与画面求交。
	minPx, minPy, maxPx, maxPy int

	attrCount int
	// 透视正确插值量：屏幕重心权重乘上对应顶点的 1/w、z/w、attr/w。
	invW0, invW1, invW2       float64
	zOverW0, zOverW1, zOverW2 float64
	attrOverW                 [3][]float64
}

// buildScreenTri 执行透视除法、视口变换、定点化与插值量预计算。
func (r *Renderer) buildScreenTri(fb *Framebuffer, v [3]Vertex) *screenTri {
	var sx, sy [3]fixed
	var invW, zOverW [3]float64
	for i := 0; i < 3; i++ {
		w := v[i].Pos.W
		if w <= 0 {
			// 近平面裁剪保证 w>0；防御性拒绝不可投影的顶点。
			return nil
		}
		invW[i] = 1 / w
		zOverW[i] = v[i].Pos.Z * invW[i]

		ndcX := v[i].Pos.X / w
		ndcY := v[i].Pos.Y / w
		// NDC [-1,1] 映射到像素中心 [0,width)/[0,height)。
		px := (ndcX + 1) * 0.5 * float64(fb.Width)
		py := (ndcY + 1) * 0.5 * float64(fb.Height)
		sx[i] = toFixed(px)
		sy[i] = toFixed(py)
	}

	// 屏幕有向面积的两倍。y 轴向下时 CW 三角形面积为正；统一规整为
	// CW（面积为正），使两种输入绕序都能光栅化，且边方向与左上规则
	// 的顶边/左边判定保持一致，边函数在内部严格为正。
	area := edgeValue(sx[0], sy[0], sx[1], sy[1], sx[2], sy[2])
	if area == 0 {
		return nil
	}
	if area < 0 {
		sx[1], sx[2] = sx[2], sx[1]
		sy[1], sy[2] = sy[2], sy[1]
		invW[1], invW[2] = invW[2], invW[1]
		zOverW[1], zOverW[2] = zOverW[2], zOverW[1]
		v[1], v[2] = v[2], v[1]
		area = -area
	}

	t := &screenTri{
		area:      area,
		attrCount: r.cfg.AttrCount,
		invW0:     invW[0], invW1: invW[1], invW2: invW[2],
		zOverW0: zOverW[0], zOverW1: zOverW[1], zOverW2: zOverW[2],
	}

	// 边 i 与顶点 i 相对：边 (i+1)->(i+2)，其边函数值用于顶点 i 的权重。
	for i := 0; i < 3; i++ {
		a := (i + 1) % 3
		b := (i + 2) % 3
		t.edges[i] = newEdge(sx[a], sy[a], sx[b], sy[b])
	}

	// 三角形包围盒（像素），与画面求交；完全在画面外则丢弃。
	minSubX := minF(minF(sx[0], sx[1]), sx[2])
	maxSubX := maxF(maxF(sx[0], sx[1]), sx[2])
	minSubY := minF(minF(sy[0], sy[1]), sy[2])
	maxSubY := maxF(maxF(sy[0], sy[1]), sy[2])
	// 像素 px 的中心定点坐标为 px*subScale + subScale/2，因此包围盒
	// 边界换算到像素索引时以 subScale/2 为偏移、subScale 为步长。
	t.minPx = int(floorDiv(minSubX-subScale/2, subScale))
	t.maxPx = int(floorDiv(maxSubX-subScale/2, subScale))
	t.minPy = int(floorDiv(minSubY-subScale/2, subScale))
	t.maxPy = int(floorDiv(maxSubY-subScale/2, subScale))
	t.minPx = maxInt(t.minPx, 0)
	t.minPy = maxInt(t.minPy, 0)
	t.maxPx = minInt(t.maxPx, fb.Width-1)
	t.maxPy = minInt(t.maxPy, fb.Height-1)
	if t.minPx > t.maxPx || t.minPy > t.maxPy {
		return nil
	}

	for i := 0; i < 3; i++ {
		t.attrOverW[i] = make([]float64, r.cfg.AttrCount)
		for k := 0; k < r.cfg.AttrCount; k++ {
			t.attrOverW[i][k] = v[i].Attr[k] * invW[i]
		}
	}
	return t
}
