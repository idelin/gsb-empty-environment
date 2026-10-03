// Package checkpoint 实现断点续传位点管理与崩溃安全恢复。
//
// 消费端以多线程并发处理一段连续位点（position）上的变更事件。
// Manager 追踪每个位点的消费状态，维护 Low Watermark：
// 所有小于该位点的事件均已被完整消费的最小连续位点。
// 每次状态变化都以“写临时文件 + fsync + rename + fsync 目录”的方式
// 原子落盘；崩溃重启后通过 LoadResumePoint 读取安全重放起始点，
// 保证不漏掉任何未完成事件（at-least-once）。
package checkpoint

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// state 是持久化到磁盘的检查点状态。
type state struct {
	// LowWatermark 是安全重放起始点：小于它的位点均已被完整消费。
	LowWatermark uint64 `json:"low_watermark"`
	// InFlight 是已领取但尚未确认消费完成的位点（崩溃后必须重放）。
	InFlight []uint64 `json:"in_flight,omitempty"`
	// Completed 是已完成但尚未被 LowWatermark 覆盖的乱序完成位点。
	Completed []uint64 `json:"completed,omitempty"`
}

// Manager 管理消费进度检查点，可被多个工作线程并发使用。
type Manager struct {
	mu        sync.Mutex
	path      string
	next      uint64          // 即 LowWatermark：第一个未被完整消费的位点
	inFlight  map[uint64]bool // 已领取未完成的位点
	completed map[uint64]bool // 已完成但 >= next 的乱序位点
}

// Open 打开（或创建）位于 path 的检查点管理器。
// 若 path 不存在，以 start 作为初始 LowWatermark 创建；
// 若已存在，则从磁盘恢复崩溃前的状态。
func Open(path string, start uint64) (*Manager, error) {
	m := &Manager{
		path:      path,
		next:      start,
		inFlight:  make(map[uint64]bool),
		completed: make(map[uint64]bool),
	}
	data, err := ioutil.ReadFile(path)
	if os.IsNotExist(err) {
		// 首次启动：落盘初始状态，保证崩溃后也能读到起点。
		if err := m.persistLocked(); err != nil {
			return nil, err
		}
		return m, nil
	}
	if err != nil {
		return nil, fmt.Errorf("checkpoint: read %s: %v", path, err)
	}
	var s state
	if err := json.Unmarshal(data, &s); err != nil {
		// 检查点损坏时宁可报错，也绝不静默回退导致位点错乱。
		return nil, fmt.Errorf("checkpoint: corrupt file %s: %v", path, err)
	}
	m.next = s.LowWatermark
	for _, p := range s.InFlight {
		m.inFlight[p] = true
	}
	for _, p := range s.Completed {
		m.completed[p] = true
	}
	return m, nil
}

// Begin 领取一个位点开始处理。若该位点早于 LowWatermark
// （崩溃前已被完整消费），返回 false，调用方应跳过它。
func (m *Manager) Begin(pos uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if pos < m.next {
		return false
	}
	m.inFlight[pos] = true
	return true
}

// Commit 确认一个位点已被完整消费，并尽可能推进 LowWatermark，
// 随后将最新状态原子落盘。
func (m *Manager) Commit(pos uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.inFlight, pos)
	if pos >= m.next {
		m.completed[pos] = true
	}
	// 推进 LowWatermark：越过所有已连续完成的位点。
	for m.completed[m.next] {
		delete(m.completed, m.next)
		m.next++
	}
	return m.persistLocked()
}

// LowWatermark 返回当前安全重放起始点。
func (m *Manager) LowWatermark() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.next
}

// persistLocked 以崩溃安全的方式持久化状态：
// 先写临时文件并 fsync，再原子 rename，最后 fsync 父目录。
// 调用者必须持有 m.mu。
func (m *Manager) persistLocked() error {
	s := state{LowWatermark: m.next}
	for p := range m.inFlight {
		s.InFlight = append(s.InFlight, p)
	}
	for p := range m.completed {
		s.Completed = append(s.Completed, p)
	}
	sort.Slice(s.InFlight, func(i, j int) bool { return s.InFlight[i] < s.InFlight[j] })
	sort.Slice(s.Completed, func(i, j int) bool { return s.Completed[i] < s.Completed[j] })

	data, err := json.Marshal(&s)
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("checkpoint: write tmp: %v", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("checkpoint: write tmp: %v", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("checkpoint: fsync tmp: %v", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("checkpoint: close tmp: %v", err)
	}
	if err := os.Rename(tmp, m.path); err != nil {
		return fmt.Errorf("checkpoint: rename: %v", err)
	}
	dir, err := os.Open(filepath.Dir(m.path))
	if err != nil {
		return fmt.Errorf("checkpoint: open dir: %v", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("checkpoint: fsync dir: %v", err)
	}
	return nil
}

// LoadResumePoint 是崩溃重启后使用的进度读取器：
// 只读取磁盘上的检查点，返回安全重放起始点。
// 从该位点（含）开始重放即可保证不漏掉任何未完成事件。
func LoadResumePoint(path string) (uint64, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("checkpoint: read %s: %v", path, err)
	}
	var s state
	if err := json.Unmarshal(data, &s); err != nil {
		return 0, fmt.Errorf("checkpoint: corrupt file %s: %v", path, err)
	}
	return s.LowWatermark, nil
}
