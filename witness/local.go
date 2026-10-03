package witness

import "time"

// LocalClient is an in-process Witness client (no network). Useful for
// single-process deployments, embedded judges and tests.
type LocalClient struct {
	W *Witness
}

func NewLocalClient(w *Witness) *LocalClient {
	return &LocalClient{W: w}
}

func (c *LocalClient) Acquire(holder string, ttl time.Duration) (Lease, bool, error) {
	l, ok := c.W.Acquire(holder, ttl)
	return l, ok, nil
}

func (c *LocalClient) Renew(holder string, epoch uint64, ttl time.Duration) (Lease, bool, error) {
	l, ok := c.W.Renew(holder, epoch, ttl)
	return l, ok, nil
}
