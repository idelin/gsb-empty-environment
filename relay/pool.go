package relay

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrPoolExhausted is returned when no port is currently allocatable
	// (all ports are active or in quarantine).
	ErrPoolExhausted = errors.New("relay: no free ports")
	// ErrQuotaExceeded is returned when a tenant already holds its maximum
	// number of concurrent mappings.
	ErrQuotaExceeded = errors.New("relay: tenant quota exceeded")
)

type portState int

const (
	stateFree portState = iota
	stateActive
	stateQuarantine
)

type portSlot struct {
	state      portState
	generation uint64
	tenant     string
	remote     string
}

// Mapping is a handle to one allocated UDP backhaul port. The Generation
// distinguishes successive allocations of the same port: replies and close
// requests from an older generation are rejected.
type Mapping struct {
	Port       int
	Tenant     string
	Remote     string
	Generation uint64
}

// Config configures a Pool.
type Config struct {
	// Ports is the set of UDP ports the pool may hand out.
	Ports []int
	// Quarantine is the silent isolation period after a mapping closes,
	// during which the port rejects all traffic and cannot be reallocated.
	Quarantine time.Duration
	// Lease is the maximum lifetime of a mapping. Zero disables expiry.
	Lease time.Duration
	// TenantQuota is the maximum number of concurrent active mappings per
	// tenant. Zero means unlimited.
	TenantQuota int
	// Clock drives quarantine and lease timers. Nil uses RealClock.
	Clock Clock
}

// Pool allocates temporary UDP backhaul ports to tenants, binding each
// mapping to a remote endpoint and an allocation generation.
type Pool struct {
	mu         sync.Mutex
	clock      Clock
	quarantine time.Duration
	lease      time.Duration
	quota      int
	ports      []int
	slots      map[int]*portSlot
	tenants    map[string]int
}

func NewPool(cfg Config) *Pool {
	clock := cfg.Clock
	if clock == nil {
		clock = RealClock{}
	}
	p := &Pool{
		clock:      clock,
		quarantine: cfg.Quarantine,
		lease:      cfg.Lease,
		quota:      cfg.TenantQuota,
		ports:      append([]int(nil), cfg.Ports...),
		slots:      make(map[int]*portSlot, len(cfg.Ports)),
		tenants:    make(map[string]int),
	}
	for _, port := range cfg.Ports {
		p.slots[port] = &portSlot{}
	}
	return p
}

// Allocate reserves a free port for tenant, bound to remote. It fails with
// ErrQuotaExceeded if the tenant is at its quota, or ErrPoolExhausted if no
// port is free (ports in quarantine are not allocatable).
func (p *Pool) Allocate(tenant, remote string) (*Mapping, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.quota > 0 && p.tenants[tenant] >= p.quota {
		return nil, fmt.Errorf("%w: tenant %q", ErrQuotaExceeded, tenant)
	}
	for _, port := range p.ports {
		slot := p.slots[port]
		if slot.state != stateFree {
			continue
		}
		slot.state = stateActive
		slot.generation++
		slot.tenant = tenant
		slot.remote = remote
		p.tenants[tenant]++
		m := &Mapping{
			Port:       port,
			Tenant:     tenant,
			Remote:     remote,
			Generation: slot.generation,
		}
		if p.lease > 0 {
			gen := slot.generation
			p.clock.AfterFunc(p.lease, func() { p.expire(port, gen) })
		}
		return m, nil
	}
	return nil, ErrPoolExhausted
}

// HandleReply reports whether an inbound reply on port from remote belongs
// to the live mapping of the given generation. Late packets (stale
// generation, mismatched remote, quarantined or closed port) are rejected.
func (p *Pool) HandleReply(port int, remote string, generation uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	slot, ok := p.slots[port]
	if !ok {
		return false
	}
	return slot.state == stateActive &&
		slot.generation == generation &&
		slot.remote == remote
}

// Close releases the mapping identified by port and generation. It returns
// true only for the caller that actually closes the live mapping; stale or
// duplicate closes return false. The port enters quarantine and becomes
// allocatable again only after the quarantine elapses.
func (p *Pool) Close(port int, generation uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	slot, ok := p.slots[port]
	if !ok || slot.state != stateActive || slot.generation != generation {
		return false
	}
	p.closeLocked(port, slot)
	return true
}

// expire is the lease timer callback. The generation guard ensures a timer
// scheduled by a previous allocation of the same port can never clear the
// current mapping.
func (p *Pool) expire(port int, generation uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	slot, ok := p.slots[port]
	if !ok || slot.state != stateActive || slot.generation != generation {
		return
	}
	p.closeLocked(port, slot)
}

// closeLocked transitions an active slot into quarantine. The caller must
// hold p.mu and must have validated the generation.
func (p *Pool) closeLocked(port int, slot *portSlot) {
	slot.state = stateQuarantine
	p.tenants[slot.tenant]--
	if p.tenants[slot.tenant] == 0 {
		delete(p.tenants, slot.tenant)
	}
	gen := slot.generation
	p.clock.AfterFunc(p.quarantine, func() { p.release(port, gen) })
}

// release is the quarantine timer callback. It only frees the port if the
// slot is still quarantined at the same generation, so a recycled port is
// never freed by a stale timer.
func (p *Pool) release(port int, generation uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	slot, ok := p.slots[port]
	if !ok || slot.state != stateQuarantine || slot.generation != generation {
		return
	}
	slot.state = stateFree
	slot.tenant = ""
	slot.remote = ""
}

// ActiveCount reports how many active mappings tenant currently holds.
func (p *Pool) ActiveCount(tenant string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tenants[tenant]
}

// FreePorts reports how many ports are currently allocatable.
func (p *Pool) FreePorts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, port := range p.ports {
		if p.slots[port].state == stateFree {
			n++
		}
	}
	return n
}
