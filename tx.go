package gsb

import (
	"crypto/sha256"
	"errors"
)

var (
	// ErrQuota 表示物理块配额超限，触发整体回滚。
	ErrQuota = errors.New("gsb: 物理块配额超限")
	// ErrTxClosed 表示事务已提交或已回滚。
	ErrTxClosed = errors.New("gsb: 事务已结束")
	// ErrRange 表示越界访问。
	ErrRange = errors.New("gsb: 越界访问")
	// ErrNoBranch 表示分支不存在。
	ErrNoBranch = errors.New("gsb: 分支不存在")
)

// Tx 是一次原子操作：所有引用计数变更与块分配都记入撤销日志，
// Commit 生效，Rollback（取消或配额超限）整体回滚。
type Tx struct {
	store     *Store
	undos     []func()
	tempBytes int // 本事务内新创建物理块的占用（去重命中不计）
	tempCount int
	closed    bool
}

// TempBytes 返回事务进行中新复制物理块占用的字节数。
// 这些临时复制在事务期间同样计入 Store.LiveBytes。
func (tx *Tx) TempBytes() int { return tx.tempBytes }

// TempBlocks 返回事务进行中新创建的物理块数量。
func (tx *Tx) TempBlocks() int { return tx.tempCount }

func (tx *Tx) checkQuota(delta int) error {
	s := tx.store
	if s.quota > 0 && s.live+delta > s.quota {
		return ErrQuota
	}
	return nil
}

// alloc 为一段负载分配物理块：内容相同则去重复用（含复活垃圾块），
// 否则创建新块并计入临时复制。配额不足时返回 ErrQuota。
func (tx *Tx) alloc(data []byte) (uint64, error) {
	s := tx.store
	h := sha256.Sum256(data)
	if id, ok := s.byHash[h]; ok {
		b := s.blocks[id]
		if b.refs == 0 { // 复活垃圾块将增加存活字节
			if err := tx.checkQuota(blockBytes(b)); err != nil {
				return 0, err
			}
		}
		tx.retain(id)
		return id, nil
	}
	nb := &block{id: s.nextID, data: append([]byte(nil), data...)}
	if err := tx.checkQuota(blockBytes(nb)); err != nil {
		return 0, err
	}
	s.nextID++
	s.blocks[nb.id] = nb
	s.byHash[h] = nb.id
	s.allocated += blockBytes(nb)
	tx.tempBytes += blockBytes(nb)
	tx.tempCount++
	id, size := nb.id, blockBytes(nb)
	// 撤销日志先入“删块”，再由 retain 入“减引用”；
	// 回滚逆序执行：先减引用归零，再删块。
	tx.undos = append(tx.undos, func() {
		delete(s.blocks, id)
		if s.byHash[h] == id {
			delete(s.byHash, h)
		}
		s.allocated -= size
	})
	tx.retain(id)
	return id, nil
}

func (tx *Tx) retain(id uint64) {
	tx.store.retainRaw(id)
	tx.undos = append(tx.undos, func() { tx.store.releaseRaw(id) })
}

func (tx *Tx) release(id uint64) {
	tx.store.releaseRaw(id)
	tx.undos = append(tx.undos, func() { tx.store.retainRaw(id) })
}

// Commit 提交事务，撤销日志作废。
func (tx *Tx) Commit() error {
	if tx.closed {
		return ErrTxClosed
	}
	tx.closed = true
	tx.undos = nil
	return nil
}

// Rollback 逆序执行撤销日志，整体回滚到事务开始前的状态。
func (tx *Tx) Rollback() error {
	if tx.closed {
		return ErrTxClosed
	}
	tx.closed = true
	for i := len(tx.undos) - 1; i >= 0; i-- {
		tx.undos[i]()
	}
	tx.undos = nil
	return nil
}
