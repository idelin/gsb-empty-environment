// Package gsb 实现一个分支化、写时复制的字节序列存储。
//
// 物理层：定长负载（BlockSize 字节）的内容寻址块，按内容哈希去重，
// 引用计数管理生命周期，账本以“物理块”为单位记账（含事务期临时复制）。
// 逻辑层：版本（分支）是一串不可变跨度 Span{块ID, 偏移, 长度}，
// 编辑通过拼接新旧跨度完成，未触及的块零复制共享。
package gsb

import "crypto/sha256"

// BlockSize 是每个物理块的最大负载字节数。
const BlockSize = 64

// BlockHeaderBytes 是每个物理块在账本中登记的固定头开销：
// id(8) + refs(4) + length(4)。记账 = 头开销 + 实际负载长度。
const BlockHeaderBytes = 16

type block struct {
	id   uint64
	data []byte // len(data) <= BlockSize
	refs int
}

// blockBytes 返回该物理块在账本中占用的字节数（头 + 负载）。
func blockBytes(b *block) int { return BlockHeaderBytes + len(b.data) }

// Store 是物理块存储：内容寻址去重 + 引用计数 + 字节账本。
type Store struct {
	blocks    map[uint64]*block
	byHash    map[[sha256.Size]byte]uint64
	nextID    uint64
	quota     int // 存活物理字节上限；0 表示不限
	live      int // 所有 refs>0 块的占用合计（含事务期临时复制）
	allocated int // 已分配（含待回收垃圾）的占用合计
}

// NewStore 创建物理块存储，quota 为存活字节配额（0 = 不限）。
func NewStore(quota int) *Store {
	return &Store{
		blocks: make(map[uint64]*block),
		byHash: make(map[[sha256.Size]byte]uint64),
		quota:  quota,
	}
}

// LiveBytes 返回存活物理块（含事务中的临时复制）占用的字节数。
func (s *Store) LiveBytes() int { return s.live }

// AllocatedBytes 返回已分配物理块（含可回收垃圾）占用的字节数。
func (s *Store) AllocatedBytes() int { return s.allocated }

// GarbageBytes 返回已失去引用、可被 Collect 回收的字节数。
func (s *Store) GarbageBytes() int { return s.allocated - s.live }

// LiveBlocks 返回存活物理块数量。
func (s *Store) LiveBlocks() int {
	n := 0
	for _, b := range s.blocks {
		if b.refs > 0 {
			n++
		}
	}
	return n
}

// Collect 回收所有引用计数为 0 的物理块，返回回收的块数与字节数。
// 被任何存活版本借用的底块引用计数大于 0，绝不会被回收。
func (s *Store) Collect() (blocks, bytes int) {
	for id, b := range s.blocks {
		if b.refs != 0 {
			continue
		}
		h := sha256.Sum256(b.data)
		if s.byHash[h] == id {
			delete(s.byHash, h)
		}
		delete(s.blocks, id)
		s.allocated -= blockBytes(b)
		blocks++
		bytes += blockBytes(b)
	}
	return blocks, bytes
}

// retainRaw / releaseRaw 直接调整引用计数与存活账本，不记录撤销日志。
func (s *Store) retainRaw(id uint64) {
	b := s.blocks[id]
	if b == nil {
		return
	}
	if b.refs == 0 {
		s.live += blockBytes(b)
	}
	b.refs++
}

func (s *Store) releaseRaw(id uint64) {
	b := s.blocks[id]
	if b == nil {
		return
	}
	b.refs--
	if b.refs == 0 {
		s.live -= blockBytes(b)
	}
}
