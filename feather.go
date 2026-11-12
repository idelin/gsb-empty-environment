package taskpkg

import "errors"

// Feather 沿给定缝合线对两路线性光帧做固定宽度的线性光羽化混合。
//
// halfWidth 是缝两侧各自的过渡半宽，过渡区总宽为 2*halfWidth：
// 缝左 halfWidth 列以上完全取帧 a，缝右 halfWidth 列以上完全取帧 b。
// 过渡区内 alpha 在线性光空间按距离线性变化：
// alpha = clamp((x - seamY + halfWidth) / (2*halfWidth), 0, 1)，
// 输出 = (1-alpha)*a + alpha*b；halfWidth 为 0 时缝左侧取 a、缝处及右侧取 b（硬切）。
//
// 由于输入本身是线性光 RGB，权重直接在线性光空间作用，不做 gamma 校正。
// 非重叠像素按所在侧直接取对应帧；重叠区中掩码无效的像素同样按所在侧
// 回退到单帧数据，保证输出每个像素都有定义。
// seam 必须通过 validateSeam 的约束（每行一个有效像素且相邻行横移不超过 1）。
func Feather(a, b *Frame, overlap *Mask, seam []int, halfWidth int) (*Frame, error) {
	if err := validateInputs(a, b, overlap); err != nil {
		return nil, err
	}
	if halfWidth < 0 {
		return nil, errors.New("taskpkg: halfWidth 不能为负数")
	}
	if err := validateSeam(overlap, seam); err != nil {
		return nil, err
	}

	width, height := a.Width, a.Height
	out := NewFrame(width, height)
	width2f := float64(2 * halfWidth)

	for y := 0; y < height; y++ {
		seamX := seam[y]
		for x := 0; x < width; x++ {
			alpha := featherAlpha(x, seamX, halfWidth, width2f)
			if !overlap.At(x, y) {
				// 非重叠区域：缝左侧归 a，缝处及右侧归 b。
				if x < seamX {
					alpha = 0
				} else {
					alpha = 1
				}
			}
			i := a.index(x, y)
			for c := 0; c < 3; c++ {
				av := a.Pix[i+c]
				bv := b.Pix[i+c]
				out.Pix[i+c] = (1-alpha)*av + alpha*bv
			}
		}
	}
	return out, nil
}

func featherAlpha(x, seamX, halfWidth int, width2f float64) float64 {
	if halfWidth == 0 {
		if x < seamX {
			return 0
		}
		return 1
	}
	d := float64(x - seamX + halfWidth)
	switch {
	case d <= 0:
		return 0
	case d >= width2f:
		return 1
	default:
		return d / width2f
	}
}
