package main

import (
	"encoding/json"
	"fmt"
)

// HealthReport 缓存健康度评估报告（JSON 输出）。
type HealthReport struct {
	Hits                uint64  `json:"hits"`
	Misses              uint64  `json:"misses"`
	Sets                uint64  `json:"sets"`
	Evictions           uint64  `json:"evictions"`
	TotalOps            uint64  `json:"total_ops"`
	HitRate             float64 `json:"hit_rate"`
	LatencyP50Micros    int64   `json:"latency_p50_us"`
	LatencyP90Micros    int64   `json:"latency_p90_us"`
	LatencyP99Micros    int64   `json:"latency_p99_us"`
	LatencyP999Micros   int64   `json:"latency_p999_us"`
	LatencyMaxMicros    int64   `json:"latency_max_us"`
	LatencyMeanMicros   float64 `json:"latency_mean_us"`
	LatencySamples      uint64  `json:"latency_samples"`
	UsedBytes           int64   `json:"used_bytes"`
	AllocatedBytes      int64   `json:"allocated_bytes"`
	CapacityBytes       int64   `json:"capacity_bytes"`
	MemoryFragmentation float64 `json:"memory_fragmentation"`
	Entries             int     `json:"entries"`
	Health              string  `json:"health"`
	HealthDetail        string  `json:"health_detail"`
}

// BuildReport 汇总 Metrics 与 Cache 状态，生成健康度评估报告。
func BuildReport(c *Cache, m *Metrics) *HealthReport {
	sorted := m.Latency.Snapshot()
	var sum int64
	for _, v := range sorted {
		sum += v
	}
	var mean float64
	if len(sorted) > 0 {
		mean = float64(sum) / float64(len(sorted))
	}
	used, allocated, capacity := c.MemStats()
	hits, misses := m.Hits.Load(), m.Misses.Load()
	rep := &HealthReport{
		Hits:                hits,
		Misses:              misses,
		Sets:                m.Sets.Load(),
		Evictions:           m.Evictions.Load(),
		TotalOps:            hits + misses + m.Sets.Load(),
		HitRate:             m.HitRate(),
		LatencyP50Micros:    Percentile(sorted, 50),
		LatencyP90Micros:    Percentile(sorted, 90),
		LatencyP99Micros:    Percentile(sorted, 99),
		LatencyP999Micros:   Percentile(sorted, 99.9),
		LatencyMaxMicros:    Percentile(sorted, 100),
		LatencyMeanMicros:   mean,
		LatencySamples:      m.Latency.Count(),
		UsedBytes:           used,
		AllocatedBytes:      allocated,
		CapacityBytes:       capacity,
		MemoryFragmentation: c.Fragmentation(),
		Entries:             c.Len(),
	}
	rep.Health, rep.HealthDetail = assess(rep)
	return rep
}

// assess 依据命中率、P99 延迟与碎片率给出健康度评级。
func assess(r *HealthReport) (string, string) {
	score := 0
	var reasons []string
	if r.HitRate >= 0.9 {
		score += 2
	} else if r.HitRate >= 0.7 {
		score += 1
		reasons = append(reasons, fmt.Sprintf("命中率 %.2f%% 偏低", r.HitRate*100))
	} else {
		reasons = append(reasons, fmt.Sprintf("命中率 %.2f%% 过低", r.HitRate*100))
	}
	if r.LatencyP99Micros <= 100 {
		score += 2
	} else if r.LatencyP99Micros <= 1000 {
		score += 1
		reasons = append(reasons, fmt.Sprintf("P99 延迟 %dus 偏高", r.LatencyP99Micros))
	} else {
		reasons = append(reasons, fmt.Sprintf("P99 延迟 %dus 过高", r.LatencyP99Micros))
	}
	if r.MemoryFragmentation <= 0.2 {
		score += 2
	} else if r.MemoryFragmentation <= 0.5 {
		score += 1
		reasons = append(reasons, fmt.Sprintf("内存碎片率 %.2f%% 偏高", r.MemoryFragmentation*100))
	} else {
		reasons = append(reasons, fmt.Sprintf("内存碎片率 %.2f%% 过高", r.MemoryFragmentation*100))
	}
	detail := "各项指标正常"
	if len(reasons) > 0 {
		detail = ""
		for i, s := range reasons {
			if i > 0 {
				detail += "；"
			}
			detail += s
		}
	}
	switch {
	case score >= 5:
		return "HEALTHY", detail
	case score >= 3:
		return "DEGRADED", detail
	default:
		return "CRITICAL", detail
	}
}

// JSON 返回报告的 JSON 序列化结果。
func (r *HealthReport) JSON() (string, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
