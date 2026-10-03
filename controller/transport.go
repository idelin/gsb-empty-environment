package controller

import (
	"errors"
	"sync"
	"time"

	"leasefed/witness"
)

// ErrLinkDown is returned by CuttableClient while the fiber link between the
// datacenter and the Witness is "cut".
var ErrLinkDown = errors.New("transport: link down, witness unreachable")

// CuttableClient wraps a Client and simulates the fiber link between a
// datacenter and the third-party Witness. Cut() severs the link (fiber
// dug up), Heal() restores it.
type CuttableClient struct {
	inner Client

	mu  sync.RWMutex
	cut bool
}

func NewCuttableClient(inner Client) *CuttableClient {
	return &CuttableClient{inner: inner}
}

func (c *CuttableClient) Cut() {
	c.mu.Lock()
	c.cut = true
	c.mu.Unlock()
}

func (c *CuttableClient) Heal() {
	c.mu.Lock()
	c.cut = false
	c.mu.Unlock()
}

func (c *CuttableClient) down() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cut
}

func (c *CuttableClient) Acquire(holder string, ttl time.Duration) (witness.Lease, bool, error) {
	if c.down() {
		return witness.Lease{}, false, ErrLinkDown
	}
	return c.inner.Acquire(holder, ttl)
}

func (c *CuttableClient) Renew(holder string, epoch uint64, ttl time.Duration) (witness.Lease, bool, error) {
	if c.down() {
		return witness.Lease{}, false, ErrLinkDown
	}
	return c.inner.Renew(holder, epoch, ttl)
}
