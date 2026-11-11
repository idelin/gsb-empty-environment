package raster

// 定点化的屏幕坐标。
//
// 所有覆盖判定（包围盒、边函数、步进）都在子像素定点空间完成，
// 不引入浮点，从而保证串行与并行、不同平台之间逐像素结果完全一致。
// 透视正确的属性插值仍使用 float64（仅由定点化的重心权重驱动，
// 结果同样是确定性的）。

const (
	// subBits 为每个像素保留的分数位数。8 位即 1/256 子像素精度。
	subBits = 8
	// subScale 是定点单位与像素单位之间的换算系数。
	subScale = 1 << subBits
)

// fixed 表示以 1/subScale 像素为单位的定点屏幕坐标。
type fixed int64

// toFixed 把像素空间的浮点坐标舍入为定点坐标（四舍五入，半远离零）。
func toFixed(pixels float64) fixed {
	if pixels >= 0 {
		return fixed(pixels*subScale + 0.5)
	}
	return fixed(pixels*subScale - 0.5)
}

// floorDiv 返回 a/b 的向下取整除法（b 必须为正）。
func floorDiv(a fixed, b int64) fixed {
	q := a / fixed(b)
	r := a % fixed(b)
	if r != 0 && ((a < 0) != (fixed(b) < 0)) {
		q--
	}
	return q
}
