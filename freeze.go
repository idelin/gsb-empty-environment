package netsec

// FrozenChain 是已冻结的描述符链：描述符被复制进宿主私有数组，
// 对外只暴露按值拷贝的访问器，客户机此后改写原环不影响本链。
type FrozenChain struct {
	descs []Descriptor // 宿主私有，绝不对外暴露可写引用
	gen   uint64       // 提交代次，由 Executor 赋值
}

// Len 返回链中描述符个数。
func (c *FrozenChain) Len() int { return len(c.descs) }

// At 按值返回第 i 项描述符，调用方无法借此修改链内容。
func (c *FrozenChain) At(i int) Descriptor { return c.descs[i] }

// Generation 返回本次提交的代次。
func (c *FrozenChain) Generation() uint64 { return c.gen }

// Freezer 负责把客户机可写的描述符环冻结为宿主私有、已验证的链。
type Freezer struct {
	mem GuestMemory
	reg *Registry
}

func NewFreezer(mem GuestMemory, reg *Registry) *Freezer {
	return &Freezer{mem: mem, reg: reg}
}

// Freeze 从 ringBase 处的环（至多 64 项）自 head 沿 next 遍历：
//  1. 对整个环做一致性快照（版本一致 + 同纪元字节一致）；
//  2. 沿 next 遍历，END 项停止；未 END 收尾、重复项、越界索引一律拒绝；
//  3. 把描述符复制到宿主私有不可变数组；
//  4. 逐项验证 flags/长度/溢出/总字节预算，并核对区间属于该 VM 已登记
//     DMA 页、权限不超过底层页、不触碰保留区。
func (f *Freezer) Freeze(ringBase uint64, head uint16) (*FrozenChain, error) {
	raw, err := snapshot(f.mem, ringBase, MaxDescriptors*DescriptorSize)
	if err != nil {
		return nil, err
	}
	chain, err := parseChain(raw, head)
	if err != nil {
		return nil, err
	}
	// 先复制到宿主私有数组，之后只基于私有副本验证与提交。
	priv := make([]Descriptor, len(chain))
	copy(priv, chain)

	var total uint64
	for i := range priv {
		d := &priv[i]
		if err := d.validate(); err != nil {
			return nil, err
		}
		total += uint64(d.Len)
		if total > MaxChainBytes {
			return nil, ErrBudgetExceeded
		}
		if err := f.reg.check(d.Addr, uint64(d.Len), d.needPerm()); err != nil {
			return nil, err
		}
	}
	return &FrozenChain{descs: priv}, nil
}

// parseChain 从快照字节中沿 next 遍历出描述符序列。
func parseChain(raw []byte, head uint16) ([]Descriptor, error) {
	if int(head) >= MaxDescriptors {
		return nil, ErrIndexOutOfRange
	}
	var visited [MaxDescriptors]bool
	chain := make([]Descriptor, 0, MaxDescriptors)
	idx := head
	for {
		if int(idx) >= MaxDescriptors {
			return nil, ErrIndexOutOfRange
		}
		if visited[idx] {
			return nil, ErrDuplicate
		}
		visited[idx] = true
		d := DecodeDescriptor(raw[int(idx)*DescriptorSize:])
		chain = append(chain, d)
		if d.Flags&FlagEnd != 0 {
			return chain, nil // END 项不得继续
		}
		if len(chain) == MaxDescriptors {
			return nil, ErrMissingEnd // 整环耗尽仍无 END
		}
		idx = d.Next
	}
}
