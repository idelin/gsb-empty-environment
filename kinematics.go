// Package kinematics 实现移动物体时空范围速度与加速度极值探测：
// 在流式轨迹上基于滑动窗口多项式拟合（Savitzky-Golay 风格）估计
// 一阶瞬时速度与二阶加速度，并对超速、急加速、急刹车、
// 向心加速度超限（侧翻风险）输出完整证据链。
package kinematics

import "math"

// Sample 是一条轨迹采样点。T 为秒（单调递增），Pos 为多维空间坐标。
type Sample struct {
	T   float64
	Pos []float64
}

// Point 是某一时刻的运动学状态（由滑动窗口多项式拟合得到）。
type Point struct {
	T           float64   // 时刻（秒）
	Pos         []float64 // 平滑后的位置
	Vel         []float64 // 瞬时速度向量
	Speed       float64   // 速率 |v|
	Acc         []float64 // 瞬时加速度向量
	Tangential  float64   // 切向加速度（沿速度方向，正为加速、负为刹车）
	Centripetal float64   // 法向（向心）加速度幅值
}

// differentiator 用滑动窗口多项式拟合从离散轨迹估计导数。
// 窗口内时间归一化到 [-1,1] 保证数值稳定；位置、速度、加速度
// 由拟合多项式在窗口内任意时刻解析求值，从而平滑 GPS 定位噪声。
type differentiator struct {
	window int // 窗口样本数（奇数）
	degree int // 多项式阶数（>=2 才能估计加速度）
}

func newDifferentiator(window, degree int) differentiator {
	if window < degree+2 {
		window = degree + 2
	}
	if window%2 == 0 {
		window++
	}
	if degree < 2 {
		degree = 2
	}
	return differentiator{window: window, degree: degree}
}

// eval 在窗口 win 内对索引 evalIdx 处的时刻求运动学状态。
// win 不必以 evalIdx 为中心（流末尾可用固定窗口对尾部时刻求值）。
func (d differentiator) eval(win []Sample, evalIdx int) Point {
	tRef := win[len(win)/2].T
	halfSpan := (win[len(win)-1].T - win[0].T) / 2
	if halfSpan <= 0 {
		halfSpan = 1
	}
	dims := len(win[0].Pos)
	tau := make([]float64, len(win))
	for i, s := range win {
		tau[i] = (s.T - tRef) / halfSpan
	}

	p := Point{T: win[evalIdx].T, Pos: make([]float64, dims), Vel: make([]float64, dims), Acc: make([]float64, dims)}
	tauE := (p.T - tRef) / halfSpan
	// 预计算 tauE 的幂，避免在中心点（tauE==0）出现 0/0。
	powTau := make([]float64, d.degree+1)
	powTau[0] = 1
	for k := 1; k <= d.degree; k++ {
		powTau[k] = powTau[k-1] * tauE
	}
	for dim := 0; dim < dims; dim++ {
		ys := make([]float64, len(win))
		for i, s := range win {
			ys[i] = s.Pos[dim]
		}
		c := polyFit(tau, ys, d.degree)
		var pos, vel, acc float64
		for k := 0; k <= d.degree; k++ {
			pos += c[k] * powTau[k]
			if k >= 1 {
				vel += float64(k) * c[k] * powTau[k-1]
			}
			if k >= 2 {
				acc += float64(k) * float64(k-1) * c[k] * powTau[k-2]
			}
		}
		p.Pos[dim] = pos
		p.Vel[dim] = vel / halfSpan
		p.Acc[dim] = acc / (halfSpan * halfSpan)
	}

	for dim := 0; dim < dims; dim++ {
		p.Speed += p.Vel[dim] * p.Vel[dim]
	}
	p.Speed = math.Sqrt(p.Speed)

	if p.Speed > 1e-9 {
		var along float64
		for dim := 0; dim < dims; dim++ {
			along += p.Acc[dim] * p.Vel[dim] / p.Speed
		}
		p.Tangential = along
		var normSq float64
		for dim := 0; dim < dims; dim++ {
			normSq += p.Acc[dim] * p.Acc[dim]
		}
		if rest := normSq - along*along; rest > 0 {
			p.Centripetal = math.Sqrt(rest)
		}
	} else {
		for dim := 0; dim < dims; dim++ {
			p.Tangential += p.Acc[dim] * p.Acc[dim]
		}
		p.Tangential = math.Sqrt(p.Tangential)
	}
	return p
}
