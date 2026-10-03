// Package quota implements a distributed sliding-window rate limiter with
// heartbeat-lease quota pre-allocation.
//
// A central Master owns the global QPS pool and leases per-window token
// grants to Workers. Workers consume tokens locally without any RPC; only
// when the local budget runs low do they send a batched asynchronous
// heartbeat to renew. The Master rebalances the pool on every heartbeat by
// reclaiming idle quota from workers that are not using their share.
package quota

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Lease is the per-window quota a worker is allowed to consume locally.
type Lease struct {
	WorkerID string
	Grant    int64 // tokens allocated for the current window
	Used     int64 // tokens reported consumed in the current window
}

// HeartbeatRequest is sent by a worker to renew / top up its lease.
type HeartbeatRequest struct {
	WorkerID string
	Used     int64 // tokens consumed since the previous heartbeat
	Want     int64 // additional tokens requested for this window
}

// HeartbeatResponse carries the rebalanced grant back to the worker.
type HeartbeatResponse struct {
	Grant int64 // new total grant for the current window
}

// Master is the central coordinator that owns the global token pool.
type Master struct {
	mu          sync.Mutex
	total       int64
	window      time.Duration
	leases      map[string]*Lease
	windowStart time.Time

	heartbeats int64 // total RPCs served, exposed for tests/metrics
}

// NewMaster creates a coordinator for a pool of totalQPS tokens per window.
func NewMaster(totalQPS int64, window time.Duration) *Master {
	return &Master{
		total:       totalQPS,
		window:      window,
		leases:      make(map[string]*Lease),
		windowStart: time.Now(),
	}
}

// RegisterWorkers splits the pool evenly among the given workers.
func (m *Master) RegisterWorkers(ids ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	share := m.total / int64(len(ids))
	for _, id := range ids {
		m.leases[id] = &Lease{WorkerID: id, Grant: share}
	}
}

// NewWorker registers (if needed) and returns a Worker bound to this master.
func (m *Master) NewWorker(id string) *Worker {
	m.mu.Lock()
	l, ok := m.leases[id]
	m.mu.Unlock()
	if !ok {
		m.RegisterWorkers(id)
		m.mu.Lock()
		l = m.leases[id]
		m.mu.Unlock()
	}
	return newWorker(id, m, l.Grant)
}

// Heartbeat records consumption and rebalances the pool: idle quota is
// reclaimed from other workers and handed to the requester.
func (m *Master) Heartbeat(req HeartbeatRequest) HeartbeatResponse {
	atomic.AddInt64(&m.heartbeats, 1)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.rotateWindowLocked()

	lease := m.leases[req.WorkerID]
	lease.Used += req.Used

	if req.Want > 0 {
		m.rebalanceLocked(lease, req.Want)
	}
	return HeartbeatResponse{Grant: lease.Grant}
}

// rebalanceLocked moves idle tokens from other workers to the requester,
// taking from the most-idle workers first. The sum of all grants is
// invariant: tokens are only ever moved, never created.
func (m *Master) rebalanceLocked(requester *Lease, want int64) {
	type idleLease struct {
		lease *Lease
		idle  int64
	}
	var donors []idleLease
	for _, other := range m.leases {
		if other == requester {
			continue
		}
		if idle := other.Grant - other.Used; idle > 0 {
			donors = append(donors, idleLease{other, idle})
		}
	}
	sort.Slice(donors, func(i, j int) bool { return donors[i].idle > donors[j].idle })

	need := want
	for _, d := range donors {
		if need <= 0 {
			break
		}
		take := d.idle
		if take > need {
			take = need
		}
		d.lease.Grant -= take
		requester.Grant += take
		need -= take
	}
}

// rotateWindowLocked slides the window: consumption counters reset so a
// fresh window starts with the full grant available again.
func (m *Master) rotateWindowLocked() {
	if time.Since(m.windowStart) < m.window {
		return
	}
	m.windowStart = time.Now()
	for _, l := range m.leases {
		l.Used = 0
	}
}

// Grant returns the worker's current lease grant.
func (m *Master) Grant(id string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.leases[id].Grant
}

// TotalGrants returns the sum of all outstanding grants (invariant: == pool).
func (m *Master) TotalGrants() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var sum int64
	for _, l := range m.leases {
		sum += l.Grant
	}
	return sum
}

// HeartbeatCount returns how many heartbeat RPCs the master has served.
func (m *Master) HeartbeatCount() int64 {
	return atomic.LoadInt64(&m.heartbeats)
}
