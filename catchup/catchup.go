// Package catchup 模拟快照追赶：Leader 向落后副本分块传输快照，
// 期间日志继续追加；副本只有收齐全部块且校验一致才原子替换状态，
// 随后只重放快照索引之后的日志。
package catchup

import (
	"errors"
	"strings"
)

// ErrChecksumMismatch 表示块校验或整体校验不一致。
var ErrChecksumMismatch = errors.New("catchup: checksum mismatch")

// LogEntry 是一条复制日志。
type LogEntry struct {
	Index   uint64
	Term    uint64
	Command string
}

// SnapshotMeta 描述一个快照代次（generation）的元信息。
type SnapshotMeta struct {
	Generation        uint64
	LastIncludedIndex uint64
	LastIncludedTerm  uint64
	NumChunks         int
	ChunkChecksums    []uint32
	DataChecksum      uint32 // 拼装后完整数据的校验和
}

// Chunk 是快照的一个分块，携带所属代次的元信息。
type Chunk struct {
	Meta     SnapshotMeta
	Index    int
	Data     []byte
	Checksum uint32
}

// StagedSnapshot 是暂存区中尚未安装的快照（模拟落盘，重启可恢复）。
type StagedSnapshot struct {
	Meta     SnapshotMeta
	Chunks   [][]byte
	Got      []bool
	Received int
}

// Complete 报告全部块是否已收齐。
func (s *StagedSnapshot) Complete() bool {
	return s.Received == s.Meta.NumChunks
}

func cloneStaged(s *StagedSnapshot) *StagedSnapshot {
	if s == nil {
		return nil
	}
	c := &StagedSnapshot{Meta: s.Meta, Received: s.Received}
	c.Meta.ChunkChecksums = append([]uint32(nil), s.Meta.ChunkChecksums...)
	c.Chunks = make([][]byte, len(s.Chunks))
	for i, p := range s.Chunks {
		c.Chunks[i] = append([]byte(nil), p...)
	}
	c.Got = append([]bool(nil), s.Got...)
	return c
}

// MemStore 模拟持久化暂存区：Follower 崩溃重启后仍可读回。
type MemStore struct {
	staged *StagedSnapshot
}

// NewMemStore 创建一个空的持久化暂存区。
func NewMemStore() *MemStore { return &MemStore{} }

// SaveStaging 持久化暂存快照（拷贝，模拟序列化往返）。
func (m *MemStore) SaveStaging(s *StagedSnapshot) { m.staged = cloneStaged(s) }

// LoadStaging 读回暂存快照，没有则返回 nil。
func (m *MemStore) LoadStaging() *StagedSnapshot { return cloneStaged(m.staged) }

// ClearStaging 安装完成后清除暂存区。
func (m *MemStore) ClearStaging() { m.staged = nil }

func encodeState(cmds []string) []byte { return []byte(strings.Join(cmds, "\x00")) }

func decodeState(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	return strings.Split(string(b), "\x00")
}
