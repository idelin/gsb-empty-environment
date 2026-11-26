// Package outlier 实现服务网格数据面的异常点摘除（Outlier Detection）状态机。
//
// 状态机按毫秒时间戳驱动，支持三类摘除触发器：
//   - 连续 5xx 摘除（Consecutive5xxThreshold）
//   - 连续本地源（网关）错误摘除（ConsecutiveGatewayFailureThreshold）
//   - 统计周期成功率 + 总体标准差摘除（SuccessRate* / StdevFactor）
//
// 所有摘除都受最大摘除比例帽 MaxEjectionPercent 保护，摘除时长按
// min(MaxEjectionTime, BaseEjectionTime * EjectionCount) 退避倍增，
// 主机恢复后连续健康运行满一个完整统计周期则递减 EjectionCount。
package outlier

import (
	"errors"
	"fmt"
	"math"
)

// EjectionReason 标识触发摘除判定的原因。
type EjectionReason int

const (
	ReasonConsecutive5xx            EjectionReason = iota + 1 // 连续 5xx 达标
	ReasonConsecutiveGatewayFailure                           // 连续本地源错误达标
	ReasonSuccessRate                                         // 周期成功率低于统计阈值
)

// String 返回摘除原因的可读名称。
func (r EjectionReason) String() string {
	switch r {
	case ReasonConsecutive5xx:
		return "consecutive_5xx"
	case ReasonConsecutiveGatewayFailure:
		return "consecutive_gateway_failure"
	case ReasonSuccessRate:
		return "success_rate"
	default:
		return "unknown"
	}
}

// Config 是异常点摘除的全部可调参数，时间单位均为毫秒。
type Config struct {
	// Consecutive5xxThreshold 为连续 5xx 摘除阈值，<=0 表示禁用该触发器。
	Consecutive5xxThreshold int
	// ConsecutiveGatewayFailureThreshold 为连续本地源错误摘除阈值，<=0 表示禁用。
	ConsecutiveGatewayFailureThreshold int
	// Interval 为成功率统计周期长度（毫秒），也是退避衰减计时单位。
	Interval int64
	// SuccessRateMinHosts 为执行标准差统计所需的最小有效主机数。
	SuccessRateMinHosts int
	// SuccessRateReqVolume 为主机进入统计样本所需的周期内最小请求数。
	SuccessRateReqVolume int64
	// StdevFactor 为标准差因子，按 stdev * StdevFactor / 1000 参与阈值计算。
	StdevFactor int64
	// BaseEjectionTime 为基础摘除时长（毫秒）。
	BaseEjectionTime int64
	// MaxEjectionTime 为摘除时长上限（毫秒）；<=0 表示不限制。
	MaxEjectionTime int64
	// MaxEjectionPercent 为最大摘除百分比，取值 0..100。
	MaxEjectionPercent int
}

// RequestResult 描述一个按毫秒时间戳到达的请求结果。
type RequestResult struct {
	TimeMs int64 // 到达时刻（毫秒）
	Host   string
	// StatusCode 为 HTTP 状态码，500..599 记为 5xx。
	StatusCode int
	// LocalOriginFailure 表示连接失败、连接超时等本地源发起失败的网关错误。
	LocalOriginFailure bool
}

// Is5xx 判断该结果是否为 5xx 响应。
func (r RequestResult) Is5xx() bool {
	return r.StatusCode >= 500 && r.StatusCode <= 599
}

// IsGatewayFailure 判断该结果是否为本地源（网关）错误。
func (r RequestResult) IsGatewayFailure() bool {
	return r.LocalOriginFailure
}

// IsSuccess 判断该结果是否计为成功：非本地源错误且非 5xx。
// 本地源错误优先判定，即使携带 5xx 状态码也属于失败。
func (r RequestResult) IsSuccess() bool {
	return !r.IsGatewayFailure() && !r.Is5xx()
}

// Ejection 记录一次实际生效的摘除。
type Ejection struct {
	TimeMs        int64
	Host          string
	Reason        EjectionReason
	EjectionCount int64 // 递增后的惩罚倍数
	DurationMs    int64 // 实际生效时长（已触顶处理）
	RecoverAtMs   int64 // 精确解禁时刻
}

// BlockedEjection 记录一次达到触发条件但被比例帽拦截的摘除判定。
type BlockedEjection struct {
	TimeMs int64
	Host   string
	Reason EjectionReason
}

// Recovery 记录一次到期自动恢复。
type Recovery struct {
	TimeMs int64
	Host   string
}

// Outcome 是单个请求结果处理后的决策结果。
type Outcome struct {
	Ejected   *Ejection
	Blocked   *BlockedEjection
	Recovered []Recovery
}

// HostState 是一台主机在某一时刻的完整快照。
type HostState struct {
	Host                    string
	Ejected                 bool
	EjectionCount           int64
	RecoverAtMs             int64
	Consecutive5xx          int
	ConsecutiveGatewayError int
	RequestsInWindow        int64
	SuccessesInWindow       int64
	// HealthySinceMs 为最近一次进入健康（未摘除）状态的时刻；-1 表示无需衰减。
	HealthySinceMs int64
}

// TickReport 是一次统计周期事件的完整处理报告。
type TickReport struct {
	TimeMs int64
	// Recovered 为本周期先于统计执行的到期恢复。
	Recovered []Recovery
	// Ejected 为本周期成功率统计实际摘除的主机，按主机池顺序排列。
	Ejected []Ejection
	// Blocked 为达到成功率阈值但被比例帽拦截的主机。
	Blocked []BlockedEjection
	// EligibleHosts 为请求量达标的有效主机数。
	EligibleHosts int
	// MeanSuccessRate 为有效主机成功率均值（百分比），不足 minHosts 时为 0。
	MeanSuccessRate float64
	// StdevSuccessRate 为有效主机成功率的总体标准差（百分比）。
	StdevSuccessRate float64
	// Threshold 为 mean - stdev*StdevFactor/1000，不足 minHosts 时为 0。
	Threshold float64
	// StatsEvaluated 表示本周期是否实际执行了均值/标准差统计。
	StatsEvaluated bool
}

// hostRuntime 是主机的内部可变状态。
type hostRuntime struct {
	name                    string
	ejected                 bool
	ejectionCount           int64
	recoverAtMs             int64
	consecutive5xx          int
	consecutiveGatewayError int
	windowRequests          int64
	windowSuccesses         int64
	healthySinceMs          int64
}

func (h *hostRuntime) snapshot() HostState {
	return HostState{
		Host:                    h.name,
		Ejected:                 h.ejected,
		EjectionCount:           h.ejectionCount,
		RecoverAtMs:             h.recoverAtMs,
		Consecutive5xx:          h.consecutive5xx,
		ConsecutiveGatewayError: h.consecutiveGatewayError,
		RequestsInWindow:        h.windowRequests,
		SuccessesInWindow:       h.windowSuccesses,
		HealthySinceMs:          h.healthySinceMs,
	}
}

// Detector 是异常点摘除状态机；单实例不保证并发安全，
// 事件必须按 TimeMs 非递减顺序投递。
type Detector struct {
	cfg   Config
	hosts []*hostRuntime
	index map[string]*hostRuntime
	nowMs int64
}

// New 校验配置并基于给定主机池（顺序即处理顺序）构造状态机。
func New(hosts []string, cfg Config) (*Detector, error) {
	if len(hosts) == 0 {
		return nil, errors.New("outlier: 主机池不能为空")
	}
	if cfg.Interval <= 0 {
		return nil, errors.New("outlier: Interval 必须为正数")
	}
	if cfg.BaseEjectionTime <= 0 {
		return nil, errors.New("outlier: BaseEjectionTime 必须为正数")
	}
	if cfg.MaxEjectionPercent < 0 || cfg.MaxEjectionPercent > 100 {
		return nil, errors.New("outlier: MaxEjectionPercent 必须位于 [0,100]")
	}
	if cfg.SuccessRateMinHosts < 0 {
		return nil, errors.New("outlier: SuccessRateMinHosts 不能为负")
	}
	if cfg.SuccessRateReqVolume < 0 {
		return nil, errors.New("outlier: SuccessRateReqVolume 不能为负")
	}

	index := make(map[string]*hostRuntime, len(hosts))
	runtimes := make([]*hostRuntime, 0, len(hosts))
	for _, name := range hosts {
		if name == "" {
			return nil, errors.New("outlier: 主机标识不能为空")
		}
		if _, dup := index[name]; dup {
			return nil, fmt.Errorf("outlier: 主机标识重复: %q", name)
		}
		h := &hostRuntime{name: name, healthySinceMs: -1}
		index[name] = h
		runtimes = append(runtimes, h)
	}
	return &Detector{cfg: cfg, hosts: runtimes, index: index}, nil
}

// NowMs 返回状态机已经处理到的当前时刻（毫秒）。
func (d *Detector) NowMs() int64 { return d.nowMs }

// Hosts 按主机池顺序返回全部主机的状态快照。
func (d *Detector) Hosts() []HostState {
	out := make([]HostState, len(d.hosts))
	for i, h := range d.hosts {
		out[i] = h.snapshot()
	}
	return out
}

// Host 返回指定主机的状态快照。
func (d *Detector) Host(name string) (HostState, bool) {
	h, ok := d.index[name]
	if !ok {
		return HostState{}, false
	}
	return h.snapshot(), true
}

// IsEjected 查询主机当前是否处于摘除状态（含到期惰性恢复判断）。
func (d *Detector) IsEjected(name string) bool {
	h, ok := d.index[name]
	if !ok {
		return false
	}
	return h.ejected && d.nowMs < h.recoverAtMs
}

// EjectedCount 返回当前实际处于摘除状态（未到期）的主机数。
func (d *Detector) EjectedCount() int {
	return d.countEjected(d.nowMs)
}

// ApplyResult 处理一个请求结果：先惰性执行到期恢复，再更新连续错误与窗口统计，
// 连续错误达标时发起摘除判定。返回的 Outcome 描述恢复与摘除决策。
func (d *Detector) ApplyResult(r RequestResult) (*Outcome, error) {
	h, ok := d.index[r.Host]
	if !ok {
		return nil, fmt.Errorf("outlier: 未知主机 %q", r.Host)
	}
	if r.TimeMs < d.nowMs {
		return nil, fmt.Errorf("outlier: 事件时间倒退: %d < %d", r.TimeMs, d.nowMs)
	}

	outcome := &Outcome{}
	d.nowMs = r.TimeMs
	outcome.Recovered = d.recoverExpired(r.TimeMs)

	// 窗口统计对所有到达结果生效（含摘除期间的探活流量）。
	h.windowRequests++
	if r.IsSuccess() {
		h.windowSuccesses++
		h.consecutive5xx = 0
		h.consecutiveGatewayError = 0
	} else {
		if r.Is5xx() {
			h.consecutive5xx++
		} else {
			// 非 5xx 失败（如 4xx）打断 5xx 连续链。
			h.consecutive5xx = 0
		}
		if r.IsGatewayFailure() {
			h.consecutiveGatewayError++
		} else {
			// 非本地源失败打断网关错误连续链。
			h.consecutiveGatewayError = 0
		}
	}

	if d.cfg.Consecutive5xxThreshold > 0 && h.consecutive5xx >= d.cfg.Consecutive5xxThreshold {
		d.decideEjection(h, ReasonConsecutive5xx, r.TimeMs, outcome)
	} else if d.cfg.ConsecutiveGatewayFailureThreshold > 0 &&
		h.consecutiveGatewayError >= d.cfg.ConsecutiveGatewayFailureThreshold {
		d.decideEjection(h, ReasonConsecutiveGatewayFailure, r.TimeMs, outcome)
	}
	return outcome, nil
}

// Advance 将时钟推进到 timeMs 并执行所有到期恢复；不触发统计与衰减。
func (d *Detector) Advance(timeMs int64) ([]Recovery, error) {
	if timeMs < d.nowMs {
		return nil, fmt.Errorf("outlier: 时间倒退: %d < %d", timeMs, d.nowMs)
	}
	d.nowMs = timeMs
	return d.recoverExpired(timeMs), nil
}

// countEjected 返回在 nowMs 时刻仍未到期的摘除主机数。
func (d *Detector) countEjected(nowMs int64) int {
	count := 0
	for _, h := range d.hosts {
		if h.ejected && nowMs < h.recoverAtMs {
			count++
		}
	}
	return count
}

// recoverExpired 恢复所有在 nowMs 时刻已到期（含恰好到期）的主机，
// 并记录其健康计时起点。
func (d *Detector) recoverExpired(nowMs int64) []Recovery {
	var recovered []Recovery
	for _, h := range d.hosts {
		if h.ejected && nowMs >= h.recoverAtMs {
			h.ejected = false
			h.recoverAtMs = 0
			h.consecutive5xx = 0
			h.consecutiveGatewayError = 0
			h.healthySinceMs = nowMs
			recovered = append(recovered, Recovery{TimeMs: nowMs, Host: h.name})
		}
	}
	return recovered
}

// Tick 处理一个统计周期事件：先到期恢复，再做成功率标准差统计摘除，
// 随后对健康运行满一个完整周期且未再被摘除的主机递减惩罚倍数，最后滚动窗口。
func (d *Detector) Tick(timeMs int64) (*TickReport, error) {
	if timeMs < d.nowMs {
		return nil, fmt.Errorf("outlier: 周期事件时间倒退: %d < %d", timeMs, d.nowMs)
	}
	d.nowMs = timeMs
	report := &TickReport{TimeMs: timeMs}
	report.Recovered = d.recoverExpired(timeMs)

	type sample struct {
		host *hostRuntime
		rate float64
	}
	samples := make([]sample, 0, len(d.hosts))
	for _, h := range d.hosts {
		if h.windowRequests >= d.cfg.SuccessRateReqVolume {
			samples = append(samples, sample{
				host: h,
				rate: 100.0 * float64(h.windowSuccesses) / float64(h.windowRequests),
			})
		}
	}
	report.EligibleHosts = len(samples)

	if len(samples) >= d.cfg.SuccessRateMinHosts {
		var sum float64
		for _, s := range samples {
			sum += s.rate
		}
		mean := sum / float64(len(samples))
		var variance float64
		for _, s := range samples {
			diff := s.rate - mean
			variance += diff * diff
		}
		stdev := math.Sqrt(variance / float64(len(samples)))
		threshold := mean - stdev*float64(d.cfg.StdevFactor)/1000.0
		report.StatsEvaluated = true
		report.MeanSuccessRate = mean
		report.StdevSuccessRate = stdev
		report.Threshold = threshold

		// 按主机池顺序逐个判定，每次都重新检查比例帽。
		for _, s := range samples {
			if s.rate < threshold {
				outcome := &Outcome{}
				d.decideEjection(s.host, ReasonSuccessRate, timeMs, outcome)
				if ej := outcome.Ejected; ej != nil {
					report.Ejected = append(report.Ejected, *ej)
				} else if b := outcome.Blocked; b != nil {
					report.Blocked = append(report.Blocked, *b)
				}
			}
		}
	}

	// 退避衰减：恢复后连续健康运行满一个完整统计周期且期间无新摘除。
	for _, h := range d.hosts {
		if h.ejected {
			continue
		}
		if h.ejectionCount > 0 && h.healthySinceMs >= 0 &&
			timeMs-h.healthySinceMs >= d.cfg.Interval {
			h.ejectionCount--
			if h.ejectionCount == 0 {
				h.healthySinceMs = -1
			} else {
				// 重新计时，每个完整周期至多衰减一次。
				h.healthySinceMs = timeMs
			}
		}
	}

	// 滚动到下一统计窗口。
	for _, h := range d.hosts {
		h.windowRequests = 0
		h.windowSuccesses = 0
	}
	return report, nil
}

// decideEjection 执行比例帽检查并在允许时实际摘除，否则记录为被拦截。
func (d *Detector) decideEjection(h *hostRuntime, reason EjectionReason, nowMs int64, outcome *Outcome) {
	currentEjected := d.countEjected(nowMs)
	total := len(d.hosts)
	// 当前已摘除占比严格低于上限；首台在比例上限 > 0 时恒允许。
	allow := currentEjected*100 < d.cfg.MaxEjectionPercent*total
	if currentEjected == 0 && d.cfg.MaxEjectionPercent > 0 {
		allow = true
	}
	if !allow {
		outcome.Blocked = &BlockedEjection{TimeMs: nowMs, Host: h.name, Reason: reason}
		// 与生效摘除一致地清零连续错误，避免同一批错误在每个请求上重复判定。
		h.consecutive5xx = 0
		h.consecutiveGatewayError = 0
		return
	}

	h.ejectionCount++
	duration := d.cfg.BaseEjectionTime * h.ejectionCount
	if d.cfg.MaxEjectionTime > 0 && duration > d.cfg.MaxEjectionTime {
		duration = d.cfg.MaxEjectionTime
	}
	h.ejected = true
	h.recoverAtMs = nowMs + duration
	h.consecutive5xx = 0
	h.consecutiveGatewayError = 0
	h.healthySinceMs = -1

	outcome.Ejected = &Ejection{
		TimeMs:        nowMs,
		Host:          h.name,
		Reason:        reason,
		EjectionCount: h.ejectionCount,
		DurationMs:    duration,
		RecoverAtMs:   h.recoverAtMs,
	}
}
