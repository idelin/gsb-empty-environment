// Package controller implements the per-datacenter lease controller. Each DC
// runs one controller which keeps a lease with the remote Witness:
//
//   - While holding a locally-unexpired lease, the DC is Primary (read-write).
//   - When the lease cannot be renewed (partition) the controller demotes
//     itself to read-only Standby as soon as the local lease expires, so it
//     stops writing *before* the Witness may hand the lease to the peer DC.
//   - When a renew/acquire is rejected, the controller learns the Witness has
//     issued a higher epoch to someone else; it records the new fencing token
//     and stays/becomes a read-only Standby.
package controller

import (
	"errors"
	"sync"
	"time"

	"leasefed/store"
	"leasefed/witness"
)

// ErrNotPrimary is returned by Write when this DC is a read-only standby.
var ErrNotPrimary = errors.New("controller: not primary, datacenter is read-only")

// Role is the datacenter role from this controller's point of view.
type Role int

const (
	Standby Role = iota
	Primary
)

func (r Role) String() string {
	if r == Primary {
		return "PRIMARY(read-write)"
	}
	return "STANDBY(read-only)"
}

// Client abstracts the link to the Witness so tests and demos can cut it.
type Client interface {
	Acquire(holder string, ttl time.Duration) (witness.Lease, bool, error)
	Renew(holder string, epoch uint64, ttl time.Duration) (witness.Lease, bool, error)
}

type Controller struct {
	id       string
	client   Client
	st       *store.Store
	ttl      time.Duration
	interval time.Duration

	mu    sync.Mutex
	role  Role
	lease witness.Lease

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// New creates a controller for datacenter id. ttl is the lease duration
// requested from the Witness; renewals happen 3x faster than the ttl.
func New(id string, client Client, st *store.Store, ttl time.Duration) *Controller {
	return &Controller{
		id:       id,
		client:   client,
		st:       st,
		ttl:      ttl,
		interval: ttl / 3,
		role:     Standby,
		stopCh:   make(chan struct{}),
	}
}

func (c *Controller) Store() *store.Store { return c.st }

// Start launches the lease maintenance loop.
func (c *Controller) Start() {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.tick() // acquire immediately instead of waiting one interval
		t := time.NewTicker(c.interval)
		defer t.Stop()
		for {
			select {
			case <-c.stopCh:
				return
			case <-t.C:
				c.tick()
			}
		}
	}()
}

// Stop halts the maintenance loop.
func (c *Controller) Stop() {
	close(c.stopCh)
	c.wg.Wait()
}

func (c *Controller) tick() {
	c.mu.Lock()
	role, lease := c.role, c.lease
	c.mu.Unlock()

	if role == Primary {
		l, ok, err := c.client.Renew(c.id, lease.Epoch, c.ttl)
		switch {
		case err != nil:
			// Link to the Witness is down. We may keep serving writes only
			// until the local lease expires; afterwards we must demote
			// ourselves, because the Witness is free to hand the lease over.
			if !lease.Valid(time.Now()) {
				c.demote()
			}
		case !ok:
			// The judge rejected our credential: a higher epoch is out
			// there. Record the fence and step down to read-only.
			c.st.SetFence(l.Epoch)
			c.demote()
		default:
			c.mu.Lock()
			c.lease = l
			c.mu.Unlock()
		}
		return
	}

	// Standby: try to (re)acquire the lease. Granted only if nobody holds a
	// live lease, i.e. the previous primary is considered dead by the judge.
	l, ok, err := c.client.Acquire(c.id, c.ttl)
	if err != nil {
		return
	}
	c.st.SetFence(l.Epoch)
	if ok {
		c.mu.Lock()
		c.role = Primary
		c.lease = l
		c.mu.Unlock()
	}
}

func (c *Controller) demote() {
	c.mu.Lock()
	c.role = Standby
	c.lease = witness.Lease{}
	c.mu.Unlock()
}

// Role returns the current role.
func (c *Controller) Role() Role {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.role
}

// Epoch returns the epoch of the lease currently held (0 if standby).
func (c *Controller) Epoch() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lease.Epoch
}

// IsPrimary reports whether this DC may serve writes right now: it must
// believe it is primary AND its lease must not have expired locally.
func (c *Controller) IsPrimary() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.role == Primary && c.lease.Valid(time.Now())
}

// Write performs a fenced write to the local store. It fails with
// ErrNotPrimary on a standby, and with store.ErrStaleFencingToken if the
// lease epoch turned out to be stale.
func (c *Controller) Write(key, value string) error {
	c.mu.Lock()
	primary := c.role == Primary && c.lease.Valid(time.Now())
	epoch := c.lease.Epoch
	c.mu.Unlock()
	if !primary {
		return ErrNotPrimary
	}
	return c.st.Put(key, value, epoch)
}
