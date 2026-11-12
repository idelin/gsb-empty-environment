package netsec

import "fmt"

// Memory 是解释器使用的简单字节可寻址内存，并负责底层越界判定。
// 键为绝对字节地址。检测回调在访问前给出安全策略，Memory 只负责存取。
type Memory struct {
	bytes map[int64]byte
}

// NewMemory 构造空内存。
func NewMemory() *Memory {
	return &Memory{bytes: make(map[int64]byte)}
}

// Clone 深拷贝内存，保证对照运行互不干扰。
func (m *Memory) Clone() *Memory {
	cp := make(map[int64]byte, len(m.bytes))
	for k, v := range m.bytes {
		cp[k] = v
	}
	return &Memory{bytes: cp}
}

// Snapshot 返回当前字节映射的副本。
func (m *Memory) Snapshot() map[int64]byte {
	cp := make(map[int64]byte, len(m.bytes))
	for k, v := range m.bytes {
		cp[k] = v
	}
	return cp
}

// Set 预置一个字节（测试夹具用）。
func (m *Memory) Set(addr int64, v byte) { m.bytes[addr] = v }

// Get 读取一个字节，未初始化读为 0。
func (m *Memory) Get(addr int64) byte { return m.bytes[addr] }

// Read 以小端序读取 size 字节；size 仅支持 1/2/4/8。
func (m *Memory) Read(addr int64, size int) (int64, error) {
	if !validSize(size) {
		return 0, fmt.Errorf("netsec: bad read size %d", size)
	}
	var v int64
	for i := 0; i < size; i++ {
		v |= int64(m.bytes[addr+int64(i)]) << (8 * i)
	}
	return v, nil
}

// Write 以小端序写入 size 字节；size 仅支持 1/2/4/8。
func (m *Memory) Write(addr int64, size int, val int64) error {
	if !validSize(size) {
		return fmt.Errorf("netsec: bad write size %d", size)
	}
	for i := 0; i < size; i++ {
		m.bytes[addr+int64(i)] = byte(val >> (8 * i))
	}
	return nil
}

func validSize(size int) bool {
	return size == 1 || size == 2 || size == 4 || size == 8
}

// RangeChecker 返回一个只允许访问 [lo, hi) 的纯检测回调，越界即拒绝。
func RangeChecker(lo, hi int64) CheckerFunc {
	return func(a Access) error {
		if a.Addr < lo || a.Addr+int64(a.Size) > hi {
			return fmt.Errorf("netsec: %s out of bounds: addr=%d size=%d range=[%d,%d)",
				a.Kind, a.Addr, a.Size, lo, hi)
		}
		return nil
	}
}
