// Package quota implements a distributed sliding-window rate-limit
// quota system with heartbeat leases and batch quota pre-allocation.
//
// A central Master owns the global QPS pool and leases quota slices to
// Workers. Workers consume their local tokens without any network RPC;
// when the local balance drops below a low watermark they asynchronously
// send a batched heartbeat to the Master to renew. The Master reclaims
// unused quota from idle workers and reassigns it to busy ones.
package quota

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// ErrUnknownWorker is returned when a heartbeat arrives from a worker
// that never registered with the Master.
var ErrUnknownWorker = errors.New("quota: unknown worker")

type lease struct {
	granted   int64     // total tokens leased for the current window
	used      int64     // tokens the worker reported consumed this window
	expiresAt time.Time // lease TTL; expired leases return to the pool
}

// Master is the central coordinator. It owns the global token pool and
// hands out time-boxed leases to workers. It is safe for concurrent use.
type Master struct {
	mu         sync.Mutex
	total      int64 // global pool size, e.g. 3000 QPS
	workers    int   // expected worker count, used for the initial fair split
	window     time.Duration
	leases     map[string]*lease
	heartbeats int64 // total heartbeats served (observability)
	now        func() time.Time
}

// NewMaster creates a coordinator owning total tokens per window, split
// fairly across workers nodes at registration time. window is the lease
// TTL: leases not renewed within window expire and return to the pool.
func NewMaster(total int64, workers int, window time.Duration) *Master {
	return &Master{
		total:   total,
		workers: workers,
		window:  window,
		leases:  make(map[string]*lease),
		now:     time.Now,
	}
}

// SetClock overrides the time source. Intended for tests.
func (m *Master) SetClock(now func() time.Time) { m.now = now }

// freeLocked returns the tokens currently not leased to anyone.
// Callers must hold m.mu.
func (m *Master) freeLocked() int64 {
	var leased int64
	for _, l := range m.leases {
		leased += l.granted
	}
	return m.total - leased
}

// sweepLocked drops expired leases, returning their tokens to the pool.
// Callers must hold m.mu.
func (m *Master) sweepLocked() {
	now := m.now()
	for id, l := range m.leases {
		if !now.Before(l.expiresAt) {
			delete(m.leases, id)
		}
	}
}

// Register assigns the worker its initial fair share of the pool.
func (m *Master) Register(workerID string) (HeartbeatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked()
	if _, ok := m.leases[workerID]; ok {
		return HeartbeatResponse{}, errors.New("quota: worker already registered")
	}
	share := m.total / int64(m.workers)
	if free := m.freeLocked(); share > free {
		share = free
	}
	l := &lease{granted: share, expiresAt: m.now().Add(m.window)}
	m.leases[workerID] = l
	return HeartbeatResponse{Additional: share, Remaining: share, ExpiresAt: l.expiresAt}, nil
}

// reclaimLocked takes back unused tokens from other workers, most-idle
// first, and returns how many tokens were recovered. Only the unused
// portion (granted - used) of a lease is ever reclaimed, so an active
// worker never loses tokens it has already spent against.
// Callers must hold m.mu.
func (m *Master) reclaimLocked(requester string, need int64) int64 {
	type idle struct {
		id          string
		used        int64
		reclaimable int64
	}
	var idles []idle
	for id, l := range m.leases {
		if id == requester {
			continue
		}
		if r := l.granted - l.used; r > 0 {
			idles = append(idles, idle{id: id, used: l.used, reclaimable: r})
		}
	}
	// Least-used (most idle) first; ties broken by largest unused balance.
	sort.Slice(idles, func(i, j int) bool {
		if idles[i].used != idles[j].used {
			return idles[i].used < idles[j].used
		}
		return idles[i].reclaimable > idles[j].reclaimable
	})
	var recovered int64
	for _, it := range idles {
		if recovered >= need {
			break
		}
		take := it.reclaimable
		if remain := need - recovered; take > remain {
			take = remain
		}
		m.leases[it.id].granted -= take
		recovered += take
	}
	return recovered
}

// Heartbeat processes a batched renewal from a worker. The worker reports
// the tokens it consumed since its last heartbeat and asks for Want more.
// The Master first uses the free pool; if that is not enough it reclaims
// unused quota from idle peers. The response carries only the *additional*
// tokens granted, so in-flight local consumption on the worker can never
// double-spend.
func (m *Master) Heartbeat(req HeartbeatRequest) (HeartbeatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.heartbeats++
	m.sweepLocked()
	l, ok := m.leases[req.WorkerID]
	if !ok {
		return HeartbeatResponse{}, ErrUnknownWorker
	}
	l.used += req.Used
	l.expiresAt = m.now().Add(m.window)

	free := m.freeLocked()
	if free < req.Want {
		free += m.reclaimLocked(req.WorkerID, req.Want-free)
	}
	add := req.Want
	if add > free {
		add = free
	}
	if add < 0 {
		add = 0
	}
	l.granted += add
	return HeartbeatResponse{Additional: add, Remaining: l.granted - l.used, ExpiresAt: l.expiresAt}, nil
}

// LeaseInfo is a point-in-time view of one worker's lease.
type LeaseInfo struct {
	WorkerID  string
	Granted   int64
	Used      int64
	ExpiresAt time.Time
}

// Snapshot returns the current leases, sorted by worker ID.
func (m *Master) Snapshot() []LeaseInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked()
	out := make([]LeaseInfo, 0, len(m.leases))
	for id, l := range m.leases {
		out = append(out, LeaseInfo{WorkerID: id, Granted: l.granted, Used: l.used, ExpiresAt: l.expiresAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WorkerID < out[j].WorkerID })
	return out
}

// Stats reports the total number of heartbeats the Master has served.
func (m *Master) Stats() (heartbeats int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.heartbeats
}
