// Package pubstore 实现双存储槽的配置发布库。
//
// 单次写入可能撕裂，因此每个槽位由代际、长度、摘要和提交标记共同保护：
// 先写未提交的完整镜像并落盘，最后才写提交标记。读取时两个槽各自独立
// 验证，只在接受完整提交的槽中挑选代际最高者；坏槽（包括代际更高的坏槽）
// 一律被忽略，发布失败不影响旧配置可读。
//
// 代际溢出规则：代际为单调递增的 uint64，当最新有效代际已达
// math.MaxUint64 时，Publish 明确返回 ErrGenerationOverflow 拒绝发布，
// 绝不回绕（回绕会让旧槽被误判为新值）。
package pubstore

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

var (
	// ErrNoConfig 表示两个槽都不存在有效提交。
	ErrNoConfig = errors.New("pubstore: no committed config")
	// ErrGenerationOverflow 表示代际已达 uint64 上限，发布被明确拒绝。
	ErrGenerationOverflow = errors.New("pubstore: generation overflow, refusing to publish")
	// errSimulatedCrash 是测试注入的“提交标记写入前崩溃”故障。
	errSimulatedCrash = errors.New("pubstore: simulated crash before commit marker")
)

// Config 是读取到的一份已提交配置。
type Config struct {
	Generation uint64
	Data       []byte
}

// Store 是双槽配置存储，两个槽位文件交替承载相邻代际。
type Store struct {
	dir   string
	slots [2]string

	// crashBeforeCommit 仅供测试注入：在提交标记写入前模拟崩溃。
	crashBeforeCommit bool
}

// Open 打开（必要时创建）dir 下的双槽存储。
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{
		dir:   dir,
		slots: [2]string{filepath.Join(dir, "slot0"), filepath.Join(dir, "slot1")},
	}, nil
}

// Load 返回两个槽中通过独立验证且代际最高的配置。
// 无法通过验证的槽（撕裂、截断、摘要不符、未提交）被忽略，
// 因此高代际坏槽绝不会被当成新值。
func (s *Store) Load() (*Config, error) {
	var best *Slot
	for _, path := range s.slots {
		slot, err := readSlot(path)
		if err != nil {
			continue
		}
		if best == nil || slot.Generation > best.Generation {
			best = slot
		}
	}
	if best == nil {
		return nil, ErrNoConfig
	}
	return &Config{Generation: best.Generation, Data: append([]byte(nil), best.Payload...)}, nil
}

// Publish 以“最新有效代际 + 1”把 data 写入另一个槽。
// 目标槽在写入期间可能处于撕裂状态，但旧配置所在槽不被触碰，
// 因此任何发布失败都仍能读出旧配置。
func (s *Store) Publish(data []byte) (*Config, error) {
	if len(data) > MaxPayload {
		return nil, fmt.Errorf("pubstore: payload %d bytes exceeds max %d", len(data), MaxPayload)
	}
	var latestGen uint64
	latestSlot := -1
	for i, path := range s.slots {
		slot, err := readSlot(path)
		if err != nil {
			continue
		}
		if latestSlot < 0 || slot.Generation > latestGen {
			latestGen, latestSlot = slot.Generation, i
		}
	}
	if latestSlot >= 0 && latestGen == math.MaxUint64 {
		return nil, ErrGenerationOverflow
	}
	newGen := uint64(1)
	if latestSlot >= 0 {
		newGen = latestGen + 1
	}
	target := 0
	if latestSlot == 0 {
		target = 1
	}
	if err := s.writeSlot(s.slots[target], newGen, data); err != nil {
		return nil, err
	}
	syncDir(s.dir)
	return &Config{Generation: newGen, Data: append([]byte(nil), data...)}, nil
}

// readSlot 读取并独立验证一个槽位文件；不存在或验证失败都返回错误。
func readSlot(path string) (*Slot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ValidateSlot(data)
}

// writeSlot 执行两阶段写入：先写未提交的完整镜像并 fsync，
// 最后写入提交标记并再次 fsync。标记之前的任何崩溃都会留下
// 一个验证失败的槽，不会被读取采纳。
func (s *Store) writeSlot(path string, generation uint64, payload []byte) error {
	image := encodeSlot(generation, payload, false)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(image); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if s.crashBeforeCommit {
		f.Close()
		return errSimulatedCrash
	}
	if _, err := f.WriteAt([]byte{commitCommitted}, commitOff); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}
