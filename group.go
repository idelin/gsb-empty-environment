package compositor

// Layer 描述一个待合成的图层。
type Layer struct {
	Src  Image   // 图层像素,预乘 alpha
	Mode Mode    // 与背景的混合模式,ModeNormal 即 source-over
	Opa  float64 // 图层不透明度 [0,1],0 为完全透明
	Mask Image   // 裁剪遮罩,取其 alpha 通道逐像素相乘;nil 表示无遮罩
}

// DrawLayer 将图层合成到 dst 上,逐像素执行:
//
//	cs = Src(x,y) · Opa · Mask(x,y).A
//	dst(x,y) = Mode.Blend(cs, dst(x,y))
func DrawLayer(dst Image, l Layer) {
	w := min(dst.Width(), l.Src.Width())
	h := min(dst.Height(), l.Src.Height())
	if l.Mask != nil {
		w = min(w, l.Mask.Width())
		h = min(h, l.Mask.Height())
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			cs := l.Src.At(x, y).Scale(l.Opa)
			if l.Mask != nil {
				cs = cs.Scale(l.Mask.At(x, y).A)
			}
			dst.Set(x, y, l.Mode.Blend(cs, dst.At(x, y)))
		}
	}
}

// Group 是一组作为整体合成的图层。
type Group struct {
	Layers   []Layer // 自底向上依次合成
	Isolated bool    // 隔离组:组内图层只与组内像素混合
	Opa      float64 // 组不透明度 [0,1],0 为完全透明
	Mode     Mode    // 组整体与背景的混合模式
	Mask     Image   // 组级裁剪遮罩;nil 表示无遮罩
}

// DrawGroup 将组合成到 dst 上。
//
// 非隔离组在不透明度为 1、无组级混合与遮罩时,等价于把组内图层
// 依次直接画到 dst 上;其余情况(隔离组、组不透明度 <1、组级混合
// 或遮罩)都按规范先渲染到离屏缓冲,再作为整体与背景合成一次。
// 离屏缓冲与 dst 同类型,保证 8 位路径的量化误差有界。
func DrawGroup(dst Image, g Group) {
	flat := !g.Isolated && g.Opa >= 1 && g.Mode == ModeNormal && g.Mask == nil
	if flat {
		for _, l := range g.Layers {
			DrawLayer(dst, l)
		}
		return
	}
	buf := NewBuffer(dst)
	if !g.Isolated {
		// 非隔离组:组内第一个图层的背景是 dst 当前内容。
		copyImage(buf, dst)
	}
	for _, l := range g.Layers {
		DrawLayer(buf, l)
	}
	DrawLayer(dst, Layer{Src: buf, Mode: g.Mode, Opa: g.Opa, Mask: g.Mask})
}

func copyImage(dst, src Image) {
	w := min(dst.Width(), src.Width())
	h := min(dst.Height(), src.Height())
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(x, y, src.At(x, y))
		}
	}
}
