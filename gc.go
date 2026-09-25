package mvcc

import "os"

// GC 以最老活跃读事务的固定版本为界，回收内存中不再可见的版本链前缀。
//
// 对每个 key 的版本链（升序）：保留最后一个 version <= 上界 的条目
// （该版本仍可能被恰好固定在上界的读者看到），丢弃更早的条目；
// 若整条链都不超过上界且最新条目是墓碑，则整条链连同墓碑一并移除。
// 返回释放的版本条目数。
func (s *Store) GC() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, ErrStoreClosed
	}
	threshold := s.minPinnedLocked()
	freed := 0
	for key, c := range s.index {
		cut := 0
		for cut < len(c.vers) && c.vers[cut].version <= threshold {
			cut++
		}
		if cut == len(c.vers) && c.vers[len(c.vers)-1].tombstone {
			// 整条链对所有现存及未来读者都不可见，且结尾是墓碑：整体移除。
			freed += len(c.vers)
			delete(s.index, key)
			continue
		}
		if cut > 1 {
			freed += cut - 1
			// 复制到新切片，真正释放底层数组前缀。
			c.vers = append([]verEntry(nil), c.vers[cut-1:]...)
		}
	}
	return freed, nil
}

// Compact 把 GC 上界之后仍可见的状态重写到新日志文件，原子替换旧日志，
// 回收磁盘空间。活跃读事务不受影响（它们读内存索引）；进程崩溃后旧读者
// 本就随之消失，因此日志里只需保留上界边界条目及更新的条目。
func (s *Store) Compact() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrStoreClosed
	}
	threshold := s.minPinnedLocked()

	tmpPath := s.logPath + ".tmp"
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	fail := func(err error) error {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	// 先写一个空操作的锚点帧，保证版本水位线随压缩后的日志存活，
	// 即使所有数据条目都被回收，重启后版本号也不会回退复用。
	if _, err := tmp.Write(encodeFrame(s.version, nil)); err != nil {
		return fail(err)
	}
	for key, c := range s.index {
		// 找保留起点：最后一个 version <= threshold 的条目（边界条目），
		// 以及它之后所有条目。
		start := 0
		for i, e := range c.vers {
			if e.version <= threshold {
				start = i
			}
		}
		for _, e := range c.vers[start:] {
			op := logOp{del: e.tombstone, key: key, value: e.value}
			if _, err := tmp.Write(encodeFrame(e.version, []logOp{op})); err != nil {
				return fail(err)
			}
		}
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, s.logPath); err != nil {
		return err
	}
	// fsync 目录，保证 rename 本身持久。
	if dir, err := os.Open(s.dir); err == nil {
		dir.Sync()
		dir.Close()
	}
	s.f.Close()
	f, err := os.OpenFile(s.logPath, os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	s.f = f
	_, err = s.f.Seek(0, os.SEEK_END)
	return err
}

// LogSize 返回当前日志文件大小（字节），用于观察回收效果。
func (s *Store) LogSize() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, ErrStoreClosed
	}
	fi, err := s.f.Stat()
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}
