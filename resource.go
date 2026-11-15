package netsec

import "sync"

// Resource is a host-side resource (file or network endpoint) guarded by
// capability handles. Close is lease-aware: callbacks that acquired an
// operation lease before Close may finish, and once Close has returned no
// new callback may enter.
type Resource struct {
	host   *Host
	id     resourceID
	rtype  ResourceType
	name   string
	mu     sync.Mutex
	cond   *sync.Cond
	closed bool
	leases int
}

func newResource(host *Host, id resourceID, rtype ResourceType, name string) *Resource {
	r := &Resource{host: host, id: id, rtype: rtype, name: name}
	r.cond = sync.NewCond(&r.mu)
	return r
}

// Type reports the resource type fixed at registration time.
func (r *Resource) Type() ResourceType {
	return r.rtype
}

// acquire takes an operation lease, or reports that the resource is closed.
func (r *Resource) acquire() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	r.leases++
	return true
}

func (r *Resource) release() {
	r.mu.Lock()
	r.leases--
	if r.leases == 0 {
		r.cond.Broadcast()
	}
	r.mu.Unlock()
}

// Close marks the resource closed and blocks until every outstanding
// operation lease has been released. After Close returns, acquire always
// fails, so no new resource callback can begin.
func (r *Resource) Close() {
	r.mu.Lock()
	r.closed = true
	for r.leases > 0 {
		r.cond.Wait()
	}
	r.mu.Unlock()
}

// Closed reports whether Close has been invoked.
func (r *Resource) Closed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}
