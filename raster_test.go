package raster

import (
	"math"
	"math/rand"
	"testing"
)

// cv 用 NDC 坐标与 w 构造裁剪空间顶点（Pos = ndc*w）。
func cv(ndcX, ndcY, ndcZ, w float64, attr ...float64) Vertex {
	return Vertex{
		Pos:  Vec4{X: ndcX * w, Y: ndcY * w, Z: ndcZ * w, W: w},
		Attr: attr,
	}
}

func newTestRenderer(t *testing.T, workers int) *Renderer {
	t.Helper()
	r, err := New(Config{
		AttrCount:  1,
		Depth:      DepthLess,
		DepthTest:  true,
		DepthWrite: true,
		TileSize:   7, // 故意取非整除尺寸，制造不规则图块
		Workers:    workers,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

// 左上填充规则：沿对角线拆开的四边形，每个像素恰好着色一次，
// 对角线与共享边上没有重复或遗漏。
func TestTopLeftFillRule(t *testing.T) {
	const w, h = 8, 8
	fb := NewFramebuffer(w, h, 1)
	r := newTestRenderer(t, 1)

	// 覆盖屏幕像素矩形 [2,5]x[2,5]，四个角落在像素边界上。
	tl := cv(-0.5, -0.5, 0, 1, 1)
	tr := cv(0.5, -0.5, 0, 1, 2)
	br := cv(0.5, 0.5, 0, 1, 3)
	bl := cv(-0.5, 0.5, 0, 1, 4)
	if err := r.Draw(fb, []Vertex{tl, br, bl, tl, tr, br}); err != nil {
		t.Fatalf("Draw: %v", err)
	}

	covered := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			got := fb.SamplesAt(x, y)
			inRect := x >= 2 && x <= 5 && y >= 2 && y <= 5
			if inRect {
				if got != 1 {
					t.Fatalf("pixel (%d,%d) shaded %d times, want exactly 1", x, y, got)
				}
				covered++
			} else if got != 0 {
				t.Fatalf("pixel (%d,%d) outside quad shaded %d times", x, y, got)
			}
		}
	}
	if covered != 16 || fb.TotalSamples() != 16 {
		t.Fatalf("coverage = %d (total %d), want 16", covered, fb.TotalSamples())
	}
}

// 两个相邻矩形各由两个三角形拼成，共享一条竖直边：共享边像素只归属
// 一侧，关闭深度测试也不会重复着色，且没有缝隙。
func TestSharedVerticalEdgeNoOverlap(t *testing.T) {
	const w, h = 16, 16
	// 左矩形覆盖像素列 [2,7]，右矩形覆盖 [8,13]，共享边落在像素边界 x=8。
	l0 := cv(-0.75, -0.5, 0, 1, 1)
	l1 := cv(0, -0.5, 0, 1, 1)
	l2 := cv(0, 0.5, 0, 1, 1)
	l3 := cv(-0.75, 0.5, 0, 1, 1)
	r0 := cv(0, -0.5, 0, 1, 2)
	r1 := cv(0.75, -0.5, 0, 1, 2)
	r2 := cv(0.75, 0.5, 0, 1, 2)
	r3 := cv(0, 0.5, 0, 1, 2)
	verts := []Vertex{
		l0, l2, l3, l0, l1, l2, // 左矩形，两个三角形
		r0, r2, r3, r0, r1, r2, // 右矩形，两个三角形
	}

	for _, workers := range []int{1, 4} {
		fb := NewFramebuffer(w, h, 1)
		r, err := New(Config{AttrCount: 1, DepthTest: false, TileSize: 3, Workers: workers})
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Draw(fb, verts); err != nil {
			t.Fatalf("workers=%d Draw: %v", workers, err)
		}
		covered := 0
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				idx := y*w + x
				switch fb.Samples[idx] {
				case 0:
				case 1:
					covered++
					want := 1.0
					if x >= 8 {
						want = 2
					}
					if fb.Color[idx] != want {
						t.Fatalf("workers=%d pixel (%d,%d) color=%v, want %v",
							workers, x, y, fb.Color[idx], want)
					}
				default:
					t.Fatalf("workers=%d pixel (%d,%d) samples=%d, want 0 or 1",
						workers, x, y, fb.Samples[idx])
				}
			}
		}
		// 两个矩形各 6 列 x 8 行 = 48 像素，合计 96，无重叠无缝隙。
		if covered != 96 {
			t.Fatalf("workers=%d covered=%d, want 96", workers, covered)
		}
	}
}

// buildGridMesh 用三角网格铺满 NDC 全屏，每格两个三角形，
// 属性写入格子编号，便于逐像素核对归属。
func buildGridMesh(cols, rows int) []Vertex {
	verts := make([]Vertex, 0, cols*rows*6)
	for cy := 0; cy < rows; cy++ {
		for cx := 0; cx < cols; cx++ {
			x0 := -1 + 2*float64(cx)/float64(cols)
			x1 := -1 + 2*float64(cx+1)/float64(cols)
			y0 := -1 + 2*float64(cy)/float64(rows)
			y1 := -1 + 2*float64(cy+1)/float64(rows)
			id := float64(cy*cols + cx + 1)
			tl := cv(x0, y0, 0, 1, id)
			tr := cv(x1, y0, 0, 1, id)
			br := cv(x1, y1, 0, 1, id)
			bl := cv(x0, y1, 0, 1, id)
			// y 轴向下时 CCW 绕序：(tl,br,bl) 与 (tl,tr,br)。
			verts = append(verts, tl, br, bl, tl, tr, br)
		}
	}
	return verts
}

// 三角网格铺满整屏：每个像素恰好着色一次；串行与图块并行逐位一致。
func TestFullScreenGridCoverage(t *testing.T) {
	const w, h = 64, 48
	mesh := buildGridMesh(8, 6)

	serialFB := NewFramebuffer(w, h, 1)
	if err := newTestRenderer(t, 1).Draw(serialFB, mesh); err != nil {
		t.Fatalf("serial Draw: %v", err)
	}
	parallelFB := NewFramebuffer(w, h, 1)
	if err := newTestRenderer(t, 4).Draw(parallelFB, mesh); err != nil {
		t.Fatalf("parallel Draw: %v", err)
	}

	if serialFB.TotalSamples() != w*h {
		t.Fatalf("serial total samples = %d, want %d", serialFB.TotalSamples(), w*h)
	}
	cellW := float64(w) / 8
	cellH := float64(h) / 6
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := y*w + x
			if n := serialFB.Samples[idx]; n != 1 {
				t.Fatalf("serial pixel (%d,%d) shaded %d times", x, y, n)
			}
			if n := parallelFB.Samples[idx]; n != 1 {
				t.Fatalf("parallel pixel (%d,%d) shaded %d times", x, y, n)
			}
			if math.Float64bits(serialFB.Color[idx]) != math.Float64bits(parallelFB.Color[idx]) {
				t.Fatalf("color mismatch at (%d,%d): %v vs %v", x, y, serialFB.Color[idx], parallelFB.Color[idx])
			}
			if math.Float64bits(serialFB.Depth[idx]) != math.Float64bits(parallelFB.Depth[idx]) {
				t.Fatalf("depth mismatch at (%d,%d)", x, y)
			}
			cx := int(float64(x) / cellW)
			cy := int(float64(y) / cellH)
			want := float64(cy*8 + cx + 1)
			if serialFB.Color[idx] != want {
				t.Fatalf("pixel (%d,%d) color = %v, want %v", x, y, serialFB.Color[idx], want)
			}
		}
	}
}

// 随机三角形压力测试：并行结果必须与串行逐像素完全一致。
func TestSerialParallelEquivalent(t *testing.T) {
	const w, h = 80, 60
	rng := rand.New(rand.NewSource(42))
	verts := make([]Vertex, 0, 300)
	for i := 0; i < 100; i++ {
		for k := 0; k < 3; k++ {
			nx := rng.Float64()*2.4 - 1.2
			ny := rng.Float64()*2.4 - 1.2
			nz := rng.Float64()*2 - 1
			vw := 0.5 + rng.Float64()*2
			verts = append(verts, cv(nx, ny, nz, vw, rng.Float64()))
		}
	}

	serialFB := NewFramebuffer(w, h, 1)
	if err := newTestRenderer(t, 1).Draw(serialFB, verts); err != nil {
		t.Fatalf("serial Draw: %v", err)
	}
	parallelFB := NewFramebuffer(w, h, 1)
	if err := newTestRenderer(t, 8).Draw(parallelFB, verts); err != nil {
		t.Fatalf("parallel Draw: %v", err)
	}

	for i := range serialFB.Color {
		if math.Float64bits(serialFB.Color[i]) != math.Float64bits(parallelFB.Color[i]) {
			t.Fatalf("color mismatch at %d: %v vs %v", i, serialFB.Color[i], parallelFB.Color[i])
		}
		if math.Float64bits(serialFB.Depth[i]) != math.Float64bits(parallelFB.Depth[i]) {
			t.Fatalf("depth mismatch at %d", i)
		}
		if serialFB.Samples[i] != parallelFB.Samples[i] {
			t.Fatalf("samples mismatch at %d: %d vs %d", i, serialFB.Samples[i], parallelFB.Samples[i])
		}
	}
}

// 透视正确插值：与独立参考实现逐像素比对；w=1 时退化为仿射插值。
func TestPerspectiveCorrectInterpolation(t *testing.T) {
	const w, h = 32, 32
	tri := []Vertex{
		cv(-0.9, -0.9, -0.3, 1.0, 10),
		cv(0.9, -0.6, 0.2, 3.0, 20),
		cv(0.1, 0.9, 0.6, 0.5, 30),
	}
	fb := NewFramebuffer(w, h, 1)
	if err := newTestRenderer(t, 1).Draw(fb, tri); err != nil {
		t.Fatalf("Draw: %v", err)
	}
	if fb.TotalSamples() == 0 {
		t.Fatal("triangle produced no fragments")
	}

	sx := [3]float64{}
	sy := [3]float64{}
	invW := [3]float64{}
	attrs := [3]float64{}
	for i := 0; i < 3; i++ {
		ndcX := tri[i].Pos.X / tri[i].Pos.W
		ndcY := tri[i].Pos.Y / tri[i].Pos.W
		// 与渲染器完全一致地做 1/256 子像素定点量化，保证采样位置相同。
		sx[i] = float64(toFixed((ndcX+1)*0.5*w)) / subScale
		sy[i] = float64(toFixed((ndcY+1)*0.5*h)) / subScale
		invW[i] = 1 / tri[i].Pos.W
		attrs[i] = tri[i].Attr[0]
	}
	area := (sx[1]-sx[0])*(sy[2]-sy[0]) - (sy[1]-sy[0])*(sx[2]-sx[0])

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := y*w + x
			if fb.Samples[idx] == 0 {
				continue
			}
			px := float64(x) + 0.5
			py := float64(y) + 0.5
			l0 := ((sx[1]-px)*(sy[2]-py) - (sy[1]-py)*(sx[2]-px)) / area
			l1 := ((sx[2]-px)*(sy[0]-py) - (sy[2]-py)*(sx[0]-px)) / area
			l2 := 1 - l0 - l1
			denom := l0*invW[0] + l1*invW[1] + l2*invW[2]
			want := (l0*attrs[0]*invW[0] + l1*attrs[1]*invW[1] + l2*attrs[2]*invW[2]) / denom
			if math.Abs(fb.Color[idx]-want) > 1e-12 {
				t.Fatalf("interpolated attr at (%d,%d) = %v, want %v", x, y, fb.Color[idx], want)
			}
		}
	}

	// w 全为 1 时透视插值必须等于普通仿射重心插值。
	affine := []Vertex{
		cv(-0.8, -0.8, 0, 1, 1),
		cv(0.8, -0.8, 0, 1, 2),
		cv(0, 0.8, 0, 1, 3),
	}
	affFB := NewFramebuffer(w, h, 1)
	if err := newTestRenderer(t, 1).Draw(affFB, affine); err != nil {
		t.Fatalf("affine Draw: %v", err)
	}
	ax := [3]float64{0.1 * w, 0.9 * w, 0.5 * w}
	ay := [3]float64{0.1 * h, 0.1 * h, 0.9 * h}
	aArea := (ax[1]-ax[0])*(ay[2]-ay[0]) - (ay[1]-ay[0])*(ax[2]-ax[0])
	px := float64(w/2) + 0.5
	py := float64(h/4) + 0.5
	b0 := ((ax[1]-px)*(ay[2]-py) - (ay[1]-py)*(ax[2]-px)) / aArea
	b1 := ((ax[2]-px)*(ay[0]-py) - (ay[2]-py)*(ax[0]-px)) / aArea
	b2 := 1 - b0 - b1
	idx := (h/4)*w + w/2
	if affFB.Samples[idx] == 0 {
		t.Fatalf("expected coverage at (%d,%d)", w/2, h/4)
	}
	want := b0 + 2*b1 + 3*b2
	if math.Abs(affFB.Color[idx]-want) > 1e-3 {
		t.Fatalf("affine fallback mismatch: got %v want %v", affFB.Color[idx], want)
	}
}

// fullScreenTri 构造覆盖整屏、给定 NDC 深度与颜色的两个三角形。
func fullScreenTri(z, color float64) []Vertex {
	return []Vertex{
		cv(-1, -1, z, 1, color),
		cv(1, 1, z, 1, color),
		cv(1, -1, z, 1, color),
		cv(-1, -1, z, 1, color),
		cv(-1, 1, z, 1, color),
		cv(1, 1, z, 1, color),
	}
}

func rendererWith(t *testing.T, fn DepthFunc, test, write bool, workers int) *Renderer {
	t.Helper()
	r, err := New(Config{
		AttrCount:  1,
		Depth:      fn,
		DepthTest:  test,
		DepthWrite: write,
		TileSize:   5,
		Workers:    workers,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

// 可配置深度测试：比较函数、测试开关、回写开关的行为都正确。
func TestDepthConfig(t *testing.T) {
	const w, h = 16, 16

	t.Run("less_near_wins", func(t *testing.T) {
		fb := NewFramebuffer(w, h, 1)
		r := rendererWith(t, DepthLess, true, true, 1)
		if err := r.Draw(fb, fullScreenTri(0.5, 1)); err != nil {
			t.Fatal(err)
		}
		if err := r.Draw(fb, fullScreenTri(0.8, 2)); err != nil {
			t.Fatal(err)
		}
		if fb.Color[0] != 1 || fb.Depth[0] != 0.5 {
			t.Fatalf("near surface lost: color=%v depth=%v", fb.Color[0], fb.Depth[0])
		}
		if err := r.Draw(fb, fullScreenTri(0.2, 3)); err != nil {
			t.Fatal(err)
		}
		if fb.Color[0] != 3 || fb.Depth[0] != 0.2 {
			t.Fatalf("closer surface not written: color=%v depth=%v", fb.Color[0], fb.Depth[0])
		}
	})

	t.Run("greater_far_wins", func(t *testing.T) {
		fb := NewFramebuffer(w, h, 0) // 清除深度 0，要求更大的深度通过
		r := rendererWith(t, DepthGreater, true, true, 1)
		if err := r.Draw(fb, fullScreenTri(0.2, 1)); err != nil {
			t.Fatal(err)
		}
		if err := r.Draw(fb, fullScreenTri(0.6, 2)); err != nil {
			t.Fatal(err)
		}
		if fb.Color[0] != 2 || fb.Depth[0] != 0.6 {
			t.Fatalf("DepthGreater wrong: color=%v depth=%v", fb.Color[0], fb.Depth[0])
		}
	})

	t.Run("always_and_never", func(t *testing.T) {
		fb := NewFramebuffer(w, h, 1)
		r := rendererWith(t, DepthAlways, true, true, 1)
		if err := r.Draw(fb, fullScreenTri(0.9, 7)); err != nil {
			t.Fatal(err)
		}
		if fb.Color[0] != 7 {
			t.Fatalf("DepthAlways failed: %v", fb.Color[0])
		}
		fb2 := NewFramebuffer(w, h, 1)
		r2 := rendererWith(t, DepthNever, true, true, 1)
		if err := r2.Draw(fb2, fullScreenTri(-0.5, 9)); err != nil {
			t.Fatal(err)
		}
		if fb2.TotalSamples() != 0 {
			t.Fatalf("DepthNever produced %d fragments", fb2.TotalSamples())
		}
	})

	t.Run("test_disabled_writes_all", func(t *testing.T) {
		fb := NewFramebuffer(w, h, 1)
		r := rendererWith(t, DepthLess, false, true, 1)
		if err := r.Draw(fb, fullScreenTri(0.5, 1)); err != nil {
			t.Fatal(err)
		}
		if err := r.Draw(fb, fullScreenTri(0.9, 2)); err != nil {
			t.Fatal(err)
		}
		if fb.Color[0] != 2 {
			t.Fatalf("depth test should be disabled, got %v", fb.Color[0])
		}
	})

	t.Run("no_depth_write_keeps_buffer", func(t *testing.T) {
		fb := NewFramebuffer(w, h, 1)
		r := rendererWith(t, DepthLess, true, false, 1)
		if err := r.Draw(fb, fullScreenTri(0.5, 1)); err != nil {
			t.Fatal(err)
		}
		if fb.Depth[0] != 1 {
			t.Fatalf("depth buffer changed despite DepthWrite=false: %v", fb.Depth[0])
		}
	})

	t.Run("functions_table", func(t *testing.T) {
		cases := []struct {
			fn           DepthFunc
			frag, stored float64
			want         bool
		}{
			{DepthLess, 0.4, 0.5, true},
			{DepthLess, 0.6, 0.5, false},
			{DepthEqual, 0.5, 0.5, true},
			{DepthLEqual, 0.5, 0.5, true},
			{DepthGreater, 0.6, 0.5, true},
			{DepthNotEqual, 0.6, 0.5, true},
			{DepthGEqual, 0.5, 0.5, true},
			{DepthNever, 0, 0, false},
			{DepthAlways, 9, 0, true},
		}
		for _, c := range cases {
			if got := c.fn.pass(c.frag, c.stored); got != c.want {
				t.Fatalf("%v pass(%v,%v) = %v, want %v", c.fn, c.frag, c.stored, got, c.want)
			}
		}
	})
}

// 近平面裁剪：完全在近平面后的三角形被剔除；跨越近平面的三角形
// 被裁剪后仍能着色，且串行/并行结果一致。
func TestNearPlaneClipping(t *testing.T) {
	const w, h = 32, 32

	t.Run("fully_behind_rejected", func(t *testing.T) {
		fb := NewFramebuffer(w, h, 1)
		r := newTestRenderer(t, 1)
		behind := []Vertex{
			cv(-0.5, -0.5, -2, 1, 1),
			cv(0.5, -0.5, -1.5, 1, 1),
			cv(0, 0.5, -3, 1, 1),
		}
		if err := r.Draw(fb, behind); err != nil {
			t.Fatal(err)
		}
		if fb.TotalSamples() != 0 {
			t.Fatalf("behind-near triangle produced %d fragments", fb.TotalSamples())
		}
	})

	t.Run("straddling_triangle_clipped", func(t *testing.T) {
		// 一个顶点在近平面后（z=-2 -> ndc=-2），另外两个在前。
		straddle := []Vertex{
			cv(-0.9, -0.9, -0.2, 1, 1),
			cv(0.9, -0.9, -0.1, 1, 2),
			cv(0, 0.9, -2, 1, 3),
		}
		serialFB := NewFramebuffer(w, h, 1)
		if err := newTestRenderer(t, 1).Draw(serialFB, straddle); err != nil {
			t.Fatal(err)
		}
		parallelFB := NewFramebuffer(w, h, 1)
		if err := newTestRenderer(t, 4).Draw(parallelFB, straddle); err != nil {
			t.Fatal(err)
		}
		if serialFB.TotalSamples() == 0 {
			t.Fatal("straddling triangle produced no fragments")
		}
		for i := range serialFB.Color {
			if math.Float64bits(serialFB.Color[i]) != math.Float64bits(parallelFB.Color[i]) {
				t.Fatalf("clip color mismatch at %d: %v vs %v", i, serialFB.Color[i], parallelFB.Color[i])
			}
			if serialFB.Samples[i] != parallelFB.Samples[i] {
				t.Fatalf("clip samples mismatch at %d", i)
			}
			if serialFB.Samples[i] > 1 {
				t.Fatalf("clipped triangle double-shaded pixel %d: %d", i, serialFB.Samples[i])
			}
		}
		// 裁剪后所有片段都必须在近平面之前（ndc z >= -1）。
		for i, n := range serialFB.Samples {
			if n > 0 && serialFB.Depth[i] < -1-1e-12 {
				t.Fatalf("fragment %d depth %v violates near plane", i, serialFB.Depth[i])
			}
		}
	})

	t.Run("two_vertices_behind", func(t *testing.T) {
		// 两个顶点在近平面后，裁剪结果退化为单个三角形。
		verts := []Vertex{
			cv(-0.9, -0.9, -0.2, 1, 5),
			cv(0.9, -0.9, -3, 1, 6),
			cv(0, 0.9, -2.5, 1, 7),
		}
		fb := NewFramebuffer(w, h, 1)
		if err := newTestRenderer(t, 1).Draw(fb, verts); err != nil {
			t.Fatal(err)
		}
		if fb.TotalSamples() == 0 {
			t.Fatal("expected clipped fragments with one vertex in front")
		}
	})
}

// 配置与输入校验。
func TestValidation(t *testing.T) {
	if _, err := New(Config{AttrCount: 0}); err == nil {
		t.Fatal("expected error for non-positive AttrCount")
	}
	r := newTestRenderer(t, 1)
	fb := NewFramebuffer(8, 8, 1)
	if err := r.Draw(fb, []Vertex{cv(0, 0, 0, 1, 1), cv(1, 0, 0, 1, 1)}); err == nil {
		t.Fatal("expected error for vertex count not divisible by 3")
	}
	bad := []Vertex{
		cv(0, 0, 0, 1, 1),
		cv(1, 0, 0, 1, 1),
		{Pos: Vec4{X: 0, Y: 1, W: 1}, Attr: []float64{1, 2}},
	}
	if err := r.Draw(fb, bad); err == nil {
		t.Fatal("expected error for mismatched attribute count")
	}
}

// 多种分辨率、网格密度与图块尺寸下，整屏网格都做到逐像素唯一覆盖，
// 且串行与并行输出逐位一致（含不整除图块与奇数尺寸）。
func TestGridCoverageDimensions(t *testing.T) {
	cases := []struct {
		w, h, cols, rows, tile int
	}{
		{1, 1, 1, 1, 1},
		{7, 5, 3, 2, 2},
		{33, 17, 5, 4, 8},
		{64, 64, 16, 16, 16},
		{100, 50, 7, 3, 13},
		{128, 96, 32, 24, 32},
	}
	for _, c := range cases {
		mesh := buildGridMesh(c.cols, c.rows)
		serialFB := NewFramebuffer(c.w, c.h, 1)
		serial, err := New(Config{
			AttrCount: 1, Depth: DepthLess, DepthTest: true, DepthWrite: true,
			TileSize: c.tile, Workers: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := serial.Draw(serialFB, mesh); err != nil {
			t.Fatalf("%+v serial Draw: %v", c, err)
		}
		parallelFB := NewFramebuffer(c.w, c.h, 1)
		parallel, err := New(Config{
			AttrCount: 1, Depth: DepthLess, DepthTest: true, DepthWrite: true,
			TileSize: c.tile, Workers: 4,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := parallel.Draw(parallelFB, mesh); err != nil {
			t.Fatalf("%+v parallel Draw: %v", c, err)
		}
		if serialFB.TotalSamples() != c.w*c.h {
			t.Fatalf("%+v total samples = %d, want %d", c, serialFB.TotalSamples(), c.w*c.h)
		}
		for i := range serialFB.Color {
			if serialFB.Samples[i] != 1 || parallelFB.Samples[i] != 1 {
				t.Fatalf("%+v pixel %d samples serial=%d parallel=%d",
					c, i, serialFB.Samples[i], parallelFB.Samples[i])
			}
			if math.Float64bits(serialFB.Color[i]) != math.Float64bits(parallelFB.Color[i]) ||
				math.Float64bits(serialFB.Depth[i]) != math.Float64bits(parallelFB.Depth[i]) {
				t.Fatalf("%+v serial/parallel mismatch at %d", c, i)
			}
		}
	}
}
