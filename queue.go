package main

import "sync"

// DropPolicy decides what happens when Enqueue finds the queue full.
type DropPolicy int

const (
	// PolicyRejectNew drops the incoming event.
	PolicyRejectNew DropPolicy = iota
	// PolicyDropOldest evicts the globally oldest queued event to make room.
	PolicyDropOldest
)

// ShutdownMode selects how Close treats the queued backlog.
type ShutdownMode int

const (
	// Drain processes every queued event before workers exit.
	Drain ShutdownMode = iota
	// StopNow discards the queued backlog (counted as dropped with reason
	// "shutdown") and only lets in-flight handlers finish.
	StopNow
)

// DefaultHighBurst is the maximum number of consecutive high-priority picks
// per tenant before a normal-priority event is forced through, so a sustained
// high-priority stream can never fully starve the normal stream.
const DefaultHighBurst = 3

type queuedEvent struct {
	ev  Event
	seq uint64
}

// lane holds one tenant's events, split by priority.
type lane struct {
	high       []queuedEvent
	normal     []queuedEvent
	highStreak int
}

func (l *lane) len() int { return len(l.high) + len(l.normal) }

// pop picks the next event for the tenant: high priority first, but after
// burst consecutive high picks a normal event is forced if one is waiting.
func (l *lane) pop(burst int) queuedEvent {
	if len(l.high) > 0 && (len(l.normal) == 0 || l.highStreak < burst) {
		ev := l.high[0]
		l.high = l.high[1:]
		l.highStreak++
		return ev
	}
	ev := l.normal[0]
	l.normal = l.normal[1:]
	l.highStreak = 0
	return ev
}

// headSeq returns the arrival sequence of the lane's oldest event.
func (l *lane) headSeq() (uint64, bool) {
	switch {
	case len(l.high) > 0 && len(l.normal) > 0:
		if l.high[0].seq < l.normal[0].seq {
			return l.high[0].seq, true
		}
		return l.normal[0].seq, true
	case len(l.high) > 0:
		return l.high[0].seq, true
	case len(l.normal) > 0:
		return l.normal[0].seq, true
	}
	return 0, false
}

// evictHead removes the lane's oldest event.
func (l *lane) evictHead() queuedEvent {
	if len(l.high) > 0 && (len(l.normal) == 0 || l.high[0].seq < l.normal[0].seq) {
		ev := l.high[0]
		l.high = l.high[1:]
		return ev
	}
	ev := l.normal[0]
	l.normal = l.normal[1:]
	return ev
}

// Queue is a bounded, multi-tenant, priority-aware receive queue.
type Queue struct {
	mu        sync.Mutex
	cond      *sync.Cond
	capacity  int
	highBurst int
	policy    DropPolicy
	size      int
	seq       uint64
	lanes     map[string]*lane
	order     []string // tenants in round-robin order
	rr        int
	closed    bool
	stats     *stats
}

func NewQueue(capacity, highBurst int, policy DropPolicy) *Queue {
	if highBurst < 1 {
		highBurst = DefaultHighBurst
	}
	q := &Queue{
		capacity:  capacity,
		highBurst: highBurst,
		policy:    policy,
		lanes:     make(map[string]*lane),
		stats:     newStats(),
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Enqueue records an arrival. Every arrival is counted as received exactly
// once and will later be counted as either processed or dropped, never both.
func (q *Queue) Enqueue(ev Event) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.stats.received++
	q.tenantStatsLocked(ev.Tenant).Received++

	if q.closed {
		q.dropLocked(ev.Tenant, ReasonShutdown)
		return
	}

	qe := queuedEvent{ev: ev, seq: q.seq}
	q.seq++

	if q.size >= q.capacity {
		if q.policy == PolicyDropOldest && q.size > 0 {
			old := q.evictOldestLocked()
			q.dropLocked(old.ev.Tenant, ReasonQueueFull)
		} else {
			q.dropLocked(ev.Tenant, ReasonQueueFull)
			return
		}
	}

	l := q.laneLocked(ev.Tenant)
	if ev.Priority == PriorityHigh {
		l.high = append(l.high, qe)
	} else {
		l.normal = append(l.normal, qe)
	}
	q.size++
	q.cond.Signal()
}

// Next blocks until an event is schedulable or the queue is closed and empty.
func (q *Queue) Next() (Event, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for {
		if qe, ok := q.pickLocked(); ok {
			q.size--
			return qe.ev, true
		}
		if q.closed {
			return Event{}, false
		}
		q.cond.Wait()
	}
}

// MarkProcessed records that a dequeued event finished handling.
func (q *Queue) MarkProcessed(ev Event) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.stats.processed++
	q.tenantStatsLocked(ev.Tenant).Processed++
}

// Close stops the queue. Drain lets workers empty the backlog; StopNow
// discards the backlog immediately, counting every discarded event as
// dropped with reason "shutdown".
func (q *Queue) Close(mode ShutdownMode) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	if mode == StopNow {
		for _, tenant := range q.order {
			l := q.lanes[tenant]
			n := uint64(l.len())
			if n == 0 {
				continue
			}
			l.high = nil
			l.normal = nil
			q.size -= int(n)
			q.stats.dropped += n
			q.stats.dropsByReason[ReasonShutdown] += n
			q.tenantStatsLocked(tenant).Dropped += n
		}
	}
	q.cond.Broadcast()
}

// Snapshot returns a consistent copy of the counters.
func (q *Queue) Snapshot() Snapshot {
	q.mu.Lock()
	defer q.mu.Unlock()
	snap := Snapshot{
		Received:      q.stats.received,
		Processed:     q.stats.processed,
		Dropped:       q.stats.dropped,
		DropsByReason: make(map[DropReason]uint64, len(q.stats.dropsByReason)),
		Tenants:       make(map[string]TenantStats, len(q.stats.tenants)),
	}
	for reason, n := range q.stats.dropsByReason {
		snap.DropsByReason[reason] = n
	}
	for tenant, ts := range q.stats.tenants {
		snap.Tenants[tenant] = *ts
	}
	return snap
}

// pickLocked round-robins across tenants, then applies the per-tenant
// priority burst rule.
func (q *Queue) pickLocked() (queuedEvent, bool) {
	n := len(q.order)
	for i := 0; i < n; i++ {
		idx := (q.rr + i) % n
		l := q.lanes[q.order[idx]]
		if l.len() == 0 {
			continue
		}
		q.rr = (idx + 1) % n
		return l.pop(q.highBurst), true
	}
	return queuedEvent{}, false
}

func (q *Queue) laneLocked(tenant string) *lane {
	l, ok := q.lanes[tenant]
	if !ok {
		l = &lane{}
		q.lanes[tenant] = l
		q.order = append(q.order, tenant)
	}
	return l
}

func (q *Queue) tenantStatsLocked(tenant string) *TenantStats {
	ts, ok := q.stats.tenants[tenant]
	if !ok {
		ts = &TenantStats{}
		q.stats.tenants[tenant] = ts
	}
	return ts
}

func (q *Queue) dropLocked(tenant string, reason DropReason) {
	q.stats.dropped++
	q.stats.dropsByReason[reason]++
	q.tenantStatsLocked(tenant).Dropped++
}

// evictOldestLocked removes the globally oldest queued event.
func (q *Queue) evictOldestLocked() queuedEvent {
	best := ""
	var bestSeq uint64
	for _, tenant := range q.order {
		if s, ok := q.lanes[tenant].headSeq(); ok && (best == "" || s < bestSeq) {
			best, bestSeq = tenant, s
		}
	}
	ev := q.lanes[best].evictHead()
	q.size--
	return ev
}
