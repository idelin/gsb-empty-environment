package kinematics

import "fmt"

// ViolationType 违规类型。
type ViolationType string

const (
	Overspeed            ViolationType = "overspeed"             // 超速
	HarshAcceleration    ViolationType = "harsh_acceleration"    // 急加速
	HarshBraking         ViolationType = "harsh_braking"         // 急刹车
	ExcessiveCentripetal ViolationType = "excessive_centripetal" // 向心加速度超限（侧翻风险）
)

// Thresholds 各维运动学指标的安全阈值（SI 单位）。
type Thresholds struct {
	MaxSpeed               float64 // 最大允许速率 m/s
	MaxAcceleration        float64 // 最大切向加速度 m/s²（超过即急加速）
	MaxDeceleration        float64 // 最大制动减速度 m/s²（切向加速度低于 -MaxDeceleration 即急刹车）
	MaxCentripetal         float64 // 最大向心加速度 m/s²（侧翻安全阈值）
	MinSpeedForCentripetal float64 // 判定向心加速度的最低速率，避免低速静止时误报
}

// Violation 是单次采样时刻命中的违规记录。
type Violation struct {
	Type  ViolationType
	T     float64
	Value float64
	Limit float64
}

func (v Violation) String() string {
	return fmt.Sprintf("%s t=%.1fs value=%.2f limit=%.2f", v.Type, v.T, v.Value, v.Limit)
}

// Event 是一次完整违规事件及其证据链：
// 覆盖首次违规前 PreEvent 秒到最后一次违规后 PostEvent 秒的完整运动学曲线。
type Event struct {
	ID         int
	Start      float64 // 首次违规时刻
	End        float64 // 最后一次违规时刻
	Violations []Violation
	Peak       Violation // 超限量最大的违规
	Curve      []Point   // 证据链：违规前后完整运动学曲线
}

// Types 返回事件中出现的违规类型（按首次出现顺序）。
func (e Event) Types() []ViolationType {
	seen := map[ViolationType]bool{}
	var out []ViolationType
	for _, v := range e.Violations {
		if !seen[v.Type] {
			seen[v.Type] = true
			out = append(out, v.Type)
		}
	}
	return out
}

// Config 探测器配置。
type Config struct {
	WindowSize     int     // 滑动窗口样本数（奇数，越大越平滑）
	PolyDegree     int     // 拟合多项式阶数（>=2）
	PreEvent       float64 // 证据链向前保留秒数
	PostEvent      float64 // 最后一次违规后继续保留秒数（事件静默这么久即结案）
	TriggerSamples int     // 连续违规多少个点才立案（防抖，<=1 表示逐点立案）
	Thresholds     Thresholds
	OnEvent        func(Event) // 事件结案回调（流式输出）
}

// Detector 流式多维运动学指标违规监测器。
type Detector struct {
	cfg    Config
	diff   differentiator
	th     Thresholds
	buf    []Sample // 原始采样缓冲
	next   int      // 下一个待计算运动学状态的样本索引
	recent []Point  // 未违规的近期运动学点（供证据链回溯）
	active *Event
	lastV  float64     // 最近一次违规时刻
	streak []Violation // 连续违规暂存（用于防抖立案）
	events []Event
	seq    int
}

func NewDetector(cfg Config) *Detector {
	d := &Detector{cfg: cfg, th: cfg.Thresholds}
	d.diff = newDifferentiator(cfg.WindowSize, cfg.PolyDegree)
	d.next = d.diff.window / 2
	d.lastV = -1e18
	return d
}

// Add 送入一条轨迹采样点，内部增量计算运动学状态并驱动违规状态机。
func (d *Detector) Add(s Sample) {
	d.buf = append(d.buf, s)
	half := d.diff.window / 2
	// 窗口已覆盖到中心点 d.next 时即可计算
	for d.next >= half && d.next+half < len(d.buf) {
		d.emit(d.diff.eval(d.buf[d.next-half:d.next+half+1], half))
		d.next++
	}
}

// Flush 在轨迹结束时调用：用末尾固定窗口补算尾部样本，并结案未完结事件。
func (d *Detector) Flush() {
	n := len(d.buf)
	w := d.diff.window
	if n < w {
		return // 样本总数不足一个窗口，无法拟合
	}
	if start := n - w; d.next < start {
		d.next = start
	}
	for d.next < n {
		start := n - w
		win := d.buf[start : start+w]
		d.emit(d.diff.eval(win, d.next-start))
		d.next++
	}
	d.closeEvent()
}

// Events 返回已结案的事件列表。
func (d *Detector) Events() []Event { return d.events }

func (d *Detector) emit(p Point) {
	violations := d.check(p)
	if d.active != nil {
		d.active.Curve = append(d.active.Curve, p)
		if len(violations) > 0 {
			d.active.Violations = append(d.active.Violations, violations...)
			d.active.End = p.T
			d.lastV = p.T
			for _, v := range violations {
				if v.Value-v.Limit > d.active.Peak.Value-d.active.Peak.Limit {
					d.active.Peak = v
				}
			}
		} else if p.T-d.lastV > d.cfg.PostEvent {
			d.closeEvent()
			d.remember(p)
		}
		return
	}
	if len(violations) > 0 {
		d.streak = append(d.streak, violations...)
		trigger := d.cfg.TriggerSamples
		if trigger < 1 {
			trigger = 1
		}
		if len(d.streak) < trigger {
			return // 连续违规不足，暂不立案
		}
		d.seq++
		start := d.streak[0].T
		ev := Event{ID: d.seq, Start: start, End: p.T, Violations: append([]Violation(nil), d.streak...), Peak: d.streak[0]}
		for _, v := range ev.Violations[1:] {
			if v.Value-v.Limit > ev.Peak.Value-ev.Peak.Limit {
				ev.Peak = v
			}
		}
		// 证据链回溯：附上违规前 PreEvent 秒的运动学曲线
		for _, q := range d.recent {
			if q.T >= start-d.cfg.PreEvent {
				ev.Curve = append(ev.Curve, q)
			}
		}
		ev.Curve = append(ev.Curve, p)
		d.active = &ev
		d.lastV = p.T
		d.streak = nil
		d.recent = d.recent[:0]
		return
	}
	d.streak = d.streak[:0]
	d.remember(p)
}

func (d *Detector) remember(p Point) {
	d.recent = append(d.recent, p)
	cut := p.T - d.cfg.PreEvent
	i := 0
	for i < len(d.recent) && d.recent[i].T < cut {
		i++
	}
	d.recent = append([]Point(nil), d.recent[i:]...)
}

func (d *Detector) closeEvent() {
	if d.active == nil {
		return
	}
	ev := *d.active
	d.active = nil
	d.events = append(d.events, ev)
	if d.cfg.OnEvent != nil {
		d.cfg.OnEvent(ev)
	}
}

// check 对单个运动学点做全维度阈值判定。
func (d *Detector) check(p Point) []Violation {
	var out []Violation
	th := d.th
	if th.MaxSpeed > 0 && p.Speed > th.MaxSpeed {
		out = append(out, Violation{Overspeed, p.T, p.Speed, th.MaxSpeed})
	}
	if th.MaxAcceleration > 0 && p.Tangential > th.MaxAcceleration {
		out = append(out, Violation{HarshAcceleration, p.T, p.Tangential, th.MaxAcceleration})
	}
	if th.MaxDeceleration > 0 && p.Tangential < -th.MaxDeceleration {
		out = append(out, Violation{HarshBraking, p.T, -p.Tangential, th.MaxDeceleration})
	}
	if th.MaxCentripetal > 0 && p.Speed >= th.MinSpeedForCentripetal && p.Centripetal > th.MaxCentripetal {
		out = append(out, Violation{ExcessiveCentripetal, p.T, p.Centripetal, th.MaxCentripetal})
	}
	return out
}
