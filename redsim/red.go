package redsim

import "math"

// REDConfig 是单队列 RED 参数。
type REDConfig struct {
	MinTh float64 // 平均队长下限（包数）：低于它不概率丢包
	MaxTh float64 // 平均队长上限（包数）：达到/超过它强制丢包
	Wq    float64 // 平均队长指数权重
	MaxP  float64 // 最大丢包概率
	// ByteMode 开启包长归一化（长包丢包概率按 Len/MTU 放大）。
	// 该归一化与空闲衰减严格分离，只在丢包判定阶段应用。
	ByteMode bool
	MTU      int
}

// DropReason 说明一次丢包的依据。
type DropReason int

const (
	ReasonNone DropReason = iota
	ReasonForced
	ReasonProbabilistic
)

func (r DropReason) String() string {
	switch r {
	case ReasonForced:
		return "forced(avg>=maxTh)"
	case ReasonProbabilistic:
		return "probabilistic(draw<pa)"
	}
	return "none"
}

// Decision 记录一次到达事件的判定依据（每次丢弃都会输出）。
type Decision struct {
	Tick      int64
	Queue     int
	EventID   uint64
	PktLen    int
	Avg       float64 // 判定时的平均队长
	IdleTicks int64   // 本次判定时应用的空闲衰减长度（0 表示未衰减）
	Pb        float64 // 归一化后的基础丢包概率
	Pa        float64 // 经 count 修正后的实际丢包概率
	Draw      float64 // 随机抽取值
	Dropped   bool
	Reason    DropReason
}

// RED 保存单队列的早丢弃状态。
// 平均队长更新（UpdateAvg）与空闲衰减（ApplyIdleDecay）是两个独立方法。
type RED struct {
	Cfg       REDConfig
	Avg       float64
	count     int   // 自上次丢包以来被接受的包数
	idle      bool  // 队列当前是否为空
	idleSince int64 // 队列变空的 tick
}

func NewRED(cfg REDConfig) *RED { return &RED{Cfg: cfg, idle: true} }

// ApplyIdleDecay 对空闲 m 个 tick 的平均队长做衰减：avg *= (1-wq)^m。
// 仅在队列由空转为有包到达时调用，与常规平均更新严格分离。
func (r *RED) ApplyIdleDecay(m int64) {
	if m <= 0 {
		return
	}
	r.Avg *= math.Pow(1-r.Cfg.Wq, float64(m))
}

// UpdateAvg 是忙时的常规指数平均更新：avg = (1-wq)*avg + wq*qLen。
// 不做任何空闲衰减。
func (r *RED) UpdateAvg(qLen int) {
	r.Avg = (1-r.Cfg.Wq)*r.Avg + r.Cfg.Wq*float64(qLen)
}

// NoteEmpty 由队列在排空时调用，记录空闲起点。
func (r *RED) NoteEmpty(now int64) {
	r.idle = true
	r.idleSince = now
}

// NormalizeProb 做包长归一化：ByteMode 下 pb *= pktLen/MTU。
// 与时间维度的空闲衰减完全无关。
func NormalizeProb(cfg REDConfig, pb float64, pktLen int) float64 {
	if !cfg.ByteMode || cfg.MTU <= 0 {
		return pb
	}
	return pb * float64(pktLen) / float64(cfg.MTU)
}

// Decide 判定一个到达包。now 为当前整数 tick；eventID 是到达事件身份；
// src 是（按事件身份派生的）随机源，仅在概率区间才会被取样。
func (r *RED) Decide(now int64, eventID uint64, queue, qLen, pktLen int, src Source) Decision {
	d := Decision{Tick: now, Queue: queue, EventID: eventID, PktLen: pktLen}
	if qLen == 0 && r.idle {
		// 空闲衰减路径：只衰减，不做常规平均更新。
		d.IdleTicks = now - r.idleSince
		r.ApplyIdleDecay(d.IdleTicks)
		r.idle = false
	} else {
		// 常规平均更新路径：不含任何空闲衰减。
		r.UpdateAvg(qLen)
	}
	d.Avg = r.Avg
	switch {
	case r.Avg < r.Cfg.MinTh:
		// 阈值下：不概率丢包。
		r.count++
	case r.Avg >= r.Cfg.MaxTh:
		// 阈值上：按规则强制丢。
		d.Dropped, d.Reason = true, ReasonForced
		r.count = 0
	default:
		pb := r.Cfg.MaxP * (r.Avg - r.Cfg.MinTh) / (r.Cfg.MaxTh - r.Cfg.MinTh)
		pb = NormalizeProb(r.Cfg, pb, pktLen)
		pa := pb / (1 - float64(r.count)*pb)
		if pa > 1 {
			pa = 1
		}
		if pa < 0 {
			pa = 0
		}
		d.Pb, d.Pa = pb, pa
		d.Draw = Float64(src.Uint64())
		if d.Draw < pa {
			d.Dropped, d.Reason = true, ReasonProbabilistic
			r.count = 0
		} else {
			r.count++
		}
	}
	return d
}
