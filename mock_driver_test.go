package netsec_test

import (
	"context"
	"fmt"
	"net"
	"sync"

	netsec "example.org/netsec/q0169"
)

// mockLease is a driver allocation used only in tests. It records whether it
// was recycled so cancellation and validation-failure paths can be asserted.
type mockLease struct {
	network  string
	address  string
	driver   *mockDriver
	released bool
	mu       sync.Mutex
}

func (l *mockLease) Network() string { return l.network }

func (l *mockLease) Addr() string { return l.address }

func (l *mockLease) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil
	}
	l.released = true
	l.driver.recycle(l)
	return nil
}

// mockDriver is a scriptable listener driver. Allocate can be redirected to
// an unexpected address, mapped onto IPv6, made to honor cancellation, or
// made to fail outright.
type mockDriver struct {
	mu sync.Mutex

	active   map[*mockLease]struct{}
	released []*mockLease
	nextPort int

	// override, when non-nil, replaces the address the driver would normally
	// return for an allocation.
	override func(id netsec.ListenID) (network, address string)

	// failWith makes Allocate return an error without allocating.
	failWith error
}

func newMockDriver() *mockDriver {
	return &mockDriver{
		active:   make(map[*mockLease]struct{}),
		nextPort: 40000,
	}
}

func (d *mockDriver) Allocate(ctx context.Context, id netsec.ListenID) (netsec.Lease, error) {
	if d.failWith != nil {
		return nil, d.failWith
	}

	// A real driver blocks here until the socket exists or the request is
	// canceled, so cancellation must be observable before allocation.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	d.mu.Lock()
	port := d.nextPort
	d.nextPort++
	d.mu.Unlock()

	network := "tcp"
	host := id.IP.String()
	if id.Empty {
		host = ""
	}

	if d.override != nil {
		network, host = d.override(id)
		if host == "" {
			host = id.IP.String()
		}
	}

	lease := &mockLease{
		network: network,
		address: net.JoinHostPort(host, fmt.Sprint(port)),
		driver:  d,
	}
	d.mu.Lock()
	d.active[lease] = struct{}{}
	d.mu.Unlock()
	return lease, nil
}

func (d *mockDriver) recycle(l *mockLease) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.active, l)
	d.released = append(d.released, l)
}

func (d *mockDriver) activeCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.active)
}

func (d *mockDriver) releasedCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.released)
}
