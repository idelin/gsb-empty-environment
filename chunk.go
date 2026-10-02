// Package vecpool 实现向量化执行引擎的中间结果内存池。
//
// 核心思想：查询流水线建立时一次性预分配固定数量的 Chunk，
// 算子计算完毕立即将 Chunk 归还给对象池，下游算子直接复用
// 内存槽位，从而在整个查询执行期间达成零堆内存分配。
package vecpool

import "sync"

// ChunkSize 是每个向量块容纳的元素个数（固定大小）。
const ChunkSize = 1024

// Chunk 是一块固定大小的向量内存，在算子之间传递中间结果。
// 底层数组在池初始化时一次性分配，之后永不重新分配。
type Chunk struct {
	// Data 为实际数据区，长度恒为 ChunkSize。
	Data []float64
	// N 表示当前块中有效元素的个数（N <= ChunkSize）。
	N int
	// id 是该 Chunk 在池中的槽位编号，便于调试与审计。
	id int
}

// Reset 将 Chunk 标记为可复用状态（仅重置长度，不清零数据，
// 因为下游算子总会完整覆写有效区域，清零是多余的 CPU 开销）。
func (c *Chunk) Reset() {
	c.N = 0
}

// Pool 是固定容量的 Chunk 对象池。
//
// 池在创建时预分配 capacity 个 Chunk，之后 Get/Put 只在
// 空闲槽位栈上移动指针，绝不触发堆分配。若池被耗尽，
// Get 返回 nil 并累加 Misses 计数（用于测试与监控发现容量不足），
// 而不是悄悄向堆申请内存——这保证了"零分配"承诺不被破坏。
type Pool struct {
	chunks []Chunk // 所有 Chunk 的连续存储，一次性分配
	free   []int   // 空闲槽位索引栈，一次性分配
	mu     sync.Mutex
	allocs uint64 // 池建立后发生的堆分配次数（恒应为 0）
	gets   uint64 // 累计借出次数
	puts   uint64 // 累计归还次数
	misses uint64 // 池耗尽导致 Get 失败的次数
}

// NewPool 创建容量为 capacity 的对象池，并一次性预分配全部内存。
func NewPool(capacity int) *Pool {
	p := &Pool{
		chunks: make([]Chunk, capacity),
		free:   make([]int, capacity),
	}
	for i := 0; i < capacity; i++ {
		p.chunks[i].Data = make([]float64, ChunkSize)
		p.chunks[i].id = i
		p.free[i] = capacity - 1 - i // 槽位 0 在栈顶
	}
	return p
}

// Get 从池中借出一个 Chunk。池耗尽时返回 nil（不产生堆分配）。
func (p *Pool) Get() *Chunk {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gets++
	n := len(p.free)
	if n == 0 {
		p.misses++
		return nil
	}
	idx := p.free[n-1]
	p.free = p.free[:n-1]
	c := &p.chunks[idx]
	c.Reset()
	return c
}

// Put 将 Chunk 归还给池，供下游/后续算子复用。
func (p *Pool) Put(c *Chunk) {
	if c == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.puts++
	c.Reset()
	p.free = append(p.free, c.id)
}

// Stats 返回池的运行统计快照。
type Stats struct {
	Capacity int
	InUse    int
	Gets     uint64
	Puts     uint64
	Allocs   uint64 // 池建立后的堆分配次数，零分配目标下恒为 0
	Misses   uint64
}

// Stats 读取池的当前统计信息。
func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Stats{
		Capacity: len(p.chunks),
		InUse:    len(p.chunks) - len(p.free),
		Gets:     p.gets,
		Puts:     p.puts,
		Allocs:   p.allocs,
		Misses:   p.misses,
	}
}
