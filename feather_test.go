package taskpkg

import "testing"

func almostEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-12
}

// TestFeatherRamp 用两帧常数颜色验证固定宽度线性光过渡。
func TestFeatherRamp(t *testing.T) {
	width, height := 5, 2
	a := NewFrame(width, height)
	b := NewFrame(width, height)
	m := NewMask(width, height)
	for i := range m.Pix {
		m.Pix[i] = true
	}
	for i := range a.Pix {
		a.Pix[i] = 0.2
		b.Pix[i] = 0.8
	}
	seam := []int{2, 2}

	out, err := Feather(a, b, m, seam, 1)
	if err != nil {
		t.Fatalf("意外报错 %v", err)
	}
	// halfWidth=1，宽度 2：x=1 处 alpha=0，x=2 处 0.5，x=3 处 1。
	want := []float64{0.2, 0.2, 0.5, 0.8, 0.8}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r, g, bl := out.At(x, y)
			if !almostEqual(r, want[x]) || !almostEqual(g, want[x]) || !almostEqual(bl, want[x]) {
				t.Fatalf("(%d,%d) 期望 %v，得到 (%v,%v,%v)", x, y, want[x], r, g, bl)
			}
		}
	}
}

// TestFeatherHardCut 验证半宽为 0 时在缝处硬切。
func TestFeatherHardCut(t *testing.T) {
	width, height := 4, 1
	a := NewFrame(width, height)
	b := NewFrame(width, height)
	m := NewMask(width, height)
	for i := range m.Pix {
		m.Pix[i] = true
	}
	for i := range a.Pix {
		a.Pix[i] = 1
		b.Pix[i] = 2
	}
	seam := []int{2}
	out, err := Feather(a, b, m, seam, 0)
	if err != nil {
		t.Fatalf("意外报错 %v", err)
	}
	want := []float64{1, 1, 2, 2}
	for x := 0; x < width; x++ {
		r, _, _ := out.At(x, 0)
		if r != want[x] {
			t.Fatalf("x=%d 期望 %v，得到 %v", x, want[x], r)
		}
	}
}

// TestFeatherInvalidFallback 验证重叠区无效像素按缝所在侧回退单帧。
func TestFeatherInvalidFallback(t *testing.T) {
	width, height := 3, 1
	a := NewFrame(width, height)
	b := NewFrame(width, height)
	m := NewMask(width, height)
	for i := range a.Pix {
		a.Pix[i] = 1
		b.Pix[i] = 2
	}
	// 仅缝像素有效，左右均为无效像素。
	m.Set(1, 0, true)
	seam := []int{1}

	out, err := Feather(a, b, m, seam, 1)
	if err != nil {
		t.Fatalf("意外报错 %v", err)
	}
	// x=0 在缝左侧回退 a；x=1 是缝，alpha=0.5；x=2 在缝右侧回退 b。
	want := []float64{1, 1.5, 2}
	for x := 0; x < width; x++ {
		r, _, _ := out.At(x, 0)
		if !almostEqual(r, want[x]) {
			t.Fatalf("x=%d 期望 %v，得到 %v", x, want[x], r)
		}
	}
}

// TestFeatherEndToEnd 跑通“搜索缝 -> 羽化”的完整管线。
func TestFeatherEndToEnd(t *testing.T) {
	width, height := 4, 3
	a := NewFrame(width, height)
	b := NewFrame(width, height)
	m := NewMask(width, height)
	for i := range m.Pix {
		m.Pix[i] = true
	}
	for i := range a.Pix {
		a.Pix[i] = 0.1
		b.Pix[i] = 0.9
	}
	seam, err := Find(a, b, m, nil, nil)
	if err != nil {
		t.Fatalf("Find 意外报错 %v", err)
	}
	out, err := Feather(a, b, m, seam, 2)
	if err != nil {
		t.Fatalf("Feather 意外报错 %v", err)
	}
	if len(out.Pix) != width*height*3 {
		t.Fatalf("输出缓冲区长度异常 %d", len(out.Pix))
	}
	for i, v := range out.Pix {
		if v < 0.1-1e-12 || v > 0.9+1e-12 {
			t.Fatalf("输出像素 %d 超出两帧取值范围: %v", i, v)
		}
	}
}
