package memsched

import "sync"

// Stats 是内存监控器某一时刻的快照。
type Stats struct {
	Used               int64 // 当前全局已分配字节数
	Peak               int64 // 历史峰值
	Max                int64 // 硬配额上限
	High               int64 // 反压高水位（80%）
	Low                int64 // 恢复低水位（60%，滞回）
	BackpressureEvents int64 // 进入反压状态的次数
	ResumeEvents       int64 // 解除反压、恢复接单的次数
	Admitted           int64 // 累计准入的协程数
}

// Monitor 统计每个 Fiber 执行过程中申请的临时对象大小，
// 并在全局内存吃紧时提供反压与配额阻塞能力。
type Monitor struct {
	mu   sync.Mutex
	cond *sync.Cond

	max  int64
	high int64
	low  int64

	used int64
	peak int64

	blocked bool // 是否处于反压状态

	backpressureEvents int64
	resumeEvents       int64
	admitted           int64
}

// NewMonitor 创建监控器。highRatio/lowRatio 为反压触发/恢复水位（相对 maxBytes）。
func NewMonitor(maxBytes int64, highRatio, lowRatio float64) *Monitor {
	if maxBytes <= 0 {
		panic("memsched: maxBytes must be positive")
	}
	if highRatio <= lowRatio || lowRatio <= 0 || highRatio > 1 {
		panic("memsched: require 0 < lowRatio < highRatio <= 1")
	}
	m := &Monitor{
		max:  maxBytes,
		high: int64(float64(maxBytes) * highRatio),
		low:  int64(float64(maxBytes) * lowRatio),
	}
	m.cond = sync.NewCond(&m.mu)
	return m
}

// Admit 协程准入门。内存用量达到高水位时阻塞（触发反压），
// 直到存量协程释放内存、用量回落到低水位以下才平滑放行（滞回恢复）。
func (m *Monitor) Admit() {
	m.mu.Lock()
	for {
		if !m.blocked && m.used < m.high {
			break // 未反压且未触及高水位，直接放行
		}
		if !m.blocked {
			m.blocked = true
			m.backpressureEvents++
		}
		if m.used <= m.low {
			m.blocked = false
			m.resumeEvents++
			m.cond.Broadcast() // 唤醒其余等待者一同恢复
			break
		}
		m.cond.Wait()
	}
	m.admitted++
	m.mu.Unlock()
}

// Alloc 为 Fiber f 登记 n 字节临时对象。若全局用量将超出硬配额，
// 则阻塞等待其他协程释放，保证 used 永不超过 max。
func (m *Monitor) Alloc(f *Fiber, n int64) {
	m.mu.Lock()
	for m.used+n > m.max {
		m.cond.Wait()
	}
	m.used += n
	if m.used > m.peak {
		m.peak = m.used
	}
	m.mu.Unlock()
	f.used.Add(n)
}

// Release 释放 Fiber f 的 n 字节，并唤醒等待配额/等待恢复的协程。
func (m *Monitor) Release(f *Fiber, n int64) {
	m.mu.Lock()
	m.used -= n
	m.cond.Broadcast()
	m.mu.Unlock()
	f.used.Add(-n)
}

// UnderPressure 报告当前是否处于反压状态。
func (m *Monitor) UnderPressure() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.blocked
}

// Stats 返回监控快照。
func (m *Monitor) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Stats{
		Used:               m.used,
		Peak:               m.peak,
		Max:                m.max,
		High:               m.high,
		Low:                m.low,
		BackpressureEvents: m.backpressureEvents,
		ResumeEvents:       m.resumeEvents,
		Admitted:           m.admitted,
	}
}
