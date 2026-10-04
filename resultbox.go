// Package resultbox 提供“结果盒”：后台计算把结果放进盒子，
// 众多领取者中只有一个能取走结果，其余得到“已领取”状态。
//
// 语义约定：
//   - 结果至多被领取一次；领取成功后，结果的清理责任随结果转移给领取者。
//   - 领取超时（context 取消/超时）不消费结果，结果仍留在盒中。
//   - Close 关闭盒子：若结果仍在盒中（未被领取），由盒子负责释放；
//     若结果已被领取，Close 不再触碰它（责任已转移）。
//   - Close 与 Claim 竞争时，二者必有且仅有一方获得清理责任，
//     保证既不泄漏也不双重释放。
package resultbox

import (
	"context"
	"errors"
	"sync"
)

var (
	// ErrClosed 表示盒子已关闭。
	ErrClosed = errors.New("resultbox: box is closed")
	// ErrClaimed 表示结果已被其他领取者取走。
	ErrClaimed = errors.New("resultbox: result already claimed")
	// ErrFilled 表示盒子已有结果，重复放入被拒绝（放入的值会被释放以防泄漏）。
	ErrFilled = errors.New("resultbox: box already holds a result")
)

// Box 是结果盒。零值不可用，请用 New 构造。
//
// release 是清理函数：当结果为盒子的责任时（关闭时仍未被领取、
// 或 Put 被拒绝），盒子通过它释放结果。领取成功后盒子永不再调用它。
type Box[T any] struct {
	mu      sync.Mutex
	changed chan struct{} // 每次状态变化后关闭并重建，用于唤醒等待者
	release func(T)

	result  T
	filled  bool // 盒中是否有结果
	claimed bool // 结果是否已被领取
	closed  bool
}

// New 创建一个结果盒。release 可为 nil（表示结果无需清理）。
func New[T any](release func(T)) *Box[T] {
	return &Box[T]{
		changed: make(chan struct{}),
		release: release,
	}
}

// broadcast 唤醒所有等待者。调用时必须持有 b.mu。
func (b *Box[T]) broadcast() {
	close(b.changed)
	b.changed = make(chan struct{})
}

// Put 把后台计算的结果放入盒子。
//
// 若盒子已关闭或已有结果，Put 拒绝接收并立即用 release 释放该值
// （此时盒子是它的唯一持有者，不释放就会泄漏），返回相应错误。
func (b *Box[T]) Put(v T) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		b.releaseValue(v)
		return ErrClosed
	}
	if b.filled {
		b.mu.Unlock()
		b.releaseValue(v)
		return ErrFilled
	}
	b.result = v
	b.filled = true
	b.broadcast()
	b.mu.Unlock()
	return nil
}

// Claim 尝试领取结果，阻塞直到以下任一情形：
//
//   - 领取成功：返回结果，清理责任随之转移给调用方，盒子永不再释放它；
//   - 结果已被他人领取：返回 ErrClaimed；
//   - 盒子已关闭且结果不在盒中：返回 ErrClosed；
//   - ctx 超时/取消：返回 ctx.Err()，结果不被消费，仍留在盒中。
func (b *Box[T]) Claim(ctx context.Context) (T, error) {
	var zero T

	b.mu.Lock()
	for {
		if b.filled && !b.claimed {
			// 领取成功：取出结果，责任转移给调用方。
			b.claimed = true
			v := b.result
			b.result = zero
			b.broadcast()
			b.mu.Unlock()
			return v, nil
		}
		if b.claimed {
			b.mu.Unlock()
			return zero, ErrClaimed
		}
		if b.closed {
			b.mu.Unlock()
			return zero, ErrClosed
		}
		ch := b.changed
		b.mu.Unlock()

		select {
		case <-ch:
		case <-ctx.Done():
			return zero, ctx.Err()
		}
		b.mu.Lock()
	}
}

// Close 关闭盒子。若结果仍在盒中未被领取，盒子负责释放它；
// 若结果已被领取，清理责任已转移，Close 不做任何释放。
// Close 可重复调用，后续调用为空操作。
func (b *Box[T]) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	var v T
	pending := false
	if b.filled && !b.claimed {
		v = b.result
		b.result = *new(T)
		b.filled = false
		pending = true
	}
	b.broadcast()
	b.mu.Unlock()

	if pending {
		b.releaseValue(v)
	}
}

// releaseValue 在锁外调用清理函数，避免清理函数回调盒子造成死锁。
func (b *Box[T]) releaseValue(v T) {
	if b.release != nil {
		b.release(v)
	}
}
