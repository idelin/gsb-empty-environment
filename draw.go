package raster

import "sync"

// Draw 光栅化一批三角形（每个三角形为连续 3 个顶点）。
//
// 顶点处于裁剪空间。每个三角形先做近平面裁剪，再透视除法、视口变换
// 与定点化，最后按图块调度。串行与并行模式共用同一个逐像素内核，
// 因此结果逐位一致。
func (r *Renderer) Draw(fb *Framebuffer, verts []Vertex) error {
	if len(verts)%3 != 0 {
		return errConfig("vertex count must be a multiple of 3")
	}
	for i := range verts {
		if len(verts[i].Attr) != r.cfg.AttrCount {
			return errConfig("vertex attribute count does not match Config.AttrCount")
		}
	}

	tris := make([]*screenTri, 0, len(verts)/3)
	for base := 0; base < len(verts); base += 3 {
		var in [3]Vertex
		copy(in[:], verts[base:base+3])
		for _, clipped := range clipNear(in, r.cfg.AttrCount) {
			if tri := r.buildScreenTri(fb, clipped); tri != nil {
				tris = append(tris, tri)
			}
		}
	}

	if r.workers <= 1 {
		r.drawSerial(fb, tris)
	} else {
		r.drawTiled(fb, tris)
	}
	return nil
}

// SetShader 替换片段着色器，nil 恢复默认（输出第一个属性）。
func (r *Renderer) SetShader(s Shader) {
	r.shader = s
}

// drawSerial 按提交顺序直接光栅化全部三角形。
func (r *Renderer) drawSerial(fb *Framebuffer, tris []*screenTri) {
	for _, t := range tris {
		r.rasterRect(fb, t, t.minPx, t.minPy, t.maxPx, t.maxPy)
	}
}

// tileJob 是一个图块与其相交三角形的配对工作。
type tileJob struct {
	x0, y0, x1, y1 int
	tris           []*screenTri
}

// drawTiled 先串行分箱（无共享写入），再按图块并行光栅化。
//
// 图块之间像素不重叠，各 goroutine 写入帧缓冲的位置互不相交，无需
// 锁或原子操作；同一图块内严格按提交顺序处理三角形，深度语义与串行
// 模式完全相同。
func (r *Renderer) drawTiled(fb *Framebuffer, tris []*screenTri) {
	ts := r.cfg.TileSize
	cols := (fb.Width + ts - 1) / ts
	rows := (fb.Height + ts - 1) / ts
	jobs := make([]tileJob, 0, cols*rows)
	for ty := 0; ty < rows; ty++ {
		for tx := 0; tx < cols; tx++ {
			x0 := tx * ts
			y0 := ty * ts
			x1 := minInt(x0+ts-1, fb.Width-1)
			y1 := minInt(y0+ts-1, fb.Height-1)
			job := tileJob{x0: x0, y0: y0, x1: x1, y1: y1}
			for _, t := range tris {
				if t.maxPx >= x0 && t.minPx <= x1 && t.maxPy >= y0 && t.minPy <= y1 {
					job.tris = append(job.tris, t)
				}
			}
			if len(job.tris) > 0 {
				jobs = append(jobs, job)
			}
		}
	}

	workers := r.workers
	if workers > len(jobs) {
		workers = len(jobs)
	}
	if workers <= 1 {
		for _, job := range jobs {
			r.rasterJob(fb, job)
		}
		return
	}

	ch := make(chan tileJob)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range ch {
				r.rasterJob(fb, job)
			}
		}()
	}
	for _, job := range jobs {
		ch <- job
	}
	close(ch)
	wg.Wait()
}

func (r *Renderer) rasterJob(fb *Framebuffer, job tileJob) {
	for _, t := range job.tris {
		x0 := maxInt(t.minPx, job.x0)
		y0 := maxInt(t.minPy, job.y0)
		x1 := minInt(t.maxPx, job.x1)
		y1 := minInt(t.maxPy, job.y1)
		r.rasterRect(fb, t, x0, y0, x1, y1)
	}
}

// rasterRect 在给定像素矩形内运行逐像素内核。串行与并行唯一的差别是
// 调用时传入的矩形范围，覆盖判定与着色逻辑完全共用。
func (r *Renderer) rasterRect(fb *Framebuffer, t *screenTri, x0, y0, x1, y1 int) {
	attr := make([]float64, t.attrCount)
	originX := fixed(x0 * subScale)
	originY := fixed(y0 * subScale)

	var start [3]fixed
	for i := 0; i < 3; i++ {
		e := &t.edges[i]
		// 像素中心 = 像素索引 * subScale + subScale/2。
		start[i] = e.a*(originX+subScale/2) + e.b*(originY+subScale/2) + e.c
	}

	invArea := 1 / float64(t.area)
	for py := y0; py <= y1; py++ {
		values := start
		for px := x0; px <= x1; px++ {
			inside := true
			for i := 0; i < 3; i++ {
				if !t.edges[i].inside(values[i]) {
					inside = false
					break
				}
			}
			if inside {
				r.shadePixel(fb, t, px, py, values, invArea, attr)
			}
			for i := 0; i < 3; i++ {
				// 横向移动一个像素（subScale 个定点单位）。
				values[i] += t.edges[i].a * subScale
			}
		}
		for i := 0; i < 3; i++ {
			// 纵向移动一个像素。
			start[i] += t.edges[i].b * subScale
		}
	}
}

// shadePixel 由定点边函数求屏幕重心权重，做透视正确的深度/属性插值，
// 执行可配置深度测试并写入帧缓冲。
func (r *Renderer) shadePixel(
	fb *Framebuffer, t *screenTri,
	px, py int, edge [3]fixed, invArea float64, attr []float64,
) {
	w0 := float64(edge[0]) * invArea
	w1 := float64(edge[1]) * invArea
	w2 := float64(edge[2]) * invArea

	// 透视正确插值：先在屏幕空间用重心权重聚合“量/w”，再除以聚合的 1/w。
	invW := w0*t.invW0 + w1*t.invW1 + w2*t.invW2
	depth := (w0*t.zOverW0 + w1*t.zOverW1 + w2*t.zOverW2) / invW

	idx := py*fb.Width + px
	if r.cfg.DepthTest && !r.cfg.Depth.pass(depth, fb.Depth[idx]) {
		return
	}

	for k := range attr {
		v := w0*t.attrOverW[0][k] + w1*t.attrOverW[1][k] + w2*t.attrOverW[2][k]
		attr[k] = v / invW
	}

	var color float64
	if r.shader != nil {
		color = r.shader(attr)
	} else {
		color = attr[0]
	}

	fb.Color[idx] = color
	if r.cfg.DepthWrite {
		fb.Depth[idx] = depth
	}
	fb.Samples[idx]++
}
