package netsec

import (
	"bytes"
	"errors"
)

// ErrBlockOutOfRange 表示块号超出设备容量。
var ErrBlockOutOfRange = errors.New("netsec: block number out of device range")

// ErrBlockFree 表示读取或释放一个当前未分配的块。
var ErrBlockFree = errors.New("netsec: block is not allocated")

// MemoryDevice 是用于小块设备逐块验证的内存块设备。
//
// 它同时保存数据内容、分配状态和引用计数，模拟真实设备的块层。
// 引用计数由 VerifyBlockReferences 依据区段表独立重放，不直接信任
// Analyze 的内部记账，从而为硬链接、合法共享和代次复用提供交叉证据。
type MemoryDevice struct {
	blockSize int
	data      [][]byte
	allocated []bool
	refCount  []int
}

// NewMemoryDevice 创建含 blockCount 个块、每块 blockSize 字节的设备，
// 初始状态全部为未分配。
func NewMemoryDevice(blockCount uint64, blockSize int) *MemoryDevice {
	return &MemoryDevice{
		blockSize: blockSize,
		data:      make([][]byte, blockCount),
		allocated: make([]bool, blockCount),
		refCount:  make([]int, blockCount),
	}
}

// BlockSize 返回块大小。
func (d *MemoryDevice) BlockSize() int { return d.blockSize }

// TotalBlocks 返回块数。
func (d *MemoryDevice) TotalBlocks() uint64 { return uint64(len(d.data)) }

// IsAllocated 实现 AllocationBitmap，设备本身即可直接作为位图输入。
func (d *MemoryDevice) IsAllocated(block uint64) bool {
	if block >= d.TotalBlocks() {
		return false
	}
	return d.allocated[block]
}

// Allocate 分配一个块并写入内容；已分配块返回错误而不是静默覆盖，
// 以便测试能发现双重分配。
func (d *MemoryDevice) Allocate(block uint64, payload []byte) error {
	if block >= d.TotalBlocks() {
		return ErrBlockOutOfRange
	}
	if d.allocated[block] {
		return errors.New("netsec: block already allocated")
	}
	buf := make([]byte, d.blockSize)
	copy(buf, payload)
	d.data[block] = buf
	d.allocated[block] = true
	return nil
}

// Read 读取一个已分配块的内容。
func (d *MemoryDevice) Read(block uint64) ([]byte, error) {
	if block >= d.TotalBlocks() {
		return nil, ErrBlockOutOfRange
	}
	if !d.allocated[block] {
		return nil, ErrBlockFree
	}
	return bytes.Clone(d.data[block]), nil
}

// Free 释放一个已分配块。
func (d *MemoryDevice) Free(block uint64) error {
	if block >= d.TotalBlocks() {
		return ErrBlockOutOfRange
	}
	if !d.allocated[block] {
		return ErrBlockFree
	}
	d.data[block] = nil
	d.allocated[block] = false
	d.refCount[block] = 0
	return nil
}

// RefCount 返回块层当前记录的对象引用数（每个对象对同一块只计一次，
// 与目录项数量无关，因此硬链接不会把同一对象算成两份）。
func (d *MemoryDevice) RefCount(block uint64) (int, error) {
	if block >= d.TotalBlocks() {
		return 0, ErrBlockOutOfRange
	}
	return d.refCount[block], nil
}

// BlockAccount 是独立重放得到的单块参考证据。
type BlockAccount struct {
	Block          uint64
	Allocated      bool
	Referenced     bool
	DistinctOwners int
	Owners         []ObjectRef
	ShareGroups    []string
	LegalSharing   bool
	// OutOfRange 表示区段引用了超出设备容量的块号。
	OutOfRange bool
}

// VerifyBlockReferences 用区段表在块设备上逐块重放引用关系。
//
// 重放规则与 Analyze 保持独立实现：
//   - 按对象（而非目录项）记账，硬链接的多个目录项不产生额外引用；
//   - 同一对象重复列出同一块只计一个持有者；
//   - 仅当全部持有者共享同一个非空共享组时共享合法；
//   - 越界块标记 OutOfRange 并返回错误，调用方据此判定夹具失配。
//
// 返回以块号排序的逐块账目，可与 Report 的发现集合做逐块交叉核对。
func VerifyBlockReferences(device *MemoryDevice, table *SegmentTable) ([]BlockAccount, error) {
	if device == nil || table == nil {
		return nil, ErrNilInput
	}
	type ownerRecord struct {
		ref        ObjectRef
		shareGroup string
	}
	ownersByBlock := make(map[uint64]map[uint64]ownerRecord)
	var rangeError error

	for _, obj := range table.Objects {
		for _, extent := range obj.Extents {
			if extent.Length == 0 {
				return nil, ErrInvalidExtent
			}
			for _, block := range extent.Blocks() {
				if block >= device.TotalBlocks() {
					rangeError = ErrBlockOutOfRange
					continue
				}
				if ownersByBlock[block] == nil {
					ownersByBlock[block] = make(map[uint64]ownerRecord)
				}
				ownersByBlock[block][obj.Ref.ID] = ownerRecord{
					ref:        obj.Ref,
					shareGroup: obj.ShareGroup,
				}
			}
		}
	}

	blocks := make([]uint64, 0, len(ownersByBlock))
	for block := range ownersByBlock {
		blocks = append(blocks, block)
	}
	sortBlockNumbers(blocks)

	accounts := make([]BlockAccount, 0, len(blocks))
	for _, block := range blocks {
		records := ownersByBlock[block]
		ids := make([]uint64, 0, len(records))
		for id := range records {
			ids = append(ids, id)
		}
		sortBlockNumbers(ids)

		account := BlockAccount{
			Block:          block,
			Allocated:      device.IsAllocated(block),
			Referenced:     true,
			DistinctOwners: len(ids),
		}
		group := ""
		for i, id := range ids {
			record := records[id]
			account.Owners = append(account.Owners, record.ref)
			account.ShareGroups = append(account.ShareGroups, record.shareGroup)
			if i == 0 {
				group = record.shareGroup
			}
		}
		if len(ids) > 1 {
			legal := group != ""
			for _, id := range ids[1:] {
				if records[id].shareGroup != group {
					legal = false
				}
			}
			account.LegalSharing = legal
		}
		accounts = append(accounts, account)

		device.refCount[block] = len(ids)
	}
	return accounts, rangeError
}

func sortBlockNumbers(blocks []uint64) {
	for i := 1; i < len(blocks); i++ {
		for j := i; j > 0 && blocks[j-1] > blocks[j]; j-- {
			blocks[j-1], blocks[j] = blocks[j], blocks[j-1]
		}
	}
}
