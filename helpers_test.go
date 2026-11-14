package netsec

import (
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1_700_000_000, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestAuthorizer(t *testing.T, opts ...Option) *Authorizer {
	t.Helper()
	a := NewAuthorizer(opts...)
	if err := a.RegisterTenant("tenantA", []string{"tenantA/"}); err != nil {
		t.Fatalf("register tenantA: %v", err)
	}
	if err := a.RegisterTenant("tenantB", []string{"tenantB/"}); err != nil {
		t.Fatalf("register tenantB: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

func drain(ch <-chan Event) []Event {
	var out []Event
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		default:
			return out
		}
	}
}

func keys(events []Event) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Key)
	}
	return out
}
