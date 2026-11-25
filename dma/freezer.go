package dma

import "bytes"

// FrozenChain 是宿主私有的不可变描述符链。字段全部不导出，外部代码
// 无法构造或改写，只能通过访问器读取；切片在冻结完成后不再与任何
// 客户机内存别名。
type FrozenChain struct {
	generation uint64
	totalBytes uint64
	entries    []frozenEntry
}

// frozenEntry 是私有数组中的元素，记录链上槽位与描述符值拷贝。
type frozenEntry struct {
	slot int
	desc Descriptor
}

// isFrozenChain 让 DeviceCallback 的参数无法被外部类型伪造。
func (*FrozenChain) isFrozenChain() {}

// Generation 返回该链被执行器受理时绑定的提交代次；
// 未提交给 Executor 的链代次为 0。
func (c *FrozenChain) Generation() uint64 { return c.generation }

// Len 返回链上描述符数量。
func (c *FrozenChain) Len() int { return len(c.entries) }

// TotalBytes 返回链上所有描述符长度之和。
func (c *FrozenChain) TotalBytes() uint64 { return c.totalBytes }

// Slot 返回第 i 个链项在原描述符环中的槽位索引。
func (c *FrozenChain) Slot(i int) int { return c.entries[i].slot }

// Descriptor 返回第 i 个链项的值拷贝；调用方修改返回值不影响冻结链。
func (c *FrozenChain) Descriptor(i int) Descriptor { return c.entries[i].desc }

// Freezer 负责把客户机描述符环快照成 FrozenChain，并在冻结阶段
// 完成结构、预算与 DMA 页/权限校验。它可作为字段嵌入具体设备宿主。
type Freezer struct {
	pages  *PageTable
	budget uint64
}

// NewFreezer 构造冻结器。pages 登记该虚拟机允许 DMA 的页与保留区，
// budget 是单次提交全部描述符长度之和的上限；budget 为 0 表示不限制。
func NewFreezer(pages *PageTable, budget uint64) *Freezer {
	return &Freezer{pages: pages, budget: budget}
}

// Freeze 读取 mem 上从 ringBase 起、ringSize 个 16 字节槽位组成的环，
// 从 head 开始沿 next 遍历，校验通过后返回宿主私有不可变链。
//
// 快照协议：读前版本 V0，读取整环，读后版本 V1 必须与 V0 相等；
// 随后再读一遍得到 V2，要求 V2==V1 且两次字节完全一致。任何版本
// 前进/倒退或同版本字节变化都拒绝快照，避免 ABA。
func (f *Freezer) Freeze(mem GuestMemory, ringBase uint64, ringSize int, head uint16) (*FrozenChain, error) {
	if ringSize < 1 || ringSize > MaxRingEntries {
		return nil, ErrRingSize
	}
	if int(head) >= ringSize {
		return nil, ErrIndexOutOfRange
	}
	ringBytes := ringSize * DescriptorSize
	if uint64(ringBytes) > ^uint64(0)-ringBase {
		return nil, ErrAddressOverflow
	}

	first, err := readRingSnapshot(mem, ringBase, ringBytes)
	if err != nil {
		return nil, err
	}
	second, err := readRingSnapshot(mem, ringBase, ringBytes)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(first, second) {
		return nil, ErrABAMutation
	}

	chain, err := traverseRing(first, ringSize, head, f.budget)
	if err != nil {
		return nil, err
	}
	if f.pages != nil {
		for i := range chain.entries {
			if err := f.pages.validate(chain.entries[i].desc); err != nil {
				return nil, err
			}
		}
	}
	return chain, nil
}

// readRingSnapshot 执行一次“版本—读取—版本”的一致性读取。
func readRingSnapshot(mem GuestMemory, ringBase uint64, ringBytes int) ([]byte, error) {
	before := mem.Version()
	buf := make([]byte, ringBytes)
	if err := readFull(mem, buf, ringBase); err != nil {
		return nil, err
	}
	after := mem.Version()
	switch {
	case after < before:
		return nil, ErrVersionRegressed
	case after != before:
		return nil, ErrVersionChanged
	default:
		return buf, nil
	}
}

// traverseRing 在私有字节快照上沿 next 走链，全程不再访问客户机内存。
func traverseRing(ring []byte, ringSize int, head uint16, budget uint64) (*FrozenChain, error) {
	seen := make(map[uint16]struct{}, ringSize)
	entries := make([]frozenEntry, 0, ringSize)
	var total uint64

	idx := head
	for step := 0; step < ringSize; step++ {
		if int(idx) >= ringSize {
			return nil, ErrIndexOutOfRange
		}
		off := int(idx) * DescriptorSize
		desc := parseDescriptor(ring[off : off+DescriptorSize])
		if err := desc.validate(); err != nil {
			return nil, err
		}
		if budget != 0 && total > budget-uint64(desc.Length) {
			return nil, ErrBudgetExceeded
		}
		total += uint64(desc.Length)

		if _, dup := seen[idx]; dup {
			// 覆盖共享 next 子链：收敛到已访问槽位与自环都在此拒绝。
			return nil, ErrDuplicateEntry
		}
		seen[idx] = struct{}{}
		entries = append(entries, frozenEntry{slot: int(idx), desc: desc})

		if desc.Flags.End() {
			// END 是链尾：即使 next 非零也不得继续，直接冻结返回。
			return &FrozenChain{entries: entries, totalBytes: total}, nil
		}
		if int(desc.Next) >= ringSize {
			return nil, ErrIndexOutOfRange
		}
		if _, dup := seen[desc.Next]; dup {
			// 下一步指向已访问槽位（自环/汇合子链），重复访问先于步数耗尽判定。
			return nil, ErrDuplicateEntry
		}
		idx = desc.Next
	}
	// 走满 ringSize 步仍未遇到 END（未终止环），拒绝。
	return nil, ErrUnterminatedChain
}
