package netsec

import (
	"context"
	"sync"
)

// Counter 抽象由可信环境（例如 TPM 单调计数器）提供的单调计数器。
//
// 安全约定：
//   - Increment 成功提交后，计数在整个生命周期内只能增大，不能回退；
//   - 计数器独立于可克隆的普通磁盘存储，节点磁盘被快照回滚或整体克隆，
//     都不会改变计数器的已提交值，也无法借此恢复或增加签发额度；
//   - 计数器不可用时 Increment/Value 必须返回错误，宿主据此拒绝服务，
//     绝不能在计数器缺失的情况下自行放行签发。
type Counter interface {
	// Value 返回当前已提交的计数值。
	Value(ctx context.Context) (uint64, error)

	// Increment 原子地将计数加一并返回新值。
	// 返回的新值必须严格大于此前任何一次成功提交的值。
	Increment(ctx context.Context) (uint64, error)
}

// MemoryCounter 是可信单调计数器的参考实现：状态保存在内存中，
// 模拟独立于可克隆磁盘之外的受信硬件计数器。它没有任何回退接口，
// 真实部署中单调值由 TPM 之类的可信硬件在重启后继续维持。
type MemoryCounter struct {
	mu    sync.Mutex
	value uint64
}

// NewMemoryCounter 创建初始值为 0 的内存单调计数器。
func NewMemoryCounter() *MemoryCounter {
	return &MemoryCounter{}
}

// Value 返回当前计数值。
func (c *MemoryCounter) Value(ctx context.Context) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value, nil
}

// Increment 原子地把计数加一并返回新值，已提交的值不可回退。
func (c *MemoryCounter) Increment(ctx context.Context) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value++
	return c.value, nil
}
