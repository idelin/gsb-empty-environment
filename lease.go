package leaseread

import (
	"context"
	"sync"
	"time"
)

// LeaseManager 维护主节点租约。
//
// 线性一致性安全论证：租约过期时间由多数派从节点的心跳确认共同背书，
// 各节点时钟最大漂移不超过 maxClockDrift（安全余量）。只要本地时钟
// 距离租约过期仍大于安全余量，就可以确定多数派承诺的"不选新主"窗口
// 仍然有效，期间不会有其他节点接受更新的写，因此本地读是线性一致的。
type LeaseManager struct {
	mu     sync.Mutex
	expiry time.Time // 租约过期时刻（本地时钟）
	safety time.Duration
	now    func() time.Time
}

func NewLeaseManager(safetyMargin time.Duration) *LeaseManager {
	return &LeaseManager{
		safety: safetyMargin,
		now:    time.Now,
	}
}

// Grant 在获得多数派心跳确认后授予/续期租约。
func (lm *LeaseManager) Grant(d time.Duration) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lm.expiry = lm.now().Add(d)
}

// Remaining 返回扣除安全余量后的"绝对安全"剩余租约时间。
func (lm *LeaseManager) Remaining() time.Duration {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return lm.expiry.Add(-lm.safety).Sub(lm.now())
}

// InSafeWindow 报告当前是否处于绝对安全窗口：
// 剩余安全租约时间 >= degradeThreshold（规范要求 100ms）。
func (lm *LeaseManager) InSafeWindow(degradeThreshold time.Duration) bool {
	return lm.Remaining() >= degradeThreshold
}

// Renew 向所有从节点发起心跳，获得多数派确认后续期租约。
// 这是租约路径上唯一的网络开销，且按租约周期摊销，而非按读请求摊销。
func (lm *LeaseManager) Renew(ctx context.Context, t Transport, selfID int, peers []int, leaseDuration time.Duration) error {
	acks := 1 // 自己一票
	majority := (len(peers)+1)/2 + 1

	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, id := range peers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if err := t.Heartbeat(ctx, id); err == nil {
				mu.Lock()
				acks++
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()
	if acks < majority {
		return ErrNoQuorum
	}
	lm.Grant(leaseDuration)
	return nil
}
