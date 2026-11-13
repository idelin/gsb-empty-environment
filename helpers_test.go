package netsec

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// fakeMemory 是测试用客户机内存：写操作使版本号单调前进。
type fakeMemory struct {
	mu      sync.Mutex
	bytes   []byte
	version uint64
	onRead  func() // 每次 ReadAt 复制完成后调用（持锁）
}

func newFakeMemory(size uint64) *fakeMemory {
	return &fakeMemory{bytes: make([]byte, size)}
}

func (m *fakeMemory) ReadAt(p []byte, addr uint64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if addr+uint64(len(p)) > uint64(len(m.bytes)) {
		return 0, fmt.Errorf("read out of bounds: %#x+%#x", addr, len(p))
	}
	copy(p, m.bytes[addr:addr+uint64(len(p))])
	if m.onRead != nil {
		m.onRead()
	}
	return len(p), nil
}

func (m *fakeMemory) Version() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.version
}

// write 模拟客户机写内存，版本号前进。
func (m *fakeMemory) write(addr uint64, b []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	copy(m.bytes[addr:addr+uint64(len(b))], b)
	m.version++
}

func (m *fakeMemory) writeDesc(ringBase uint64, idx uint16, d Descriptor) {
	b := EncodeDescriptor(d)
	m.write(ringBase+uint64(idx)*DescriptorSize, b[:])
}

// abaMemory 版本号永远不变，但每次读都偷偷改写环内一个字节：
// 最终版本相同不能证明期间没变，快照必须以 ABA 拒绝。
type abaMemory struct {
	*fakeMemory
	flipAddr uint64
}

func (m *abaMemory) ReadAt(p []byte, addr uint64) (int, error) {
	n, err := m.fakeMemory.ReadAt(p, addr)
	m.fakeMemory.mu.Lock()
	m.fakeMemory.bytes[m.flipAddr] ^= 0xFF // 故意不推进版本号
	m.fakeMemory.mu.Unlock()
	return n, err
}

// regressMemory 的 Version 单调倒退。
type regressMemory struct {
	*fakeMemory
	calls uint64
}

func (m *regressMemory) Version() uint64 {
	m.calls++
	return 1000 - m.calls
}

const testRingBase = 0x40000

// newTestRig 构造注册表（[0,0x10000) 可读写，[0x2000,0x3000) 保留）与内存。
func newTestRig() (*fakeMemory, *Registry, *Freezer) {
	mem := newFakeMemory(1 << 20)
	reg := NewRegistry()
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	must(reg.RegisterDMA(0, 0x10000, PermRead|PermWrite))
	must(reg.Reserve(0x2000, 0x1000))
	return mem, reg, NewFreezer(mem, reg)
}

// writeRing 把 descs 写进环；descs 以索引为键。
func writeRing(mem *fakeMemory, descs map[uint16]Descriptor) {
	for idx, d := range descs {
		mem.writeDesc(testRingBase, idx, d)
	}
}

func mustFreeze(t *testing.T, f *Freezer, head uint16) *FrozenChain {
	t.Helper()
	c, err := f.Freeze(testRingBase, head)
	if err != nil {
		t.Fatalf("Freeze failed: %v", err)
	}
	return c
}

func expectErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("expected error %v, got %v", target, err)
	}
}
