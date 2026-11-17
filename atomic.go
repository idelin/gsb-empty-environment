package netsec

import "sync/atomic"

// atomicBool 是 atomic.Bool 的薄封装，让 Handle 的释放状态保持内聚，
// 避免业务结构里散落原子类型的细节。
type atomicBool struct{ flag atomic.Bool }

func (b *atomicBool) Load() bool { return b.flag.Load() }

func (b *atomicBool) CompareAndSwap(old, new bool) bool {
	return b.flag.CompareAndSwap(old, new)
}
