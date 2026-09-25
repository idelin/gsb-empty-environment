// Package mvcc 实现单写多读（single-writer multi-reader）的版本化记录存储。
//
//   - 每次 Commit 产生一个全局单调递增的版本号；
//   - 读事务（Snapshot）固定（pin）开启时的版本，写入后旧读者仍读旧值；
//   - Delete 写入墓碑（tombstone），旧版本读者仍可见删除前的值；
//   - GC 以最老活跃读事务的版本为界回收版本链前缀，长读不阻塞写，
//     但会限制回收；旧读者关闭后链前缀才可释放；
//   - 所有文件位于调用方指定的目录。
package mvcc

import (
	"os"
	"path/filepath"
	"sync"
)

const logFileName = "data.log"

type verEntry struct {
	version   uint64
	tombstone bool
	value     []byte
}

// chain 是某个 key 的版本链，按 version 升序。
type chain struct {
	vers []verEntry
}

// Options 打开 Store 的选项。
type Options struct {
	// SyncWrites 为 true 时，Commit 在 fsync 完成后才返回，
	// 提供断电级持久性；为 false 时只保证进程崩溃级。
	SyncWrites bool
	// Failpoints 故障注入点，仅用于测试。
	Failpoints Failpoints
}

// Store 是版本化记录存储。并发安全：写事务单飞，读事务任意并发。
type Store struct {
	dir     string
	logPath string

	mu           sync.Mutex
	f            *os.File
	index        map[string]*chain
	version      uint64 // 最新已提交版本
	writerActive bool
	readers      map[*ReadTx]struct{}
	syncWrites   bool
	fp           Failpoints
	closed       bool
}

// Open 打开（必要时创建）dir 下的存储，并重放日志完成崩溃恢复。
func Open(dir string, opts Options) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{
		dir:        dir,
		logPath:    filepath.Join(dir, logFileName),
		index:      make(map[string]*chain),
		readers:    make(map[*ReadTx]struct{}),
		syncWrites: opts.SyncWrites,
		fp:         opts.Failpoints,
	}
	f, err := os.OpenFile(s.logPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	s.f = f
	if err := s.recover(); err != nil {
		f.Close()
		return nil, err
	}
	return s, nil
}

// recover 重放日志：应用所有完整帧，截断尾部撕裂写。
func (s *Store) recover() error {
	frames, validLen, err := replayLog(s.f)
	if err != nil {
		return err
	}
	for _, payload := range frames {
		version, ops, err := decodeFrame(payload)
		if err != nil {
			return err
		}
		s.applyLocked(version, ops)
	}
	// 截断掉不完整的尾部（断电撕裂写）。
	if err := s.f.Truncate(validLen); err != nil {
		return err
	}
	_, err = s.f.Seek(0, os.SEEK_END)
	return err
}

// applyLocked 把一帧事务应用到内存索引。调用方需持有锁（恢复期间除外）。
func (s *Store) applyLocked(version uint64, ops []logOp) {
	for _, op := range ops {
		c := s.index[op.key]
		if c == nil {
			c = &chain{}
			s.index[op.key] = c
		}
		c.vers = append(c.vers, verEntry{version: version, tombstone: op.del, value: op.value})
	}
	if version > s.version {
		s.version = version
	}
}

// Version 返回最新已提交版本号。
func (s *Store) Version() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// Close 关闭存储。重复关闭返回 ErrStoreClosed。
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrStoreClosed
	}
	s.closed = true
	return s.f.Close()
}

// minPinnedLocked 返回回收上界：最老活跃读事务的固定版本；
// 没有活跃读者时返回当前最新版本。
func (s *Store) minPinnedLocked() uint64 {
	if len(s.readers) == 0 {
		return s.version
	}
	min := s.version
	for r := range s.readers {
		if r.pinned < min {
			min = r.pinned
		}
	}
	return min
}
