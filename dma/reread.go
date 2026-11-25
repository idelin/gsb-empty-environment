package dma

import "errors"

// 以下适配器刻意保留“执行时重读客户机描述符内存”的缺陷，仅用于安全评审
// 与回归测试：它演示校验阶段通过后、执行阶段重新 ReadAt 客户机可写内存
// 会如何被同步点攻击改写地址，从而发起越界 DMA。
//
// 警告：生产设备不得使用 RereadAdapter，所有真实提交路径只能把
// FrozenChain 私有数组交给 DeviceCallback。

// errRereadOOB 由测试注入的故障回调在访问受保护页时返回。
var errRereadOOB = errors.New("dma/reread: DMA hit a protected page")

// RereadBus 是适配器执行单次传输的下游接口（测试中由 IOMMU 模拟实现）。
type RereadBus interface {
	Transfer(addr uint64, length uint32, perm Perm) error
}

// RereadAdapter 是待评审的脆弱适配器。它持有已通过校验的 FrozenChain，
// 却在每次 Submit 时按原槽位重新从客户机内存读取描述符并执行。
type RereadAdapter struct {
	mem      GuestMemory
	ringBase uint64
	bus      RereadBus

	// SyncPoint 在重读之前触发；攻击测试利用它改写客户机环中的地址。
	SyncPoint func(chain *FrozenChain)
}

// NewRereadAdapter 创建待审适配器。
func NewRereadAdapter(mem GuestMemory, ringBase uint64, bus RereadBus) *RereadAdapter {
	return &RereadAdapter{mem: mem, ringBase: ringBase, bus: bus}
}

// Submit 实现 DeviceCallback：先进入同步点，再重读客户机描述符。
func (a *RereadAdapter) Submit(chain *FrozenChain) error {
	if a.SyncPoint != nil {
		a.SyncPoint(chain)
	}
	for i := 0; i < chain.Len(); i++ {
		// 缺陷点：执行阶段再次读取客户机可写内存，而不是使用
		// chain.Descriptor(i) 中的私有冻结值。
		desc, err := a.reread(chain.Slot(i))
		if err != nil {
			return err
		}
		if err := a.bus.Transfer(desc.Addr, desc.Length, desc.Perm()); err != nil {
			return err
		}
		if desc.Flags.End() {
			return nil
		}
	}
	return nil
}

// reread 按槽位从客户机环中重新解码一个描述符。
func (a *RereadAdapter) reread(slot int) (Descriptor, error) {
	buf := make([]byte, DescriptorSize)
	off := a.ringBase + uint64(slot)*DescriptorSize
	if err := readFull(a.mem, buf, off); err != nil {
		return Descriptor{}, err
	}
	return parseDescriptor(buf), nil
}
