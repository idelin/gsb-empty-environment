package taskpkg

import (
	"errors"
	"math"
	"sort"
)

// Frame 表示一帧已配准的线性光 RGB 图像。
// 像素按行优先存储，每个像素连续存放 R、G、B 三个 float64 分量。
type Frame struct {
	Width  int
	Height int
	Pix    []float64
}

// NewFrame 创建一张宽 width、高 height 的线性光 RGB 帧。
func NewFrame(width, height int) *Frame {
	return &Frame{Width: width, Height: height, Pix: make([]float64, width*height*3)}
}

func (f *Frame) index(x, y int) int { return (y*f.Width + x) * 3 }

// At 返回 (x, y) 处的线性光 RGB 分量。
func (f *Frame) At(x, y int) (r, g, b float64) {
	i := f.index(x, y)
	return f.Pix[i], f.Pix[i+1], f.Pix[i+2]
}

// Set 设置 (x, y) 处的线性光 RGB 分量。
func (f *Frame) Set(x, y int, r, g, b float64) {
	i := f.index(x, y)
	f.Pix[i], f.Pix[i+1], f.Pix[i+2] = r, g, b
}

// Mask 是两路帧重叠区域的有效性掩码，Pix 按行优先存放，
// 值为 true 的像素才允许缝合线经过。
type Mask struct {
	Width  int
	Height int
	Pix    []bool
}

// NewMask 创建一张宽 width、高 height 的空掩码（默认全部无效）。
func NewMask(width, height int) *Mask {
	return &Mask{Width: width, Height: height, Pix: make([]bool, width*height)}
}

// At 返回 (x, y) 是否为可穿过的有效重叠像素。
func (m *Mask) At(x, y int) bool { return m.Pix[y*m.Width+x] }

// Set 设置 (x, y) 的有效性。
func (m *Mask) Set(x, y int, valid bool) { m.Pix[y*m.Width+x] = valid }

// ErrDisconnected 表示重叠掩码不存在贯穿每一行的 8 连通有效区域，
// 因此不存在满足“每行一个有效像素、相邻行横移不超过一”的缝合线。
var ErrDisconnected = errors.New("taskpkg: 重叠掩码不连通，无法拼接")

// Options 控制缝合线搜索行为。
type Options struct {
	// TemporalWeight 是时间域惩罚权重，代价项为
	// TemporalWeight * |当前缝列坐标 - 前一帧缝列坐标|。
	// 显式设为 0 可在提供 prevSeam 时关闭时间项。
	TemporalWeight float64
}

// Find 在重叠掩码内搜索最小代价缝合线。
//
// 缝合线满足：每行恰有一个像素、像素必须位于掩码有效区域内、
// 相邻行缝像素的列坐标之差绝对值不超过 1。
//
// 单像素代价 = 颜色差（两帧 RGB 欧氏距离平方）
//   - 梯度差（水平、垂直中心差分梯度之差的平方和，边界做像素钳制）
//   - 时间惩罚（prevSeam 非空时，与前一帧缝的列距乘以权重）。
//
// 总代价相同时返回字典序最小的缝合线（自上而下逐行比较列坐标）。
// 掩码不存在贯穿所有行的 8 连通有效区域时返回 ErrDisconnected。
// opts 为 nil 且提供 prevSeam 时，时间惩罚权重默认为 1。
func Find(a, b *Frame, overlap *Mask, prevSeam []int, opts *Options) ([]int, error) {
	if err := validateInputs(a, b, overlap); err != nil {
		return nil, err
	}
	width, height := overlap.Width, overlap.Height
	if prevSeam != nil {
		if len(prevSeam) != height {
			return nil, errors.New("taskpkg: prevSeam 长度必须与帧高度一致")
		}
		for _, x := range prevSeam {
			if x < 0 || x >= width {
				return nil, errors.New("taskpkg: prevSeam 含越界列坐标")
			}
		}
	}
	temporalWeight := 0.0
	switch {
	case opts != nil:
		temporalWeight = opts.TemporalWeight
	case prevSeam != nil:
		temporalWeight = 1.0
	}
	if !hasSpanningComponent(overlap) {
		return nil, ErrDisconnected
	}
	energy := buildEnergy(a, b, overlap, prevSeam, temporalWeight)
	return findSeamDP(overlap, energy), nil
}

func validateInputs(a, b *Frame, overlap *Mask) error {
	if a == nil || b == nil || overlap == nil {
		return errors.New("taskpkg: 帧与掩码均不能为 nil")
	}
	if a.Width <= 0 || a.Height <= 0 {
		return errors.New("taskpkg: 帧尺寸必须为正数")
	}
	if b.Width != a.Width || b.Height != a.Height {
		return errors.New("taskpkg: 两帧尺寸不一致")
	}
	if overlap.Width != a.Width || overlap.Height != a.Height {
		return errors.New("taskpkg: 掩码尺寸必须与帧一致")
	}
	if len(a.Pix) != a.Width*a.Height*3 || len(b.Pix) != b.Width*b.Height*3 {
		return errors.New("taskpkg: 帧像素缓冲区长度与尺寸不符")
	}
	if len(overlap.Pix) != overlap.Width*overlap.Height {
		return errors.New("taskpkg: 掩码缓冲区长度与尺寸不符")
	}
	return nil
}

// validateSeam 检查外部传入的缝合线是否可用于当前掩码。
func validateSeam(overlap *Mask, seam []int) error {
	if len(seam) != overlap.Height {
		return errors.New("taskpkg: 缝合线长度必须与帧高度一致")
	}
	prev := 0
	for y, x := range seam {
		if x < 0 || x >= overlap.Width || !overlap.At(x, y) {
			return errors.New("taskpkg: 缝合线经过无效或越界像素")
		}
		if y > 0 && absInt(x-prev) > 1 {
			return errors.New("taskpkg: 缝合线相邻行横移超过 1")
		}
		prev = x
	}
	return nil
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// hasSpanningComponent 判断是否存在一个 8 连通有效组件覆盖每一行。
// 该条件等价于存在一条每行一个有效像素、相邻行横移不超过 1 的路径。
func hasSpanningComponent(m *Mask) bool {
	width, height := m.Width, m.Height
	label := make([]int, width*height)
	for i := range label {
		label[i] = -1
	}
	rowSeen := make([]bool, height)
	stack := make([]int, 0, width*height)

	for seed := 0; seed < width*height; seed++ {
		if !m.Pix[seed] || label[seed] != -1 {
			continue
		}
		clear(rowSeen)
		coveredRows := 0
		label[seed] = seed
		stack = append(stack[:0], seed)

		for len(stack) > 0 {
			p := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			py := p / width
			px := p % width
			if !rowSeen[py] {
				rowSeen[py] = true
				coveredRows++
			}
			for dy := -1; dy <= 1; dy++ {
				ny := py + dy
				if ny < 0 || ny >= height {
					continue
				}
				for dx := -1; dx <= 1; dx++ {
					if dx == 0 && dy == 0 {
						continue
					}
					nx := px + dx
					if nx < 0 || nx >= width {
						continue
					}
					q := ny*width + nx
					if m.Pix[q] && label[q] == -1 {
						label[q] = seed
						stack = append(stack, q)
					}
				}
			}
		}
		if coveredRows == height {
			return true
		}
	}
	return false
}

func channelAt(f *Frame, x, y, c int) float64 {
	return f.Pix[(y*f.Width+x)*3+c]
}

// colorGradCost 计算单像素的颜色差加梯度差代价。
// 梯度采用中心差分，越界邻居钳制到边界像素，因此边界梯度可能为 0。
func colorGradCost(a, b *Frame, x, y int) float64 {
	width, height := a.Width, a.Height
	xl := max(x-1, 0)
	xr := min(x+1, width-1)
	yu := max(y-1, 0)
	yd := min(y+1, height-1)

	var cost float64
	for c := 0; c < 3; c++ {
		d := channelAt(a, x, y, c) - channelAt(b, x, y, c)
		cost += d * d

		gax := channelAt(a, xr, y, c) - channelAt(a, xl, y, c)
		gbx := channelAt(b, xr, y, c) - channelAt(b, xl, y, c)
		d = gax - gbx
		cost += d * d

		gay := channelAt(a, x, yd, c) - channelAt(a, x, yu, c)
		gby := channelAt(b, x, yd, c) - channelAt(b, x, yu, c)
		d = gay - gby
		cost += d * d
	}
	return cost
}

func buildEnergy(a, b *Frame, m *Mask, prevSeam []int, temporalWeight float64) []float64 {
	width, height := m.Width, m.Height
	energy := make([]float64, width*height)
	inf := math.Inf(1)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			idx := y*width + x
			if !m.Pix[idx] {
				energy[idx] = inf
				continue
			}
			cost := colorGradCost(a, b, x, y)
			if prevSeam != nil && temporalWeight != 0 {
				cost += temporalWeight * math.Abs(float64(x-prevSeam[y]))
			}
			energy[idx] = cost
		}
	}
	return energy
}

// equalCost 以相对容差判定两个累计代价是否视为相等，
// 用于在浮点误差下稳定地执行字典序平局规则。
func equalCost(c1, c2 float64) bool {
	if c1 == c2 {
		return true
	}
	scale := math.Max(1, math.Max(math.Abs(c1), math.Abs(c2)))
	return math.Abs(c1-c2) <= 1e-12*scale
}

// findSeamDP 用逐行动态规划求最小代价缝合线。
//
// 每个单元格只保留到达它的最优路径；当前行候选代价相同时，
// 借助上一行路径的字典序排名选择前缀字典序最小的前驱。
// 整行处理完后按“前驱路径排名 + 当前列坐标”重排，得到本行
// 各单元格最优路径的字典序排名，供下一行使用。
func findSeamDP(m *Mask, energy []float64) []int {
	width, height := m.Width, m.Height
	inf := math.Inf(1)

	parents := make([]int, width*height)
	for i := range parents {
		parents[i] = -1
	}

	dp := make([]float64, width)
	rank := make([]int, width)
	valid := make([]int, 0, width)
	for x := 0; x < width; x++ {
		rank[x] = -1
		if m.Pix[x] {
			dp[x] = energy[x]
			valid = append(valid, x)
		} else {
			dp[x] = inf
		}
	}
	// 首行路径只有一个元素，字典序即列坐标升序。
	sort.Ints(valid)
	for r, x := range valid {
		rank[x] = r
	}

	for y := 1; y < height; y++ {
		nextDP := make([]float64, width)
		nextRank := make([]int, width)
		nextValid := make([]int, 0, width)
		for x := range nextDP {
			nextDP[x] = inf
			nextRank[x] = -1
		}

		for x := 0; x < width; x++ {
			idx := y*width + x
			if !m.Pix[idx] {
				continue
			}
			bestPred := -1
			bestCost := inf
			bestRank := math.MaxInt
			for px := x - 1; px <= x+1; px++ {
				if px < 0 || px >= width || math.IsInf(dp[px], 1) {
					continue
				}
				take := false
				switch {
				case equalCost(dp[px], bestCost):
					take = rank[px] < bestRank
				default:
					take = dp[px] < bestCost
				}
				if take {
					bestPred = px
					bestCost = dp[px]
					bestRank = rank[px]
				}
			}
			if bestPred < 0 {
				// 理论上不会发生：hasSpanningComponent 已保证可达，
				// 此处保留防御性处理，跳过不可达单元格。
				continue
			}
			nextDP[x] = bestCost + energy[idx]
			parents[idx] = bestPred
			nextValid = append(nextValid, x)
		}

		// 路径 = 前驱路径拼接当前列；先按前驱路径排名，再按当前列排序。
		sort.Slice(nextValid, func(i, j int) bool {
			xi, xj := nextValid[i], nextValid[j]
			ri := rank[parents[y*width+xi]]
			rj := rank[parents[y*width+xj]]
			if ri != rj {
				return ri < rj
			}
			return xi < xj
		})
		for r, x := range nextValid {
			nextRank[x] = r
		}

		dp = nextDP
		rank = nextRank
	}

	// 末行选累计代价最小者，代价相同取字典序排名最小者。
	end := -1
	endCost := inf
	endRank := math.MaxInt
	for x := 0; x < width; x++ {
		if math.IsInf(dp[x], 1) {
			continue
		}
		take := false
		switch {
		case equalCost(dp[x], endCost):
			take = rank[x] < endRank
		default:
			take = dp[x] < endCost
		}
		if take {
			end = x
			endCost = dp[x]
			endRank = rank[x]
		}
	}

	seam := make([]int, height)
	x := end
	for y := height - 1; y >= 0; y-- {
		seam[y] = x
		if y > 0 {
			x = parents[y*width+x]
		}
	}
	return seam
}
