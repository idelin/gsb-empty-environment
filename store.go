package auditjoin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// StoreStats 记录存储替身的调用计数，测试可据此断言幂等行为。
type StoreStats struct {
	Puts         int // 所有 Put 调用次数（含被拒绝或重放）
	Changes      int // 实际产生写入/更新的次数
	Idempotent   int // 同版本同内容的幂等重放次数
	OldRejected  int // 旧版本被拒绝的次数
	Conflicts    int // 同版本内容冲突次数
	DistinctKeys int // 当前保存的不同物理键数量
	VersionBumps int // 版本递增次数
}

// storeKey 是存储侧物理键：逻辑 EventKey 之外叠加回绕周期消歧。
type storeKey struct {
	key   EventKey
	cycle uint64
}

// memCore 是内存幂等存储的核心实现，MemStore 与 FileStore 共用。
type memCore struct {
	mu       sync.Mutex
	records  map[storeKey]Record
	versions map[storeKey][]uint64 // 每个物理键保留过的版本历史，供测试断言
	stats    StoreStats
}

// coreSnapshot 是 memCore 的完整内存快照，用于落盘失败时整体回滚。
type coreSnapshot struct {
	records  map[storeKey]Record
	versions map[storeKey][]uint64
	stats    StoreStats
}

func newMemCore() *memCore {
	return &memCore{
		records:  make(map[storeKey]Record),
		versions: make(map[storeKey][]uint64),
	}
}

func (c *memCore) put(_ context.Context, key EventKey, cycle uint64, rec Record) (Record, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.stats.Puts++
	sk := storeKey{key: key, cycle: cycle}
	existing, ok := c.records[sk]
	if !ok {
		c.records[sk] = rec
		c.versions[sk] = append(c.versions[sk], rec.Version)
		c.stats.Changes++
		c.stats.DistinctKeys = len(c.records)
		return rec, true, nil
	}

	switch {
	case rec.Version > existing.Version:
		c.records[sk] = rec
		c.versions[sk] = append(c.versions[sk], rec.Version)
		c.stats.Changes++
		c.stats.VersionBumps++
		return rec, true, nil
	case rec.Version < existing.Version:
		c.stats.OldRejected++
		return existing, false, nil
	case recordsEqual(existing, rec):
		c.stats.Idempotent++
		return existing, false, nil
	default:
		c.stats.Conflicts++
		return existing, false, ErrConflict
	}
}

func (c *memCore) get(_ context.Context, key EventKey, cycle uint64) (Record, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.records[storeKey{key: key, cycle: cycle}]
	return rec, ok, nil
}

func (c *memCore) list(_ context.Context) ([]Record, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Record, 0, len(c.records))
	for _, rec := range c.records {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key.NodeID != out[j].Key.NodeID {
			return out[i].Key.NodeID < out[j].Key.NodeID
		}
		if out[i].Key.Generation != out[j].Key.Generation {
			return out[i].Key.Generation < out[j].Key.Generation
		}
		if out[i].Cycle != out[j].Cycle {
			return out[i].Cycle < out[j].Cycle
		}
		return out[i].Key.Seq < out[j].Key.Seq
	})
	return out, nil
}

func (c *memCore) snapshotLocked() coreSnapshot {
	records := make(map[storeKey]Record, len(c.records))
	for k, v := range c.records {
		records[k] = v
	}
	versions := make(map[storeKey][]uint64, len(c.versions))
	for k, hist := range c.versions {
		cp := make([]uint64, len(hist))
		copy(cp, hist)
		versions[k] = cp
	}
	return coreSnapshot{records: records, versions: versions, stats: c.stats}
}

// recordsEqual 比较同版本记录的业务内容是否一致（时间戳不参与判定）。
func recordsEqual(a, b Record) bool {
	return a.Key == b.Key &&
		a.Cycle == b.Cycle &&
		a.Complete == b.Complete &&
		bytes.Equal(a.Payload, b.Payload)
}

// MemStore 是纯内存的幂等存储替身，适用于单进程测试。
type MemStore struct {
	core *memCore
}

// NewMemStore 创建内存幂等存储。
func NewMemStore() *MemStore {
	return &MemStore{core: newMemCore()}
}

func (s *MemStore) Put(ctx context.Context, key EventKey, cycle uint64, rec Record) (Record, bool, error) {
	return s.core.put(ctx, key, cycle, rec)
}

func (s *MemStore) Get(ctx context.Context, key EventKey, cycle uint64) (Record, bool, error) {
	return s.core.get(ctx, key, cycle)
}

func (s *MemStore) List(ctx context.Context) ([]Record, error) {
	return s.core.list(ctx)
}

// Stats 返回调用统计快照。
func (s *MemStore) Stats() StoreStats {
	s.core.mu.Lock()
	defer s.core.mu.Unlock()
	return s.core.stats
}

// VersionHistory 返回某物理键历次写入的版本号。
func (s *MemStore) VersionHistory(key EventKey, cycle uint64) []uint64 {
	s.core.mu.Lock()
	defer s.core.mu.Unlock()
	hist := s.core.versions[storeKey{key: key, cycle: cycle}]
	out := make([]uint64, len(hist))
	copy(out, hist)
	return out
}

// fileStoreDTO 是 FileStore 的磁盘 JSON 结构。
type fileStoreDTO struct {
	Records  []Record            `json:"records"`
	Versions map[string][]uint64 `json:"versions,omitempty"`
	Stats    StoreStats          `json:"stats"`
}

// FileStore 是带磁盘持久化的幂等存储替身：每次变更原子落盘，
// 进程重启后内容与统计可完整恢复，用于验证重启不产生重复事件。
type FileStore struct {
	core *memCore
	path string
	mu   sync.Mutex // 串行化“写核心 + 落盘”
}

// OpenFileStore 打开（不存在则创建）一个文件存储。
func OpenFileStore(path string) (*FileStore, error) {
	s := &FileStore{core: newMemCore(), path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("auditjoin: read store %q: %w", path, err)
	}
	if len(data) == 0 {
		return s, nil
	}
	var dto fileStoreDTO
	if err := json.Unmarshal(data, &dto); err != nil {
		return nil, fmt.Errorf("auditjoin: decode store %q: %w", path, err)
	}
	for _, rec := range dto.Records {
		s.core.records[storeKey{key: rec.Key, cycle: rec.Cycle}] = rec
	}
	if dto.Versions != nil {
		// 版本历史仅用于测试观测，无法从 map[string] 直接恢复物理键时忽略也安全，
		// 这里通过当前记录重建可恢复的部分。
		for sk, rec := range s.core.records {
			hist := dto.Versions[physicalKeyString(sk)]
			if len(hist) > 0 {
				s.core.versions[sk] = hist
			} else {
				s.core.versions[sk] = []uint64{rec.Version}
			}
		}
	} else {
		for sk, rec := range s.core.records {
			s.core.versions[sk] = []uint64{rec.Version}
		}
	}
	s.core.stats = dto.Stats
	s.core.stats.DistinctKeys = len(s.core.records)
	return s, nil
}

func (s *FileStore) Put(ctx context.Context, key EventKey, cycle uint64, rec Record) (Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	before := s.core.snapshotForCompare()
	stored, changed, err := s.core.put(ctx, key, cycle, rec)
	if err != nil {
		return stored, changed, err
	}
	if changed {
		if perr := s.persistLocked(); perr != nil {
			// 落盘失败时回滚内存变更，保持内存与磁盘一致。
			s.core.rollback(before)
			return Record{}, false, perr
		}
	}
	return stored, changed, nil
}

func (s *FileStore) Get(ctx context.Context, key EventKey, cycle uint64) (Record, bool, error) {
	return s.core.get(ctx, key, cycle)
}

func (s *FileStore) List(ctx context.Context) ([]Record, error) {
	return s.core.list(ctx)
}

// Stats 返回调用统计快照（含重启前的累计值）。
func (s *FileStore) Stats() StoreStats {
	s.core.mu.Lock()
	defer s.core.mu.Unlock()
	return s.core.stats
}

// VersionHistory 返回某物理键历次写入的版本号。
func (s *FileStore) VersionHistory(key EventKey, cycle uint64) []uint64 {
	s.core.mu.Lock()
	defer s.core.mu.Unlock()
	hist := s.core.versions[storeKey{key: key, cycle: cycle}]
	out := make([]uint64, len(hist))
	copy(out, hist)
	return out
}

func (s *FileStore) persistLocked() error {
	s.core.mu.Lock()
	dto := fileStoreDTO{
		Records:  mustListLocked(s.core),
		Versions: make(map[string][]uint64, len(s.core.versions)),
		Stats:    s.core.stats,
	}
	for sk, hist := range s.core.versions {
		dto.Versions[physicalKeyString(sk)] = append([]uint64(nil), hist...)
	}
	s.core.mu.Unlock()

	data, err := json.MarshalIndent(dto, "", "  ")
	if err != nil {
		return fmt.Errorf("auditjoin: encode store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("auditjoin: mkdir store: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".auditjoin-store-*")
	if err != nil {
		return fmt.Errorf("auditjoin: create temp store: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("auditjoin: write store: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("auditjoin: fsync store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("auditjoin: rename store: %w", err)
	}
	return nil
}

func mustListLocked(c *memCore) []Record {
	out := make([]Record, 0, len(c.records))
	for _, rec := range c.records {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		return physicalKeyString(storeKey{key: out[i].Key, cycle: out[i].Cycle}) <
			physicalKeyString(storeKey{key: out[j].Key, cycle: out[j].Cycle})
	})
	return out
}

func physicalKeyString(sk storeKey) string {
	return fmt.Sprintf("%s|%d", sk.key.String(), sk.cycle)
}

func (c *memCore) snapshotForCompare() coreSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked()
}

func (c *memCore) rollback(prev coreSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = prev.records
	c.versions = prev.versions
	c.stats = prev.stats
	c.stats.DistinctKeys = len(c.records)
}
