package main

import (
	"bytes"
	"fmt"
)

// EventRecord 是分析仪为单次故障注入事件建立的度量档案。
type EventRecord struct {
	Injection         Injection
	FirstSuspectCycle int     // 首次被怀疑的周期，-1 表示从未被怀疑
	MarkedCycle       int     // 最终被标记故障的周期，-1 表示从未被标记
	RefutedCycle      int     // 怀疑被澄清的周期，-1 表示未发生
	DetectionLatency  float64 // 检测时延（周期数），-1 表示漏检
	FalsePositive     bool    // 存活节点被错误标记为故障
	Missed            bool    // 宕机节点未被检出
}

// Analyze 将观测流与注入计划匹配，生成逐事件度量档案。
func Analyze(injections []Injection, observations []Observation) []EventRecord {
	records := make([]EventRecord, 0, len(injections))
	for _, in := range injections {
		rec := EventRecord{
			Injection:         in,
			FirstSuspectCycle: -1,
			MarkedCycle:       -1,
			RefutedCycle:      -1,
			DetectionLatency:  -1,
		}
		for _, ob := range observations {
			if ob.Target != in.Target || ob.Cycle < in.Cycle {
				continue
			}
			switch ob.Kind {
			case Suspected:
				if rec.FirstSuspectCycle < 0 {
					rec.FirstSuspectCycle = ob.Cycle
				}
			case MarkedFailed:
				if rec.MarkedCycle < 0 {
					rec.MarkedCycle = ob.Cycle
				}
			case SuspicionRefuted:
				if rec.RefutedCycle < 0 {
					rec.RefutedCycle = ob.Cycle
				}
			}
		}
		switch in.Type {
		case CrashInjection:
			if rec.MarkedCycle >= 0 {
				rec.DetectionLatency = float64(rec.MarkedCycle - in.Cycle)
			} else {
				rec.Missed = true
			}
		case PacketLossInjection:
			// 扰动目标实际存活，若被最终标记则为误报。
			if rec.MarkedCycle >= 0 {
				rec.FalsePositive = true
			}
		}
		records = append(records, rec)
	}
	return records
}

// Report 是聚合后的诊断报告数据。
type Report struct {
	Records            []EventRecord
	CrashCount         int
	DisturbanceCount   int
	Detected           int
	Missed             int
	MTTD               float64 // 平均检测时间（周期）
	MinLatency         float64
	MaxLatency         float64
	MeanFirstSuspect   float64 // 注入到首次怀疑的平均周期数
	MeanConfirm        float64 // 首次怀疑到最终标记的平均周期数
	FalsePositives     int
	FalsePositiveRate  float64 // 误报事件数 / 扰动事件总数
	TransientSuspicion int     // 被澄清的瞬时怀疑次数
}

// BuildReport 聚合逐事件档案，计算 MTTD、误报率等运行指标。
func BuildReport(records []EventRecord) Report {
	r := Report{Records: records, MinLatency: -1}
	var latencySum, suspectSum, confirmSum float64
	for _, rec := range records {
		switch rec.Injection.Type {
		case CrashInjection:
			r.CrashCount++
			if rec.Missed {
				r.Missed++
				continue
			}
			r.Detected++
			latencySum += rec.DetectionLatency
			if r.MinLatency < 0 || rec.DetectionLatency < r.MinLatency {
				r.MinLatency = rec.DetectionLatency
			}
			if rec.DetectionLatency > r.MaxLatency {
				r.MaxLatency = rec.DetectionLatency
			}
			suspectSum += float64(rec.FirstSuspectCycle - rec.Injection.Cycle)
			confirmSum += float64(rec.MarkedCycle - rec.FirstSuspectCycle)
		case PacketLossInjection:
			r.DisturbanceCount++
			if rec.FalsePositive {
				r.FalsePositives++
			}
			if rec.FirstSuspectCycle >= 0 && rec.RefutedCycle >= 0 {
				r.TransientSuspicion++
			}
		}
	}
	if r.Detected > 0 {
		r.MTTD = latencySum / float64(r.Detected)
		r.MeanFirstSuspect = suspectSum / float64(r.Detected)
		r.MeanConfirm = confirmSum / float64(r.Detected)
	}
	if r.DisturbanceCount > 0 {
		r.FalsePositiveRate = float64(r.FalsePositives) / float64(r.DisturbanceCount)
	}
	return r
}

func (r Report) outcomeOf(rec EventRecord) string {
	if rec.Injection.Type == CrashInjection {
		if rec.Missed {
			return "MISSED"
		}
		return "detected"
	}
	if rec.FalsePositive {
		return "FALSE-POSITIVE"
	}
	if rec.FirstSuspectCycle >= 0 {
		return "transient suspicion, refuted"
	}
	return "absorbed (no suspicion)"
}

func formatCycle(v int) string {
	if v < 0 {
		return "-"
	}
	return fmt.Sprintf("%d", v)
}

func formatLatency(v float64) string {
	if v < 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f", v)
}

// String 输出详尽诊断报告。
func (r Report) String() string {
	var buf bytes.Buffer
	buf.WriteString("=== SWIM Failure Detector Diagnostics ===\n")
	fmt.Fprintf(&buf, "Events: %d total (%d crashes, %d disturbances)\n\n",
		len(r.Records), r.CrashCount, r.DisturbanceCount)

	buf.WriteString("Per-event records:\n")
	fmt.Fprintf(&buf, "%-3s %-11s %-6s %-7s %-13s %-7s %-8s %s\n",
		"ID", "Type", "Target", "Inject", "FirstSuspect", "Marked", "Latency", "Outcome")
	for _, rec := range r.Records {
		fmt.Fprintf(&buf, "%-3d %-11s N%-5d %-7.1f %-13s %-7s %-8s %s\n",
			rec.Injection.ID,
			rec.Injection.Type,
			rec.Injection.Target,
			float64(rec.Injection.Cycle)+rec.Injection.Offset,
			formatCycle(rec.FirstSuspectCycle),
			formatCycle(rec.MarkedCycle),
			formatLatency(rec.DetectionLatency),
			r.outcomeOf(rec),
		)
	}

	buf.WriteString("\nAggregate metrics:\n")
	fmt.Fprintf(&buf, "  MTTD (mean time to detect): %.2f cycles (min %.1f, max %.1f)\n",
		r.MTTD, r.MinLatency, r.MaxLatency)
	fmt.Fprintf(&buf, "  Mean time to first suspicion: %.2f cycles\n", r.MeanFirstSuspect)
	fmt.Fprintf(&buf, "  Mean suspicion-to-confirm:    %.2f cycles\n", r.MeanConfirm)
	fmt.Fprintf(&buf, "  Detection completeness:       %d/%d crashes detected, %d missed\n",
		r.Detected, r.CrashCount, r.Missed)
	fmt.Fprintf(&buf, "  False positives:              %d/%d disturbances (rate %.2f%%)\n",
		r.FalsePositives, r.DisturbanceCount, r.FalsePositiveRate*100)
	fmt.Fprintf(&buf, "  Transient suspicions refuted: %d\n", r.TransientSuspicion)
	return buf.String()
}
