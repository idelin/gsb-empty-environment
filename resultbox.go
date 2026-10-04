// Package resultbox 提供“结果盒”：后台计算把结果放进盒子，
// 众多领取者中只有一个能领到结果，其余得到 ErrClaimed。
// 领取超时不会消费结果；领取成功后，结果的清理责任随结果一起
// 转移给领取者。盒子可关闭：关闭时若结果仍无人领取，由盒子
// 负责释放，保证既不泄漏也不双重释放。
package resultbox

import (
	"errors"
	"sync"
	"time"
)

var (
	// ErrTimeout 表示领取超时，结果仍留在盒子里，可再次领取。
	ErrTimeout = errors.New("resultbox: claim timed out")
	// ErrClaimed 表示结果已被其他领取者领走。
	ErrClaimed = errors.New("resultbox: result already claimed")
	// ErrClosed 表示盒子已关闭且没有可领取的结果。
	ErrClosed = errors.New("resultbox: box is closed")
)

// Releaser 是可转移清理责任的结果对象。无论结果最终归谁，
// Release 必须恰好被调用一次：被领走时由领取者调用，
// 未被领走时由盒子（Close 或关闭后的 Put）调用。
type Releaser interface {
	Release()
}

// Box 是一次性结果盒：最多放入一个结果，最多被一个领取者领走。
// 零值不可用，请用 New 构造。
type Box struct {
	mu       sync.Mutex
	result   Releaser // 非 nil 表示盒子里躺着一份尚未被领走的结果
	claimed  bool     // 结果已被某个领取者领走
	closed   bool
	readyCh  chan struct{} // Put 成功时关闭
	closedCh chan struct{} // Close 时关闭
}

// New 返回一个空的结果盒。
func New() *Box {
	return &Box{
		readyCh:  make(chan struct{}),
		closedCh: make(chan struct{}),
	}
}

// Put 把后台计算的结果放入盒子，结果的所有权随之转移给盒子。
// 若盒子已关闭，Put 会立即释放结果，保证不泄漏。
// 重复 Put 会 panic。
func (b *Box) Put(r Releaser) {
	b.mu.Lock()
	if b.result != nil || b.claimed {
		b.mu.Unlock()
		panic("resultbox: result delivered twice")
	}
	if b.closed {
		b.mu.Unlock()
		r.Release()
		return
	}
	b.result = r
	close(b.readyCh)
	b.mu.Unlock()
}

// Claim 尝试领取结果，最多等待 timeout。
//
//   - 成功：返回结果，清理责任（调用 Release）转移给调用方；
//   - 已被领走：返回 ErrClaimed；
//   - 超时：返回 ErrTimeout，结果不被消费，之后仍可领取；
//   - 盒子关闭且无结果可领：返回 ErrClosed。
func (b *Box) Claim(timeout time.Duration) (Releaser, error) {
	b.mu.Lock()
	if r, ok := b.takeLocked(); ok {
		b.mu.Unlock()
		return r, nil
	}
	if err := b.stateLocked(); err != nil {
		b.mu.Unlock()
		return nil, err
	}
	b.mu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-b.readyCh:
			b.mu.Lock()
			if r, ok := b.takeLocked(); ok {
				b.mu.Unlock()
				return r, nil
			}
			err := b.stateLocked()
			b.mu.Unlock()
			// readyCh 已关闭但结果没了：被别的领取者抢先
			// （ErrClaimed），或被并发的 Close 收走（ErrClosed）。
			if err != nil {
				return nil, err
			}
			return nil, ErrClaimed
		case <-b.closedCh:
			// Close 与 Put 可能赛跑，最后再检查一次。
			b.mu.Lock()
			if r, ok := b.takeLocked(); ok {
				b.mu.Unlock()
				return r, nil
			}
			err := b.stateLocked()
			b.mu.Unlock()
			if err != nil {
				return nil, err
			}
			return nil, ErrClosed
		case <-timer.C:
			// 超时不触碰任何状态，结果留在盒子里。
			return nil, ErrTimeout
		}
	}
}

// Close 关闭盒子。若结果仍在盒中无人领取，Close 负责释放它；
// 若结果已被领走，清理责任已随结果转移，Close 不做任何释放。
// 重复 Close 是安全的，返回是否本次调用真正执行了关闭。
func (b *Box) Close() bool {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return false
	}
	b.closed = true
	close(b.closedCh)
	r := b.result
	b.result = nil
	b.mu.Unlock()
	if r != nil {
		r.Release()
	}
	return true
}

// takeLocked 在持锁状态下尝试取走结果。
func (b *Box) takeLocked() (Releaser, bool) {
	if b.result == nil {
		return nil, false
	}
	r := b.result
	b.result = nil
	b.claimed = true
	return r, true
}

// stateLocked 在持锁状态下返回当前应有的错误，nil 表示应继续等待。
func (b *Box) stateLocked() error {
	if b.claimed {
		return ErrClaimed
	}
	if b.closed {
		return ErrClosed
	}
	return nil
}
