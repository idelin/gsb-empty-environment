package lsmguard

import (
	"fmt"
	"sync"
)

// 诊断阈值
const (
	// ReadAmpWarnThreshold 读放大告警阈值：窗口内平均每次 Get 读取的 SSTable 数
	ReadAmpWarnThreshold = 2.0
	// FPRateWarnThreshold Bloom Filter 误判率告警阈值
	FPRateWarnThreshold = 0.05
)

// Alert 是监控器输出的诊断建议。
type Alert struct {
	Level   string // WARN / CRITICAL
	Metric  string // read_amplification / bloom_fp_rate
	Value   float64
	Message string
	Advice  string
}

func (a Alert) String() string {
	return fmt.Sprintf("[%s] %s=%.2f | %s | 建议: %s",
		a.Level, a.Metric, a.Value, a.Message, a.Advice)
}

// Metrics 是一个窗口期的聚合指标快照。
type Metrics struct {
	Samples           int
	ReadAmplification float64 // 平均每次 Get 实际读取的 SSTable 数
	FalsePositiveRate float64 // Bloom 阳性中误判的比例
}

// Monitor 以滑动窗口聚合 Get 样本，实时计算读放大与误判率并输出告警。
type Monitor struct {
	mu      sync.Mutex
	window  int
	samples []GetStats
	onAlert func(Alert)
	// 冷却：同类告警不重复轰炸，指标恢复后自动解除
	alerted map[string]bool
}

func NewMonitor(window int) *Monitor {
	if window <= 0 {
		window = 100
	}
	return &Monitor{window: window, alerted: make(map[string]bool)}
}

// SetOnAlert 注册告警回调。
func (m *Monitor) SetOnAlert(fn func(Alert)) { m.onAlert = fn }

// Observe 接收一次 Get 样本，并做即时诊断。
func (m *Monitor) Observe(s GetStats) {
	m.mu.Lock()
	m.samples = append(m.samples, s)
	if len(m.samples) > m.window {
		m.samples = m.samples[1:]
	}
	metrics := m.lockedMetrics()
	alertCb := m.onAlert
	var alerts []Alert

	if metrics.Samples >= m.window/2 { // 至少半个窗口再下结论，避免冷启动抖动
		if metrics.ReadAmplification > ReadAmpWarnThreshold && !m.alerted["read_amplification"] {
			level := "WARN"
			if metrics.ReadAmplification > 2*ReadAmpWarnThreshold {
				level = "CRITICAL"
			}
			alerts = append(alerts, Alert{
				Level:   level,
				Metric:  "read_amplification",
				Value:   metrics.ReadAmplification,
				Message: fmt.Sprintf("读放大跃升至 %.2f（阈值 %.1f），Get 延迟受多层 SSTable 磁盘读取压制", metrics.ReadAmplification, ReadAmpWarnThreshold),
				Advice:  "增加 Compaction 并发/权重，加速层级合并以压制读延迟；检查是否有 SSTable 的 Bloom Filter 被关闭",
			})
			m.alerted["read_amplification"] = true
		} else if metrics.ReadAmplification <= ReadAmpWarnThreshold {
			m.alerted["read_amplification"] = false
		}
		if metrics.FalsePositiveRate > FPRateWarnThreshold && !m.alerted["bloom_fp_rate"] {
			alerts = append(alerts, Alert{
				Level:   "WARN",
				Metric:  "bloom_fp_rate",
				Value:   metrics.FalsePositiveRate,
				Message: fmt.Sprintf("Bloom Filter 误判率 %.2f%% 超过阈值 %.2f%%", metrics.FalsePositiveRate*100, FPRateWarnThreshold*100),
				Advice:  "重建 Bloom Filter（增大位数组/哈希函数数），或检查键分布是否变化导致过滤器退化",
			})
			m.alerted["bloom_fp_rate"] = true
		} else if metrics.FalsePositiveRate <= FPRateWarnThreshold {
			m.alerted["bloom_fp_rate"] = false
		}
	}
	m.mu.Unlock()

	if alertCb != nil {
		for _, a := range alerts {
			alertCb(a)
		}
	}
}

// Metrics 返回当前窗口的聚合指标。
func (m *Monitor) Metrics() Metrics {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lockedMetrics()
}

func (m *Monitor) lockedMetrics() Metrics {
	if len(m.samples) == 0 {
		return Metrics{}
	}
	var reads, bloomPos, fp int
	for _, s := range m.samples {
		reads += s.SSTablesRead
		bloomPos += s.BloomPositives
		fp += s.FalsePositives
	}
	var fpRate float64
	if bloomPos > 0 {
		fpRate = float64(fp) / float64(bloomPos)
	}
	return Metrics{
		Samples:           len(m.samples),
		ReadAmplification: float64(reads) / float64(len(m.samples)),
		FalsePositiveRate: fpRate,
	}
}

// ResetAlertCooldown 重置告警冷却（测试用）。
func (m *Monitor) ResetAlertCooldown() {
	m.mu.Lock()
	m.alerted = make(map[string]bool)
	m.mu.Unlock()
}
