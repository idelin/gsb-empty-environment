package dma

import "sync"

// fakeGuest 是 GuestMemory 的测试实现：内存按页稀疏保存，版本号可被
// 测试显式推动或在写入时自动 +1；钩子用于在 ReadAt 中途制造 ABA。
type fakeGuest struct {
	mu      sync.Mutex
	mem     map[uint64][]byte
	version uint64

	// beforeRead 在每次 ReadAt 进入时触发（持锁），可改写内存与版本。
	beforeRead func(g *fakeGuest)
	reads      int
}

func newFakeGuest(version uint64) *fakeGuest {
	return &fakeGuest{mem: make(map[uint64][]byte), version: version}
}

func (g *fakeGuest) Version() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.version
}

func (g *fakeGuest) ReadAt(p []byte, off uint64) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.reads++
	if g.beforeRead != nil {
		g.beforeRead(g)
	}
	for i := range p {
		addr := off + uint64(i)
		page := addr / PageSize
		buf, ok := g.mem[page]
		if !ok {
			p[i] = 0
			continue
		}
		p[i] = buf[addr%PageSize]
	}
	return len(p), nil
}

// put 写入任意字节并推动版本号。
func (g *fakeGuest) put(off uint64, data []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i, b := range data {
		addr := off + uint64(i)
		page := addr / PageSize
		buf, ok := g.mem[page]
		if !ok {
			buf = make([]byte, PageSize)
			g.mem[page] = buf
		}
		buf[addr%PageSize] = b
	}
	g.version++
}

// setVersion 显式设置版本号，用于制造版本倒退。
func (g *fakeGuest) setVersion(v uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.version = v
}

// putSilent 写入字节但不推动版本号，用于模拟同版本 ABA 改写。
func (g *fakeGuest) putSilent(off uint64, data []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.writeLocked(off, data)
}

// writeLocked 在已持有锁时写入字节，不触碰版本号。
func (g *fakeGuest) writeLocked(off uint64, data []byte) {
	for i, b := range data {
		addr := off + uint64(i)
		page := addr / PageSize
		buf, ok := g.mem[page]
		if !ok {
			buf = make([]byte, PageSize)
			g.mem[page] = buf
		}
		buf[addr%PageSize] = b
	}
}

// putLocked 在已持有锁的钩子内写入并推动版本号。
func (g *fakeGuest) putLocked(off uint64, data []byte) {
	g.writeLocked(off, data)
	g.version++
}

// writeDesc 在 ringBase 下的指定槽位编码描述符，并推动版本号。
func writeDesc(g *fakeGuest, ringBase uint64, slot int, d Descriptor) {
	buf := make([]byte, DescriptorSize)
	encodeDescriptor(buf, d)
	g.put(ringBase+uint64(slot)*DescriptorSize, buf)
}

// readDesc 直接从客户机镜像解码一个槽位，供测试改写后核对。
func readDesc(g *fakeGuest, ringBase uint64, slot int) Descriptor {
	buf := make([]byte, DescriptorSize)
	_, _ = g.ReadAt(buf, ringBase+uint64(slot)*DescriptorSize)
	return parseDescriptor(buf)
}

// faultBus 是带 IOMMU 语义的 RereadBus：只允许访问 allowed 区间，
// 触碰未授权/受保护页立即返回 errRereadOOB。
type faultBus struct {
	allowed []region
	hits    []Descriptor
}

func (b *faultBus) Transfer(addr uint64, length uint32, perm Perm) error {
	end := addr + uint64(length)
	for _, r := range b.allowed {
		if r.base <= addr && end <= r.end && r.perm.Contains(perm) {
			b.hits = append(b.hits, Descriptor{Addr: addr, Length: length, Flags: 0})
			return nil
		}
	}
	return errRereadOOB
}

// recordingCallback 是正确实现的设备回调：只记录私有冻结链，绝不重读
// 客户机内存。
type recordingCallback struct {
	chains []*FrozenChain
	err    error
}

func (c *recordingCallback) Submit(chain *FrozenChain) error {
	c.chains = append(c.chains, chain)
	return c.err
}
