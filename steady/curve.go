package steady

import "math"

// FailCurve 描述节点的负载-故障率响应：给定归一化负载 load/capacity，
// 返回（0,1] 内的请求失败概率。实现必须在负载超过软阈值后单调不减。
type FailCurve interface {
	PFail(loadRatio float64) float64
}

// LinearCurve 是分段线性故障率曲线：
//
//	loadRatio <= soft 时 p = Base；
//	soft < loadRatio <= hard 时 p 线性上升到 PAtHard；
//	loadRatio > hard 时 p 继续按相同斜率上升并截断到 1。
type LinearCurve struct {
	Soft    float64 // 软阈值（相对硬容量的比例），超过后故障率单调上升
	Base    float64 // 软阈值内的基础故障率
	PAtHard float64 // 恰好打满硬容量时的故障率
}

// PFail 实现 FailCurve。
func (c LinearCurve) PFail(loadRatio float64) float64 {
	if math.IsNaN(loadRatio) || math.IsInf(loadRatio, 0) {
		return 1
	}
	if loadRatio <= c.Soft {
		return c.Base
	}
	slope := (c.PAtHard - c.Base) / math.Max(1e-12, 1.0-c.Soft)
	p := c.Base + slope*(loadRatio-c.Soft)
	if p < 0 {
		p = 0
	}
	if p > 1 {
		p = 1
	}
	return p
}

// ConstCurve 返回恒定故障率，用于测试与基线场景。
type ConstCurve struct{ P float64 }

// PFail 实现 FailCurve。
func (c ConstCurve) PFail(loadRatio float64) float64 {
	if c.P < 0 {
		return 0
	}
	if c.P > 1 {
		return 1
	}
	return c.P
}
