package cachebench

import (
	"encoding/json"
	"math"
	"sort"
	"time"
)

// LatencyReport 是延迟分布统计（单位微秒）。
type LatencyReport struct {
	Count  uint64  `json:"count"`
	MinUs  int64   `json:"min_us"`
	P50Us  int64   `json:"p50_us"`
	P90Us  int64   `json:"p90_us"`
	P95Us  int64   `json:"p95_us"`
	P99Us  int64   `json:"p99_us"`
	P999Us int64   `json:"p999_us"`
	MaxUs  int64   `json:"max_us"`
	MeanUs float64 `json:"mean_us"`
}

func latencyReport(h *Histogram) LatencyReport {
	return LatencyReport{
		Count:  h.Count(),
		MinUs:  h.MinUs(),
		P50Us:  h.Percentile(50),
		P90Us:  h.Percentile(90),
		P95Us:  h.Percentile(95),
		P99Us:  h.Percentile(99),
		P999Us: h.Percentile(99.9),
		MaxUs:  h.MaxUs(),
		MeanUs: h.MeanUs(),
	}
}

// TotalsReport 是核心计数指标汇总。
type TotalsReport struct {
	Gets      uint64  `json:"gets"`
	Hits      uint64  `json:"hits"`
	Misses    uint64  `json:"misses"`
	Sets      uint64  `json:"sets"`
	Evictions uint64  `json:"evictions"`
	HitRate   float64 `json:"hit_rate"`
}

// MemoryReport 是内存占用与碎片率评估。
type MemoryReport struct {
	LiveEntries             int64   `json:"live_entries"`
	LogicalBytes            int64   `json:"logical_bytes"`
	EstimatedFootprintBytes int64   `json:"estimated_footprint_bytes"`
	CacheFragmentationRate  float64 `json:"cache_fragmentation_rate"`
	GoHeapAllocBytes        uint64  `json:"go_heap_alloc_bytes"`
	GoHeapInuseBytes        uint64  `json:"go_heap_inuse_bytes"`
	GoHeapFragmentationRate float64 `json:"go_heap_fragmentation_rate"`
}

// WindowStat 是单个时间窗口的统计。
type WindowStat struct {
	Index     int     `json:"index"`
	StartMs   float64 `json:"start_ms"`
	EndMs     float64 `json:"end_ms"`
	Hits      uint64  `json:"hits"`
	Misses    uint64  `json:"misses"`
	Sets      uint64  `json:"sets"`
	Evictions uint64  `json:"evictions"`
	HitRate   float64 `json:"hit_rate"`
}

// InflectionPoint 描述命中率拐点：相邻窗口间命中率的最大跌幅。
type InflectionPoint struct {
	WindowIndex   int     `json:"window_index"`
	StartMs       float64 `json:"start_ms"`
	EndMs         float64 `json:"end_ms"`
	HitRateBefore float64 `json:"hit_rate_before"`
	HitRateAfter  float64 `json:"hit_rate_after"`
	Drop          float64 `json:"drop"`
}

// EvictionSpike 描述一段驱逐数显著高于均值的连续区间。
type EvictionSpike struct {
	StartMs   float64 `json:"start_ms"`
	EndMs     float64 `json:"end_ms"`
	Windows   int     `json:"windows"`
	Evictions uint64  `json:"evictions"`
	Threshold float64 `json:"threshold_per_window"`
}

// AnalysisReport 是拐点与突变区间分析结果。
type AnalysisReport struct {
	HitRateInflection        *InflectionPoint `json:"hit_rate_inflection,omitempty"`
	EvictionSpikes           []EvictionSpike  `json:"eviction_spike_intervals"`
	EvictionsPerWindowMean   float64          `json:"evictions_per_window_mean"`
	EvictionsPerWindowStddev float64          `json:"evictions_per_window_stddev"`
	EvictionsPerWindowMedian float64          `json:"evictions_per_window_median"`
}

// HealthReport 是缓存健康度评估。
type HealthReport struct {
	Score  float64  `json:"score"`
	Rating string   `json:"rating"`
	Issues []string `json:"issues"`
}

// Report 是完整的缓存健康度评估报告。
type Report struct {
	GeneratedAt  time.Time      `json:"generated_at"`
	WindowSizeMs float64        `json:"window_size_ms"`
	Totals       TotalsReport   `json:"totals"`
	GetLatency   LatencyReport  `json:"get_latency"`
	SetLatency   LatencyReport  `json:"set_latency"`
	Memory       MemoryReport   `json:"memory"`
	Health       HealthReport   `json:"health"`
	Analysis     AnalysisReport `json:"analysis"`
	Windows      []WindowStat   `json:"windows"`
}

// JSON 以缩进 JSON 格式输出报告。
func (r *Report) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// minWindowOps 是窗口参与拐点分析所需的最小操作数。
const minWindowOps = 10

// BuildReport 汇总度量与内存统计，执行拐点/突变分析并评估健康度。
func BuildReport(m *Metrics, mem MemoryReport) *Report {
	windowMs := float64(m.Windows.WindowDuration()) / float64(time.Millisecond)
	snap := m.Windows.Snapshot()

	windows := make([]WindowStat, 0, len(snap))
	for i, w := range snap {
		total := w.Hits + w.Misses + w.Sets + w.Evictions
		if total == 0 {
			continue
		}
		var hitRate float64
		if gets := w.Hits + w.Misses; gets > 0 {
			hitRate = float64(w.Hits) / float64(gets)
		}
		windows = append(windows, WindowStat{
			Index:     i,
			StartMs:   float64(i) * windowMs,
			EndMs:     float64(i+1) * windowMs,
			Hits:      w.Hits,
			Misses:    w.Misses,
			Sets:      w.Sets,
			Evictions: w.Evictions,
			HitRate:   hitRate,
		})
	}

	analysis := analyze(windows, windowMs)

	totals := TotalsReport{
		Gets:      m.Gets(),
		Hits:      m.Hits(),
		Misses:    m.Misses(),
		Sets:      m.Sets(),
		Evictions: m.Evictions(),
		HitRate:   m.HitRate(),
	}
	getLat := latencyReport(m.GetLatency)

	report := &Report{
		GeneratedAt:  time.Now(),
		WindowSizeMs: windowMs,
		Totals:       totals,
		GetLatency:   getLat,
		SetLatency:   latencyReport(m.SetLatency),
		Memory:       mem,
		Analysis:     analysis,
		Windows:      windows,
	}
	report.Health = evaluateHealth(totals, getLat, mem, analysis)
	return report
}

// analyze 定位命中率拐点与驱逐突变区间。
func analyze(windows []WindowStat, windowMs float64) AnalysisReport {
	analysis := AnalysisReport{EvictionSpikes: []EvictionSpike{}}

	// 命中率拐点：相邻有效窗口间命中率的最大跌幅。
	bestDrop := 0.0
	bestIdx := -1
	prev := -1
	for i, w := range windows {
		if w.Hits+w.Misses < minWindowOps {
			continue
		}
		if prev >= 0 {
			drop := windows[prev].HitRate - w.HitRate
			if drop > bestDrop {
				bestDrop = drop
				bestIdx = i
			}
		}
		prev = i
	}
	if bestIdx >= 0 && bestDrop > 0.05 {
		analysis.HitRateInflection = &InflectionPoint{
			WindowIndex:   windows[bestIdx].Index,
			StartMs:       windows[bestIdx].StartMs,
			EndMs:         windows[bestIdx].EndMs,
			HitRateBefore: windows[prevIndexBefore(windows, bestIdx)].HitRate,
			HitRateAfter:  windows[bestIdx].HitRate,
			Drop:          bestDrop,
		}
	}

	// 驱逐突变区间：窗口驱逐数显著高于中位数基线的连续区段。
	// 采用中位数而非 mean+2σ：当驱逐洪峰占据较大样本比例时，
	// 均值与方差会被峰值自身拉高，中位数对这类双峰分布更稳健。
	if len(windows) > 0 {
		var sum float64
		for _, w := range windows {
			sum += float64(w.Evictions)
		}
		mean := sum / float64(len(windows))
		var sq float64
		for _, w := range windows {
			d := float64(w.Evictions) - mean
			sq += d * d
		}
		stddev := math.Sqrt(sq / float64(len(windows)))
		analysis.EvictionsPerWindowMean = mean
		analysis.EvictionsPerWindowStddev = stddev

		sorted := make([]uint64, len(windows))
		for i, w := range windows {
			sorted[i] = w.Evictions
		}
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		median := float64(sorted[len(sorted)/2])
		analysis.EvictionsPerWindowMedian = median

		// 阈值：3 倍中位数基线且高于全局均值，至少为 1。
		threshold := 3 * median
		if threshold < 1 {
			threshold = 1
		}
		for i := 0; i < len(windows); {
			if float64(windows[i].Evictions) < threshold ||
				float64(windows[i].Evictions) <= mean {
				i++
				continue
			}
			j := i
			var evictions uint64
			for j < len(windows) && float64(windows[j].Evictions) >= threshold &&
				float64(windows[j].Evictions) > mean {
				evictions += windows[j].Evictions
				j++
			}
			analysis.EvictionSpikes = append(analysis.EvictionSpikes, EvictionSpike{
				StartMs:   windows[i].StartMs,
				EndMs:     windows[j-1].EndMs,
				Windows:   j - i,
				Evictions: evictions,
				Threshold: threshold,
			})
			i = j
		}
	}
	return analysis
}

// prevIndexBefore 返回 windows 中 bestIdx 之前最近一个有效窗口。
// 调用方保证 bestIdx > 0 且存在前置有效窗口。
func prevIndexBefore(windows []WindowStat, bestIdx int) int {
	prev := 0
	for i := 0; i < bestIdx; i++ {
		if windows[i].Hits+windows[i].Misses >= minWindowOps {
			prev = i
		}
	}
	return prev
}

// evaluateHealth 依据命中率、P99 延迟与碎片率评估健康度（0-100）。
func evaluateHealth(t TotalsReport, getLat LatencyReport, mem MemoryReport, a AnalysisReport) HealthReport {
	latScore := 1.0
	if getLat.P99Us > 100 {
		// 100us 得满分，10ms 得 0 分，按对数刻度线性衰减。
		latScore = 1 - (math.Log10(float64(getLat.P99Us))-2)/2
		if latScore < 0 {
			latScore = 0
		}
	}
	fragScore := 1 - mem.CacheFragmentationRate
	if fragScore < 0 {
		fragScore = 0
	}
	score := 100 * (0.45*t.HitRate + 0.30*latScore + 0.25*fragScore)

	rating := "较差"
	switch {
	case score >= 90:
		rating = "优秀"
	case score >= 75:
		rating = "良好"
	case score >= 50:
		rating = "退化"
	}

	issues := []string{}
	if t.HitRate < 0.8 {
		issues = append(issues, "缓存命中率偏低，建议检查容量配置或访问模式")
	}
	if getLat.P99Us > 1000 {
		issues = append(issues, "P99 访问延迟超过 1ms，存在长尾延迟")
	}
	if mem.CacheFragmentationRate > 0.5 {
		issues = append(issues, "缓存内存碎片率偏高，条目元数据开销占比过大")
	}
	if a.HitRateInflection != nil {
		issues = append(issues, "命中率出现拐点，存在访问模式突变")
	}
	if len(a.EvictionSpikes) > 0 {
		issues = append(issues, "检测到内存驱逐突变区间，容量可能不足")
	}

	return HealthReport{Score: score, Rating: rating, Issues: issues}
}
