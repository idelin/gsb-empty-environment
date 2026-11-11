package raster

// Framebuffer 是离屏渲染目标。
//
// Color 按行优先存放深度值为 NDC z（见 DepthFunc），Samples 记录每个
// 像素实际被着色的次数，用于核对“每个像素恰好着色一次”。
type Framebuffer struct {
	Width      int
	Height     int
	Color      []float64
	Depth      []float64
	Samples    []int
	clearDepth float64
}

// NewFramebuffer 创建指定尺寸的帧缓冲，深度被初始化为 clearDepth。
func NewFramebuffer(width, height int, clearDepth float64) *Framebuffer {
	if width <= 0 || height <= 0 {
		panic("raster: framebuffer dimensions must be positive")
	}
	fb := &Framebuffer{
		Width:      width,
		Height:     height,
		Color:      make([]float64, width*height),
		Depth:      make([]float64, width*height),
		Samples:    make([]int, width*height),
		clearDepth: clearDepth,
	}
	fb.Clear()
	return fb
}

// Clear 把颜色清零、深度重置为清除值、采样计数清零。
func (fb *Framebuffer) Clear() {
	for i := range fb.Color {
		fb.Color[i] = 0
		fb.Depth[i] = fb.clearDepth
		fb.Samples[i] = 0
	}
}

// TotalSamples 返回全画面累计着色次数。
func (fb *Framebuffer) TotalSamples() int {
	total := 0
	for _, n := range fb.Samples {
		total += n
	}
	return total
}

// SamplesAt 返回指定像素的着色次数。
func (fb *Framebuffer) SamplesAt(x, y int) int {
	return fb.Samples[y*fb.Width+x]
}
