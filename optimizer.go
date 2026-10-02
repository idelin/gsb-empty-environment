package leaseread

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var ErrNotFound = errors.New("leaseread: key not found")

// DefaultDegradeThreshold 规范要求：租约剩余不足 100ms 时降级。
const DefaultDegradeThreshold = 100 * time.Millisecond

// ReadOptimizer 是基于租约的强一致读优化器，运行在主节点上。
//
// 快路径：租约处于绝对安全窗口内 -> 零网络开销，直接本地读。
// 慢路径：租约剩余不足阈值 -> 自动降级为多数派网络确认读。
type ReadOptimizer struct {
	selfID   int
	primary  *Node
	peers    []int
	majority int

	transport *SimTransport
	lease     *LeaseManager

	DegradeThreshold time.Duration

	LocalReads  atomic.Int64 // 命中租约快路径的读次数
	QuorumReads atomic.Int64 // 降级为网络确认的读次数
}

func NewReadOptimizer(selfID int, primary *Node, peers []int, t *SimTransport, lm *LeaseManager) *ReadOptimizer {
	n := len(peers) + 1
	return &ReadOptimizer{
		selfID:           selfID,
		primary:          primary,
		peers:            peers,
		majority:         n/2 + 1,
		transport:        t,
		lease:            lm,
		DegradeThreshold: DefaultDegradeThreshold,
	}
}

// Write 主节点写路径：本地应用后同步复制到多数派（写路径不走租约优化）。
func (ro *ReadOptimizer) Write(ctx context.Context, key, value string, version uint64) error {
	v := VersionedValue{Value: value, Version: version}
	ro.primary.Store.Put(key, v)

	acks := 1
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, id := range ro.peers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if err := ro.transport.Replicate(ctx, id, key, v); err == nil {
				mu.Lock()
				acks++
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()
	if acks < ro.majority {
		return ErrNoQuorum
	}
	return nil
}

// Read 是优化后的读入口：租约安全窗口内零网络本地读，否则降级多数派确认。
func (ro *ReadOptimizer) Read(ctx context.Context, key string) (VersionedValue, error) {
	if ro.lease.InSafeWindow(ro.DegradeThreshold) {
		ro.LocalReads.Add(1)
		v, ok := ro.primary.Store.Get(key)
		if !ok {
			return VersionedValue{}, ErrNotFound
		}
		return v, nil
	}
	ro.QuorumReads.Add(1)
	return ro.quorumRead(ctx, key)
}

// quorumRead 降级路径：向多数派副本发起网络确认，取最高版本，保证线性一致。
func (ro *ReadOptimizer) quorumRead(ctx context.Context, key string) (VersionedValue, error) {
	type result struct {
		v     VersionedValue
		found bool
	}
	resCh := make(chan result, len(ro.peers)+1)

	if v, ok := ro.primary.Store.Get(key); ok {
		resCh <- result{v, true}
	} else {
		resCh <- result{}
	}
	for _, id := range ro.peers {
		go func(id int) {
			v, found, err := ro.transport.Fetch(ctx, id, key)
			if err != nil {
				resCh <- result{}
				return
			}
			resCh <- result{v, found}
		}(id)
	}

	var best VersionedValue
	found := false
	acks := 0
	for acks < ro.majority {
		r := <-resCh
		acks++
		if r.found && (!found || r.v.Version > best.Version) {
			best, found = r.v, true
		}
	}
	if !found {
		return VersionedValue{}, ErrNotFound
	}
	return best, nil
}
