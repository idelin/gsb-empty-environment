package compositor

// Image 是合成运算作用的像素表面。At/Set 以预乘 alpha 的浮点
// Color 读写;具体实现决定存储精度。
type Image interface {
	Width() int
	Height() int
	At(x, y int) Color
	Set(x, y int, c Color)
}

// ImageF 是浮点路径:每像素 4 个 float64,不做量化。
type ImageF struct {
	W, H int
	Pix  []Color
}

// NewImageF 返回全透明的 w×h 浮点图像。
func NewImageF(w, h int) *ImageF {
	return &ImageF{W: w, H: h, Pix: make([]Color, w*h)}
}

func (im *ImageF) Width() int  { return im.W }
func (im *ImageF) Height() int { return im.H }

func (im *ImageF) At(x, y int) Color { return im.Pix[y*im.W+x] }

func (im *ImageF) Set(x, y int, c Color) { im.Pix[y*im.W+x] = c }

// Image8 是 8 位路径:预乘 RGBA,每通道 8 位。写入时钳制到
// [0,1] 并四舍五入到最近的 1/255,单次写入逐通道量化误差
// 不超过 0.5/255。
type Image8 struct {
	W, H int
	Pix  []uint8 // 每像素 4 字节:R,G,B,A
}

// NewImage8 返回全透明的 w×h 8 位图像。
func NewImage8(w, h int) *Image8 {
	return &Image8{W: w, H: h, Pix: make([]uint8, 4*w*h)}
}

func (im *Image8) Width() int  { return im.W }
func (im *Image8) Height() int { return im.H }

func (im *Image8) At(x, y int) Color {
	i := 4 * (y*im.W + x)
	return Color{
		R: float64(im.Pix[i+0]) / 255,
		G: float64(im.Pix[i+1]) / 255,
		B: float64(im.Pix[i+2]) / 255,
		A: float64(im.Pix[i+3]) / 255,
	}
}

func (im *Image8) Set(x, y int, c Color) {
	i := 4 * (y*im.W + x)
	im.Pix[i+0] = quantize8(c.R)
	im.Pix[i+1] = quantize8(c.G)
	im.Pix[i+2] = quantize8(c.B)
	im.Pix[i+3] = quantize8(c.A)
}

func quantize8(v float64) uint8 {
	v = clamp01(v)
	return uint8(v*255 + 0.5)
}

// NewBuffer 返回与 src 同类型、同尺寸的全透明图像,用作组渲染的
// 离屏缓冲,保证 8 位流水线在组边界处的量化行为与浏览器一致。
func NewBuffer(src Image) Image {
	switch im := src.(type) {
	case *ImageF:
		return NewImageF(im.W, im.H)
	case *Image8:
		return NewImage8(im.W, im.H)
	default:
		return NewImageF(src.Width(), src.Height())
	}
}
