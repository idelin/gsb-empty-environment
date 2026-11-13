package netsec

// RereadAdapter 是【待审的、已知不安全】的参考实现，仅用于代码评审与
// 测试复现 TOCTOU 越界 DMA，禁止用于生产。
//
// 它把冻结链弃置一旁，在执行时重新遍历客户机可写的描述符环——
// 验证与使用之间没有任何绑定，攻击者可在验证后把已检查地址换成
// 受保护页。正确实现必须像 Executor 那样只使用冻结后的私有数组。
type RereadAdapter struct {
	Mem      GuestMemory
	RingBase uint64
	Head     uint16
	// AfterValidate 在验证之后、执行时重读之前调用，是注入改写的同步点。
	AfterValidate func()
	// DMA 对给定客户机地址执行一次 DMA。
	DMA func(addr uint64, length uint32, flags uint16) error
}

// Submit 实现 SubmitFunc，但故意忽略冻结链内容，执行时重读客户机内存。
func (a *RereadAdapter) Submit(chain *FrozenChain) error {
	_ = chain // 漏洞根源：已验证的私有副本被丢弃
	if a.AfterValidate != nil {
		a.AfterValidate()
	}
	idx := a.Head
	for i := 0; i < MaxDescriptors; i++ {
		var raw [DescriptorSize]byte
		if _, err := a.Mem.ReadAt(raw[:], a.RingBase+uint64(idx)*DescriptorSize); err != nil {
			return err
		}
		d := DecodeDescriptor(raw[:])
		if a.DMA != nil {
			if err := a.DMA(d.Addr, d.Len, d.Flags); err != nil {
				return err
			}
		}
		if d.Flags&FlagEnd != 0 {
			return nil
		}
		idx = d.Next
	}
	return nil
}
