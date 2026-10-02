package lsmwatch

import (
	"fmt"
	"sync"
)

// GetSample 是单次 Get 查询的度量样本。
type GetSample struct {
	Reads          int // 本次实际读取的 SSTable 数量
	FalsePositives int // Bloom 误判次数（说"可能有"但实际没有）
	BloomChecks    int // 本次经过的 Bloom 判定次数
}

// AlertKind 告警类型。
type AlertKind string

const (
	// AlertReadAmplificationSpike 读放大突增：建议加大 Compaction 权重。
	AlertReadAmplificationSpike AlertKind = "READ_AMPLIFICATION_SPIKE"
	// AlertBloomFilterRebuild Bloom 误判率过高：建议重建过滤器。
	AlertBloomFilterRebuild AlertKind = "BLOOM_FILTER_REBUILD"
)

// Alert 是一条诊断告警。
type Alert struct {
	Kind              AlertKind
	ReadAmplification float64
	FalsePositiveRate float64
	Message           string
	Suggestion        string
}

func (a Alert) String() string {
	return fmt.Sprintf("[%s] %s | 读放大=%.2f 误判率=%.2f%% | 建议: %s",
		a.Kind, a.Message, a.ReadAmplification, a.FalsePositiveRate*100, a.Suggestion)
}

// Snapshot 是监控器当前状态的只读快照。
type Snapshot struct {
	Gets                      int64
	ReadAmplification         float64 // 累计读放大
	FalsePositiveRate         float64 // 累计误判率
	WindowReadAmplification   float64 // 滑动窗口读放大（即时指标）
	WindowFalsePositiveRate   float64 // 滑动窗口误判率
	BaselineReadAmplification float64
	Alerts                    []Alert
}

// Monitor 是 LSM 性能度量诊断器：统计读放大与 Bloom 误判率，
// 并在指标异常时动态给出 Compaction / 重建 Filter 的建议。
type Monitor struct {
	mu sync.Mutex

	gets, reads, fps, checks int64
	window                   []GetSample
	windowCap                int

	baselineRA float64

	RAThreshold float64 // 读放大绝对阈值，默认 3.0
	SpikeFactor float64 // 相对基线的突增倍数，默认 2.0
	FPThreshold float64 // 误判率阈值，默认 0.10

	alerts        []Alert
	raAlertActive bool
	fpAlertActive bool

	// OnAlert 告警回调（可选），在锁外触发。
	OnAlert func(Alert)
}

// NewMonitor 创建监控器，windowCap 为滑动窗口容量（样本数）。
func NewMonitor(windowCap int) *Monitor {
	if windowCap < 1 {
		windowCap = 1
	}
	return &Monitor{
		windowCap:   windowCap,
		RAThreshold: 3.0,
		SpikeFactor: 2.0,
		FPThreshold: 0.10,
	}
}

// RecordGet 记录一次 Get 样本，并即时评估是否触发告警。
func (m *Monitor) RecordGet(s GetSample) {
	m.mu.Lock()
	m.gets++
	m.reads += int64(s.Reads)
	m.fps += int64(s.FalsePositives)
	m.checks += int64(s.BloomChecks)
	m.window = append(m.window, s)
	if len(m.window) > m.windowCap {
		m.window = m.window[len(m.window)-m.windowCap:]
	}

	ra := m.windowRALocked()
	fpRate, windowChecks := m.windowFPLocked()

	var fired []Alert

	// 读放大突增检测：超过绝对阈值且相对基线翻倍。
	if m.baselineRA > 0 && ra >= m.RAThreshold && ra >= m.SpikeFactor*m.baselineRA {
		if !m.raAlertActive {
			m.raAlertActive = true
			fired = append(fired, Alert{
				Kind:              AlertReadAmplificationSpike,
				ReadAmplification: ra,
				FalsePositiveRate: fpRate,
				Message:           fmt.Sprintf("读放大从基线 %.2f 跃升至 %.2f", m.baselineRA, ra),
				Suggestion:        "增加 Compaction 并发/权重，加速 SSTable 合并以压制读延迟",
			})
		}
	} else if ra < m.RAThreshold*0.8 {
		m.raAlertActive = false // 恢复后解除，允许下次再告警
	}

	// Bloom 误判率检测（要求窗口内有足够的判定样本）。
	if windowChecks >= 50 && fpRate >= m.FPThreshold {
		if !m.fpAlertActive {
			m.fpAlertActive = true
			fired = append(fired, Alert{
				Kind:              AlertBloomFilterRebuild,
				ReadAmplification: ra,
				FalsePositiveRate: fpRate,
				Message:           fmt.Sprintf("Bloom Filter 误判率 %.2f%% 超过阈值 %.2f%%", fpRate*100, m.FPThreshold*100),
				Suggestion:        "重建 Bloom Filter（提高 bits/key）以降低误判率",
			})
		}
	} else if fpRate < m.FPThreshold*0.5 {
		m.fpAlertActive = false
	}

	m.alerts = append(m.alerts, fired...)
	cb := m.OnAlert
	m.mu.Unlock()

	if cb != nil {
		for _, a := range fired {
			cb(a)
		}
	}
}

// Calibrate 将当前累计读放大标定为健康基线。
func (m *Monitor) Calibrate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gets > 0 {
		m.baselineRA = float64(m.reads) / float64(m.gets)
	}
}

// Snapshot 返回当前状态快照。
func (m *Monitor) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	snap := Snapshot{
		Gets:                      m.gets,
		BaselineReadAmplification: m.baselineRA,
		Alerts:                    append([]Alert(nil), m.alerts...),
	}
	if m.gets > 0 {
		snap.ReadAmplification = float64(m.reads) / float64(m.gets)
	}
	if m.checks > 0 {
		snap.FalsePositiveRate = float64(m.fps) / float64(m.checks)
	}
	snap.WindowReadAmplification = m.windowRALocked()
	snap.WindowFalsePositiveRate, _ = m.windowFPLocked()
	return snap
}

func (m *Monitor) windowRALocked() float64 {
	if len(m.window) == 0 {
		return 0
	}
	var reads int
	for _, s := range m.window {
		reads += s.Reads
	}
	return float64(reads) / float64(len(m.window))
}

func (m *Monitor) windowFPLocked() (rate float64, checks int) {
	var fps int
	for _, s := range m.window {
		fps += s.FalsePositives
		checks += s.BloomChecks
	}
	if checks > 0 {
		rate = float64(fps) / float64(checks)
	}
	return rate, checks
}
