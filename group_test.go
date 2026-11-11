package compositor

import "testing"

// solid 返回 1×1 纯色浮点图像。
func solid(c Color) *ImageF {
	im := NewImageF(1, 1)
	im.Pix[0] = c
	return im
}

func TestGroupIsolation(t *testing.T) {
	backdrop := Color{0.5, 0.4, 0.3, 0.8}
	bottom := Color{0.4, 0.2, 0.2, 0.5}
	top := Color{0.3, 0.3, 0.6, 0.6}
	// 隔离组:top 的 multiply 只作用于组内的 bottom。
	isolated := NewImageF(1, 1)
	isolated.Pix[0] = backdrop
	DrawGroup(isolated, Group{
		Isolated: true,
		Opa:      1,
		Layers: []Layer{
			{Src: solid(bottom), Opa: 1},
			{Src: solid(top), Mode: ModeMultiply, Opa: 1},
		},
	})
	// 手工参考:组内先合成,再整体 over 到背景。
	buf := ModeMultiply.Blend(top, Over(bottom, Color{}))
	want := Over(buf, backdrop)
	if !colorClose(isolated.Pix[0], want, eps) {
		t.Fatalf("隔离组: got %+v want %+v", isolated.Pix[0], want)
	}
	// 非隔离组:top 的 multiply 直接作用于包含背景的组背景。
	nonIso := NewImageF(1, 1)
	nonIso.Pix[0] = backdrop
	DrawGroup(nonIso, Group{
		Isolated: false,
		Opa:      1,
		Mode:     ModeMultiply, // 强制走离屏路径
		Layers: []Layer{
			{Src: solid(bottom), Opa: 1},
			{Src: solid(top), Mode: ModeMultiply, Opa: 1},
		},
	})
	// 手工参考:组缓冲以背景为起点,top 与(背景+bottom)混合,
	// 组缓冲再与背景做 multiply。
	inner := ModeMultiply.Blend(top, Over(bottom, backdrop))
	wantNonIso := ModeMultiply.Blend(inner, backdrop)
	if !colorClose(nonIso.Pix[0], wantNonIso, eps) {
		t.Fatalf("非隔离组: got %+v want %+v", nonIso.Pix[0], wantNonIso)
	}
	if colorClose(isolated.Pix[0], nonIso.Pix[0], 1e-3) {
		t.Fatal("隔离与非隔离组应产生不同结果")
	}
}

func TestGroupOpacity(t *testing.T) {
	backdrop := Color{0.2, 0.2, 0.2, 1}
	src := Color{0.9, 0.6, 0.3, 1}
	// 组不透明度 0:背景不变。
	dst := NewImageF(1, 1)
	dst.Pix[0] = backdrop
	DrawGroup(dst, Group{Opa: 0, Layers: []Layer{{Src: solid(src), Opa: 1}}})
	if dst.Pix[0] != backdrop {
		t.Fatalf("组不透明度 0 应保留背景: %+v", dst.Pix[0])
	}
	// 组不透明度 0.5:等价于组结果减半后合成。
	dst.Pix[0] = backdrop
	DrawGroup(dst, Group{Opa: 0.5, Layers: []Layer{{Src: solid(src), Opa: 1}}})
	want := Over(src.Scale(0.5), backdrop)
	if !colorClose(dst.Pix[0], want, eps) {
		t.Fatalf("组不透明度 0.5: got %+v want %+v", dst.Pix[0], want)
	}
	// 组不透明度 1 且非隔离:等价于直接画图层。
	dst.Pix[0] = backdrop
	DrawGroup(dst, Group{Opa: 1, Layers: []Layer{{Src: solid(src), Opa: 1}}})
	if !colorClose(dst.Pix[0], Over(src, backdrop), eps) {
		t.Fatalf("平坦组应等价于直接合成: %+v", dst.Pix[0])
	}
}

func TestLayerMask(t *testing.T) {
	backdrop := Color{0.1, 0.1, 0.1, 1}
	src := Color{0.8, 0.4, 0.2, 1}
	// 遮罩 alpha 0:完全裁掉。
	dst := NewImageF(1, 1)
	dst.Pix[0] = backdrop
	DrawLayer(dst, Layer{Src: solid(src), Opa: 1, Mask: solid(Color{A: 0})})
	if dst.Pix[0] != backdrop {
		t.Fatalf("遮罩 0 应裁掉图层: %+v", dst.Pix[0])
	}
	// 遮罩 alpha 0.5:等价于图层不透明度减半。
	dst.Pix[0] = backdrop
	DrawLayer(dst, Layer{Src: solid(src), Opa: 1, Mask: solid(Color{A: 0.5})})
	want := Over(src.Scale(0.5), backdrop)
	if !colorClose(dst.Pix[0], want, eps) {
		t.Fatalf("遮罩 0.5: got %+v want %+v", dst.Pix[0], want)
	}
}

func TestGroupMask(t *testing.T) {
	backdrop := Color{0.3, 0.3, 0.3, 1}
	src := Color{0.7, 0.5, 0.2, 1}
	dst := NewImageF(1, 1)
	dst.Pix[0] = backdrop
	DrawGroup(dst, Group{
		Opa:    1,
		Mask:   solid(Color{A: 0.25}),
		Layers: []Layer{{Src: solid(src), Opa: 1}},
	})
	want := Over(src.Scale(0.25), backdrop)
	if !colorClose(dst.Pix[0], want, eps) {
		t.Fatalf("组遮罩: got %+v want %+v", dst.Pix[0], want)
	}
}

// TestEightBitVsFloatPipeline 验证含隔离组的多级流水线在两条路径下
// 误差有界。8 位路径每次写入缓冲量化一次(<=0.5/255);本流水线对
// 每个像素依次经过:组缓冲 2 次图层写入 + 组整体合成 1 次 +
// 顶层图层 1 次,共 4 个量化阶段,误差界 4·0.5/255。所选混合模式
// (normal/multiply/screen)对背景色误差的放大系数不超过 1。
func TestEightBitVsFloatPipeline(t *testing.T) {
	const stages = 4
	const tol = stages*0.5/255 + 1e-9
	mkSrc := func(seed uint8) *Image8 {
		im := NewImage8(3, 3)
		fillPremul8(im, seed)
		return im
	}
	l1 := Layer{Src: mkSrc(3), Mode: ModeNormal, Opa: 0.9}
	l2 := Layer{Src: mkSrc(71), Mode: ModeMultiply, Opa: 1}
	top := Layer{Src: mkSrc(140), Mode: ModeScreen, Opa: 0.7}
	run := func(dst Image) {
		DrawGroup(dst, Group{Isolated: true, Opa: 0.8, Layers: []Layer{l1, l2}})
		DrawLayer(dst, top)
	}
	f := NewImageF(3, 3)
	b := NewImage8(3, 3)
	fillPremul8(b, 97)
	for i := range f.Pix {
		f.Pix[i] = b.At(i%3, i/3)
	}
	run(f)
	run(b)
	for i := range f.Pix {
		got := b.At(i%3, i/3)
		if !colorClose(got, f.Pix[i], tol) {
			t.Fatalf("px=%d: 8位 %+v 浮点 %+v 超出误差界 %v", i, got, f.Pix[i], tol)
		}
	}
}
