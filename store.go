package netsec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Record 是一次永久随机化的持久化结果。
type Record struct {
	// DeviceID 标识采集宿主设备。
	DeviceID string `json:"device_id"`
	// Field 标识被采集的敏感字段（敏感类别）。
	Field string `json:"field"`
	// ParamVersion 标识抽取该永久状态时使用的参数版本。
	ParamVersion string `json:"param_version"`
	// Fingerprint 绑定生成该记录时使用的完整参数（两层矩阵与版本），
	// 防止同版本号下矩阵被悄悄替换后误复用旧记忆。
	Fingerprint string `json:"fingerprint"`
	// Truth 是宿主传入的原始敏感符号。
	Truth int `json:"truth"`
	// Permanent 是永久随机化（PRR）抽取出的符号。
	Permanent int `json:"permanent"`
}

// Store 持久化永久随机化记录。实现必须保证 Commit 的原子性：调用方看到
// nil 错误后，后续 Load（含进程重启后的 Load）必须能读到该记录；
// Commit 返回错误时不得留下可被 Load 观察到的部分写入。
type Store interface {
	// Load 返回 key 对应的记录；不存在时返回 ErrNotFound。
	Load(ctx context.Context, key string) (Record, error)
	// Commit 原子写入记录。
	Commit(ctx context.Context, key string, rec Record) error
}

// ErrNotFound 表示存储中不存在指定 key 的永久记录。
var ErrNotFound = errors.New("netsec: permanent record not found")

// FingerprintMismatchError 表示同一（设备, 字段, 版本）上已存在永久记忆，
// 但其参数指纹与当前 Reporter 配置不一致。
type FingerprintMismatchError struct {
	Key      string
	Got      string
	Expected string
}

func (e *FingerprintMismatchError) Error() string {
	return fmt.Sprintf("netsec: fingerprint mismatch for %s: stored %s, expected %s", e.Key, e.Got, e.Expected)
}

// Is 支持 errors.Is(err, ErrFingerprintMismatch) 形式的判断。
func (e *FingerprintMismatchError) Is(target error) bool {
	_, ok := target.(*FingerprintMismatchError)
	return ok
}

// ErrFingerprintMismatch 是指纹不匹配错误的哨兵值。
var ErrFingerprintMismatch = &FingerprintMismatchError{}

// Key 计算永久记录的存储键，绑定设备、字段与参数版本。
func Key(deviceID, field, paramVersion string) string {
	h := sha256.New()
	fmt.Fprintf(h, "device=%s\x00field=%s\x00version=%s", deviceID, field, paramVersion)
	return hex.EncodeToString(h.Sum(nil))
}

// Fingerprint 对完整参数集（版本与两层矩阵）计算稳定指纹。
// 矩阵以确定性格式写入，不依赖指针或 map 迭代顺序。
func Fingerprint(paramVersion string, permanent, instantaneous Matrix) (string, error) {
	if err := permanent.Validate(); err != nil {
		return "", err
	}
	if err := instantaneous.Validate(); err != nil {
		return "", err
	}
	h := sha256.New()
	fmt.Fprintf(h, "version=%s\n", paramVersion)
	writeCanonicalMatrix(h, "permanent", permanent)
	writeCanonicalMatrix(h, "instantaneous", instantaneous)
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeCanonicalMatrix(w io.Writer, name string, m Matrix) {
	fmt.Fprintf(w, "%s:n=%d\n", name, m.Size())
	for _, row := range m.Row {
		for j, r := range row {
			if j > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, "%d/%d", r.Numerator, r.Denominator)
		}
		fmt.Fprintln(w)
	}
}

// MemoryStore 是进程内的 Store 实现，主要用于测试与单进程宿主。
// 并发安全；Commit 对同一 key 的并发写入以先到者为准。
type MemoryStore struct {
	mu      sync.Mutex
	records map[string]Record
}

// NewMemoryStore 创建空的内存存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{records: make(map[string]Record)}
}

func (s *MemoryStore) Load(ctx context.Context, key string) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[key]
	if !ok {
		return Record{}, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return rec, nil
}

func (s *MemoryStore) Commit(ctx context.Context, key string, rec Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[key]; ok {
		// 已有持久化记录时保持幂等：同一 key 只接受第一次写入，
		// 调用方应先 Load 复用，绝不允许覆盖重新抽取的结果。
		return nil
	}
	s.records[key] = rec
	return nil
}

// Len 返回已持久化的记录数，主要用于测试断言。
func (s *MemoryStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}
