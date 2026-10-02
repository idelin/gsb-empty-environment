// Package checkpoint 提供并发消费场景下的位点（Checkpoint）管理与崩溃安全恢复。
//
// 多个工作线程并行消费一段连续位点区间内的变更事件，每个线程消费完
// 一个位点后调用 Ack。Manager 维护低水位（Low Watermark）：一个位点
// 位置，满足"所有小于它的位点均已被完整消费"。低水位即崩溃重启后的
// 安全重放起始点。水位每次推进都会原子落盘（tmp 写入 + fsync +
// rename + 目录 fsync），保证崩溃后可恢复。
package checkpoint

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Manager 追踪已被所有线程完整消费的最小连续位点。
type Manager struct {
	mu        sync.Mutex
	watermark uint64              // 低水位：小于它的位点已全部消费完成
	pending   map[uint64]struct{} // 已 Ack 但尚未连续（水位之后）的位点
	path      string              // 持久化文件路径；空字符串表示仅内存
}

// Open 打开（或创建）一个位点管理器。若持久化文件存在，则从中恢复
// 低水位（崩溃续跑）；否则从 start 开始追踪。
func Open(path string, start uint64) (*Manager, error) {
	watermark := start
	if path != "" {
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			v, perr := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
			if perr != nil {
				return nil, fmt.Errorf("checkpoint: 持久化文件 %s 内容损坏: %w", path, perr)
			}
			watermark = v
		case !os.IsNotExist(err):
			return nil, fmt.Errorf("checkpoint: 读取 %s 失败: %w", path, err)
		}
	}
	return &Manager{
		watermark: watermark,
		pending:   make(map[uint64]struct{}),
		path:      path,
	}, nil
}

// LowWatermark 返回当前低水位，即安全重放起始点。
// 语义：所有 < LowWatermark 的位点均已被完整消费，重放从该位点开始
// 不会漏掉任何未完成事件（水位之后的事件可能被重复投递，属 at-least-once）。
func (m *Manager) LowWatermark() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.watermark
}

// Ack 报告 pos 位点已被某工作线程完整消费。幂等：重复 Ack 或对
// 已低于水位的位点 Ack 均为无操作。水位推进时同步持久化。
func (m *Manager) Ack(pos uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if pos < m.watermark {
		return nil // 已被水位覆盖，忽略
	}
	m.pending[pos] = struct{}{}

	advanced := false
	for {
		if _, ok := m.pending[m.watermark]; !ok {
			break
		}
		delete(m.pending, m.watermark)
		m.watermark++
		advanced = true
	}
	if advanced && m.path != "" {
		if err := persist(m.path, m.watermark); err != nil {
			return fmt.Errorf("checkpoint: 持久化水位 %d 失败: %w", m.watermark, err)
		}
	}
	return nil
}

// ReadCheckpoint 是独立的进度读取器：不构造 Manager，直接从持久化
// 文件读取安全重放起始点。文件不存在时返回 fallback。
func ReadCheckpoint(path string, fallback uint64) (uint64, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return fallback, nil
	}
	if err != nil {
		return 0, fmt.Errorf("checkpoint: 读取 %s 失败: %w", path, err)
	}
	v, perr := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if perr != nil {
		return 0, fmt.Errorf("checkpoint: 持久化文件 %s 内容损坏: %w", path, perr)
	}
	return v, nil
}

// persist 崩溃安全地写入水位：写临时文件 -> fsync -> 原子 rename ->
// fsync 目录。崩溃任一时刻，目标文件要么是旧水位要么是新水位，
// 绝不出现半写状态。
func persist(path string, watermark uint64) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".checkpoint-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // rename 成功后为无操作

	if _, err := tmp.WriteString(strconv.FormatUint(watermark, 10) + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}

	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
