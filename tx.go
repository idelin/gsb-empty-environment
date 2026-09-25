package mvcc

// WriteTx 是唯一允许在飞的写事务。
type WriteTx struct {
	s    *Store
	ops  []logOp
	done bool
}

// Begin 开启写事务。已有活跃写事务时返回 ErrWriterActive（单写约束）。
func (s *Store) Begin() (*WriteTx, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrStoreClosed
	}
	if s.writerActive {
		return nil, ErrWriterActive
	}
	s.writerActive = true
	return &WriteTx{s: s}, nil
}

// Put 在事务内设置 key 的值。
func (tx *WriteTx) Put(key string, value []byte) error {
	tx.s.mu.Lock()
	defer tx.s.mu.Unlock()
	if tx.done {
		return ErrTxClosed
	}
	tx.ops = append(tx.ops, logOp{key: key, value: append([]byte(nil), value...)})
	return nil
}

// Delete 在事务内删除 key（提交时写入墓碑）。
func (tx *WriteTx) Delete(key string) error {
	tx.s.mu.Lock()
	defer tx.s.mu.Unlock()
	if tx.done {
		return ErrTxClosed
	}
	tx.ops = append(tx.ops, logOp{del: true, key: key})
	return nil
}

// Commit 把事务作为一个原子帧追加到日志，再应用到内存索引。
// 重复 Commit 或 Commit 已回滚的事务返回 ErrTxClosed。
func (tx *WriteTx) Commit() error {
	s := tx.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if tx.done {
		return ErrTxClosed
	}
	tx.done = true
	s.writerActive = false

	if len(tx.ops) == 0 {
		return nil
	}
	if s.fp.BeforeAppend != nil {
		if err := s.fp.BeforeAppend(); err != nil {
			return err
		}
	}
	version := s.version + 1
	if _, err := s.f.Write(encodeFrame(version, tx.ops)); err != nil {
		return err
	}
	if s.fp.AfterAppendBeforeSync != nil {
		if err := s.fp.AfterAppendBeforeSync(); err != nil {
			return err
		}
	}
	if s.syncWrites {
		if err := s.f.Sync(); err != nil {
			return err
		}
	}
	if s.fp.AfterSyncBeforeApply != nil {
		if err := s.fp.AfterSyncBeforeApply(); err != nil {
			return err
		}
	}
	s.applyLocked(version, tx.ops)
	return nil
}

// Rollback 放弃事务。重复调用返回 ErrTxClosed。
func (tx *WriteTx) Rollback() error {
	s := tx.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if tx.done {
		return ErrTxClosed
	}
	tx.done = true
	s.writerActive = false
	tx.ops = nil
	return nil
}

// ReadTx 是固定版本的读事务（快照）。
type ReadTx struct {
	s      *Store
	pinned uint64
	closed bool
}

// Snapshot 开启读事务，固定当前最新版本。
func (s *Store) Snapshot() (*ReadTx, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrStoreClosed
	}
	tx := &ReadTx{s: s, pinned: s.version}
	s.readers[tx] = struct{}{}
	return tx, nil
}

// Version 返回该读事务固定的版本号。
func (tx *ReadTx) Version() uint64 { return tx.pinned }

// Get 读取 key 在固定版本下可见的值。
// 第二个返回值表示是否存在（墓碑视为不存在）。
// 事务关闭后调用返回 ErrTxClosed。
func (tx *ReadTx) Get(key string) ([]byte, bool, error) {
	tx.s.mu.Lock()
	defer tx.s.mu.Unlock()
	if tx.closed {
		return nil, false, ErrTxClosed
	}
	c := tx.s.index[key]
	if c == nil {
		return nil, false, nil
	}
	// 版本链升序，找最后一个 version <= pinned 的条目。
	for i := len(c.vers) - 1; i >= 0; i-- {
		e := c.vers[i]
		if e.version <= tx.pinned {
			if e.tombstone {
				return nil, false, nil
			}
			return append([]byte(nil), e.value...), true, nil
		}
	}
	return nil, false, nil
}

// Close 结束读事务，解除对回收的限制。重复关闭返回 ErrTxClosed。
func (tx *ReadTx) Close() error {
	tx.s.mu.Lock()
	defer tx.s.mu.Unlock()
	if tx.closed {
		return ErrTxClosed
	}
	tx.closed = true
	delete(tx.s.readers, tx)
	return nil
}
