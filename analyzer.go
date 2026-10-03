package swimlab

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// EventKind 描述注入事件的类型。
type EventKind int

const (
	// CrashEvent 真实节点宕机。
	CrashEvent EventKind = iota
	// DisturbanceEvent 短暂网络扰动（丢包），节点实际存活。
	DisturbanceEvent
)

func (k EventKind) String() string {
	switch k {
	case CrashEvent:
		return "节点宕机"
	case DisturbanceEvent:
		return "网络丢包"
	default:
		return "未知"
	}
}

// FaultEvent 一次故障/扰动注入事件（事实层）。
type FaultEvent struct {
	ID         int
	Kind       EventKind
	NodeID     int
	OccurredAt int // 发生时刻（协议周期）
}

// Detection 分析仪对单个事件的观测结论。
type Detection struct {
	Event            FaultEvent
	FirstSuspectedAt int  // 首次怀疑时刻，-1 表示从未被怀疑
	MarkedAt         int  // 最终标记时刻，-1 表示未被标记
	Cleared          bool // 怀疑被澄清（未发展为标记）
}

// Detected 报告该事件是否被最终标记。
func (d Detection) Detected() bool { return d.MarkedAt >= 0 }

// MTTD 返回该事件的检测时间（协议周期）。未检测到时返回 -1。
func (d Detection) MTTD() float64 {
	if !d.Detected() {
		return -1
	}
	return float64(d.MarkedAt - d.Event.OccurredAt)
}

// Analyzer 汇总注入事件与检测器观测，计算 MTTD 与误报率。
type Analyzer struct {
	events   []FaultEvent
	suspects map[int][]int // nodeID -> 怀疑时刻
	marks    map[int][]int // nodeID -> 标记时刻
	clears   map[int][]int // nodeID -> 澄清时刻
}

// NewAnalyzer 创建一个空的分析仪。
func NewAnalyzer() *Analyzer {
	return &Analyzer{
		suspects: make(map[int][]int),
		marks:    make(map[int][]int),
		clears:   make(map[int][]int),
	}
}

// RegisterEvent 登记一次故障/扰动注入事件。
func (a *Analyzer) RegisterEvent(e FaultEvent) {
	a.events = append(a.events, e)
}

// RecordObservation 记录检测器的一次观测。
func (a *Analyzer) RecordObservation(o Observation) {
	switch o.Kind {
	case ObsSuspect:
		a.suspects[o.NodeID] = append(a.suspects[o.NodeID], o.Tick)
	case ObsMark:
		a.marks[o.NodeID] = append(a.marks[o.NodeID], o.Tick)
	case ObsClear:
		a.clears[o.NodeID] = append(a.clears[o.NodeID], o.Tick)
	}
}

// Report 一次完整分析的结论。
type Report struct {
	Detections         []Detection // 按发生时刻排序
	TotalMarks         int         // 检测器发出的标记总数
	FalsePositiveMarks []int       // 无法匹配真实宕机的误报标记时刻
	DetectedCrashes    int
	MissedCrashes      int
	Disturbances       int
	AvgMTTD            float64 // 已检测宕机事件的平均检测时间（周期）
	FalsePositiveRate  float64 // 误报标记占全部标记的比例
}

const maxInt = int(^uint(0) >> 1)

// Analyze 将观测与事件按节点和时间窗口匹配，生成报告。
func (a *Analyzer) Analyze() Report {
	events := append([]FaultEvent(nil), a.events...)
	sort.Slice(events, func(i, j int) bool {
		if events[i].OccurredAt != events[j].OccurredAt {
			return events[i].OccurredAt < events[j].OccurredAt
		}
		return events[i].ID < events[j].ID
	})

	byNode := make(map[int][]FaultEvent)
	for _, e := range events {
		byNode[e.NodeID] = append(byNode[e.NodeID], e)
	}
	// 事件的观测窗口为 [发生时刻, 同节点下一事件发生时刻)。
	windowEnd := func(e FaultEvent) int {
		list := byNode[e.NodeID]
		for i, cur := range list {
			if cur.ID == e.ID && i+1 < len(list) {
				return list[i+1].OccurredAt
			}
		}
		return maxInt
	}
	firstIn := func(ticks []int, start, end int) int {
		best := -1
		for _, t := range ticks {
			if t >= start && t < end && (best < 0 || t < best) {
				best = t
			}
		}
		return best
	}
	anyIn := func(ticks []int, start, end int) bool {
		for _, t := range ticks {
			if t >= start && t < end {
				return true
			}
		}
		return false
	}

	rep := Report{}
	consumed := make(map[int]map[int]bool) // nodeID -> 已匹配事件的标记时刻
	var mttdSum float64

	for _, e := range events {
		end := windowEnd(e)
		d := Detection{
			Event:            e,
			FirstSuspectedAt: firstIn(a.suspects[e.NodeID], e.OccurredAt, end),
			MarkedAt:         firstIn(a.marks[e.NodeID], e.OccurredAt, end),
		}
		if d.FirstSuspectedAt >= 0 && d.MarkedAt < 0 {
			d.Cleared = anyIn(a.clears[e.NodeID], d.FirstSuspectedAt, end)
		}
		if d.MarkedAt >= 0 {
			if consumed[e.NodeID] == nil {
				consumed[e.NodeID] = make(map[int]bool)
			}
			consumed[e.NodeID][d.MarkedAt] = true
		}
		rep.Detections = append(rep.Detections, d)

		switch e.Kind {
		case CrashEvent:
			if d.Detected() {
				rep.DetectedCrashes++
				mttdSum += d.MTTD()
			} else {
				rep.MissedCrashes++
			}
		case DisturbanceEvent:
			rep.Disturbances++
			if d.Detected() {
				// 扰动事件被标记 = 误报。
				rep.FalsePositiveMarks = append(rep.FalsePositiveMarks, d.MarkedAt)
			}
		}
	}

	// 落在任何事件窗口之外的标记同样是误报。
	for nodeID, ticks := range a.marks {
		for _, t := range ticks {
			if !consumed[nodeID][t] {
				rep.FalsePositiveMarks = append(rep.FalsePositiveMarks, t)
			}
		}
		rep.TotalMarks += len(ticks)
	}
	sort.Ints(rep.FalsePositiveMarks)

	if rep.DetectedCrashes > 0 {
		rep.AvgMTTD = mttdSum / float64(rep.DetectedCrashes)
	}
	if rep.TotalMarks > 0 {
		rep.FalsePositiveRate = float64(len(rep.FalsePositiveMarks)) / float64(rep.TotalMarks)
	}
	return rep
}

func formatTick(t int) string {
	if t < 0 {
		return "-"
	}
	return strconv.Itoa(t)
}

func (d Detection) conclusion() string {
	switch d.Event.Kind {
	case CrashEvent:
		if d.Detected() {
			return "检测成功"
		}
		return "漏报!"
	case DisturbanceEvent:
		if d.Detected() {
			return "误报!"
		}
		if d.Cleared {
			return "怀疑后澄清(无误报)"
		}
		return "未触发怀疑"
	default:
		return ""
	}
}

// String 输出详尽诊断报告。
func (r Report) String() string {
	var b strings.Builder
	b.WriteString("===== SWIM 故障检测诊断报告 =====\n")
	b.WriteString("事件明细:\n")
	fmt.Fprintf(&b, "  %-4s %-10s %-6s %-10s %-10s %-10s %-12s %s\n",
		"ID", "类型", "节点", "发生时刻", "首次怀疑", "最终标记", "MTTD(周期)", "结论")
	for _, d := range r.Detections {
		mttd := "-"
		if d.Detected() {
			mttd = fmt.Sprintf("%.1f", d.MTTD())
		}
		fmt.Fprintf(&b, "  %-4d %-10s N%-5d %-10d %-10s %-10s %-12s %s\n",
			d.Event.ID, d.Event.Kind, d.Event.NodeID, d.Event.OccurredAt,
			formatTick(d.FirstSuspectedAt), formatTick(d.MarkedAt), mttd, d.conclusion())
	}
	b.WriteString("汇总:\n")
	fmt.Fprintf(&b, "  真实宕机事件: %d, 成功检测: %d, 漏报: %d\n",
		r.DetectedCrashes+r.MissedCrashes, r.DetectedCrashes, r.MissedCrashes)
	fmt.Fprintf(&b, "  扰动事件: %d, 被误标记: %d\n", r.Disturbances, len(r.FalsePositiveMarks))
	fmt.Fprintf(&b, "  平均检测时间 MTTD: %.1f 周期\n", r.AvgMTTD)
	fmt.Fprintf(&b, "  标记总数: %d, 误报标记: %d\n", r.TotalMarks, len(r.FalsePositiveMarks))
	fmt.Fprintf(&b, "  误报率: %.1f%%\n", r.FalsePositiveRate*100)
	return b.String()
}
