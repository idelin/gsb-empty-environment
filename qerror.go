package main

import "math"

// QError 计算估算值与真实值的偏差倍率（Q-Error）。
// Q-Error = max(est, act) / min(est, act)，恒 >= 1，越接近 1 越准。
// 任一值为 0 时按 1 处理以避免除零，同时保留数量级差异。
func QError(est, act float64) float64 {
	if est < 0 || act < 0 {
		panic("q-error: negative input")
	}
	if est == 0 {
		est = 1
	}
	if act == 0 {
		act = 1
	}
	hi := math.Max(est, act)
	lo := math.Min(est, act)
	return hi / lo
}

// Severity 告警级别。
type Severity int

const (
	OK Severity = iota
	Warn
	Critical
)

func (s Severity) String() string {
	switch s {
	case Warn:
		return "WARN"
	case Critical:
		return "CRITICAL"
	default:
		return "OK"
	}
}

// 偏差倍率阈值：>=4 倍告警，>=10 倍严重告警。
const (
	WarnThreshold     = 4.0
	CriticalThreshold = 10.0
)

// SeverityOf 根据偏差倍率给出告警级别。
func SeverityOf(q float64) Severity {
	switch {
	case q >= CriticalThreshold:
		return Critical
	case q >= WarnThreshold:
		return Warn
	default:
		return OK
	}
}
