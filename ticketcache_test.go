package ticketcache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// blockingIssuer blocks every Issue call until the test responds to it,
// and records each call so tests can observe exactly when renewals happen.
type blockingIssuer struct {
	calls chan issueCall
}

type issueCall struct {
	key     Key
	respond chan issueResult
}

type issueResult struct {
	ticket Ticket
	err    error
}

func newBlockingIssuer() *blockingIssuer {
	return &blockingIssuer{calls: make(chan issueCall, 32)}
}

func (b *blockingIssuer) Issue(ctx context.Context, key Key) (Ticket, error) {
	c := issueCall{key: key, respond: make(chan issueResult, 1)}
	b.calls <- c
	select {
	case r := <-c.respond:
		return r.ticket, r.err
	case <-ctx.Done():
		return Ticket{}, ctx.Err()
	}
}

// next waits for the next issuer call and fails the test if none arrives.
func (b *blockingIssuer) next(t *testing.T) issueCall {
	t.Helper()
	select {
	case c := <-b.calls:
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("expected an issuer call, got none")
		return issueCall{}
	}
}

// expectNone fails the test if any issuer call arrives within a grace period.
func (b *blockingIssuer) expectNone(t *testing.T) {
	t.Helper()
	select {
	case c := <-b.calls:
		t.Fatalf("unexpected issuer call for %+v", c.key)
	case <-time.After(50 * time.Millisecond):
	}
}

type getResult struct {
	ticket Ticket
	err    error
}

func getAsync(c *Cache, ctx context.Context, key Key) <-chan getResult {
	ch := make(chan getResult, 1)
	go func() {
		tk, err := c.Get(ctx, key)
		ch <- getResult{ticket: tk, err: err}
	}()
	return ch
}

func mustGet(t *testing.T, ch <-chan getResult) Ticket {
	t.Helper()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("Get failed: %v", r.err)
		}
		return r.ticket
	case <-time.After(2 * time.Second):
		t.Fatal("Get did not return")
		return Ticket{}
	}
}

// Concurrent misses for the same partition share one issuer call instead of
// triggering duplicate renewals, and the result is cached afterwards.
func TestSharedRenewalSingleIssuerCall(t *testing.T) {
	iss := newBlockingIssuer()
	c := New(iss)
	key := Key{Tenant: "t1", Principal: "alice", Service: "db", Scope: "read"}

	const waiters = 5
	results := make([]<-chan getResult, waiters)
	for i := range results {
		results[i] = getAsync(c, context.Background(), key)
	}

	call := iss.next(t)
	time.Sleep(50 * time.Millisecond) // let all waiters join the flight
	iss.expectNone(t)                 // no duplicate renewals

	call.respond <- issueResult{ticket: Ticket{Token: "tok-1"}}
	for _, ch := range results {
		if tk := mustGet(t, ch); tk.Token != "tok-1" {
			t.Fatalf("got token %q, want tok-1", tk.Token)
		}
	}

	// The ticket is now cached: further Gets must not hit the issuer.
	tk, err := c.Get(context.Background(), key)
	if err != nil || tk.Token != "tok-1" {
		t.Fatalf("cached Get = %q, %v; want tok-1, nil", tk.Token, err)
	}
	iss.expectNone(t)
}

// A narrow-scope request must neither reuse a cached wide-scope ticket nor
// join an in-flight wide-scope refresh: it gets its own issuer call.
func TestNarrowScopeDoesNotUseWideTicket(t *testing.T) {
	iss := newBlockingIssuer()
	c := New(iss)
	wide := Key{Tenant: "t1", Principal: "alice", Service: "db", Scope: "db:*"}
	narrow := Key{Tenant: "t1", Principal: "alice", Service: "db", Scope: "db:read-own"}

	wideRes := getAsync(c, context.Background(), wide)
	wideCall := iss.next(t) // wide refresh in flight, blocked

	narrowRes := getAsync(c, context.Background(), narrow)
	narrowCall := iss.next(t) // narrow request triggers its own renewal
	if narrowCall.key.Scope != narrow.Scope {
		t.Fatalf("narrow request issued with scope %q", narrowCall.key.Scope)
	}

	narrowCall.respond <- issueResult{ticket: Ticket{Token: "tok-narrow"}}
	wideCall.respond <- issueResult{ticket: Ticket{Token: "tok-wide"}}

	if tk := mustGet(t, narrowRes); tk.Token != "tok-narrow" {
		t.Fatalf("narrow Get = %q, want tok-narrow", tk.Token)
	}
	if tk := mustGet(t, wideRes); tk.Token != "tok-wide" {
		t.Fatalf("wide Get = %q, want tok-wide", tk.Token)
	}

	// Both are cached independently now.
	tk, err := c.Get(context.Background(), narrow)
	if err != nil || tk.Token != "tok-narrow" {
		t.Fatalf("cached narrow Get = %q, %v", tk.Token, err)
	}
	iss.expectNone(t)
}

// Different delegation chains partition the cache even when every other
// field matches.
func TestDelegationChainPartitions(t *testing.T) {
	iss := newBlockingIssuer()
	c := New(iss)
	direct := Key{Tenant: "t1", Principal: "alice", Service: "db", Scope: "read"}
	delegated := direct
	delegated.Delegation = []string{"svc-a", "svc-b"}

	directRes := getAsync(c, context.Background(), direct)
	directCall := iss.next(t)

	delegatedRes := getAsync(c, context.Background(), delegated)
	delegatedCall := iss.next(t) // separate partition, separate renewal
	if len(delegatedCall.key.Delegation) != 2 {
		t.Fatalf("delegated call key = %+v", delegatedCall.key)
	}

	directCall.respond <- issueResult{ticket: Ticket{Token: "tok-direct"}}
	delegatedCall.respond <- issueResult{ticket: Ticket{Token: "tok-delegated"}}

	if tk := mustGet(t, directRes); tk.Token != "tok-direct" {
		t.Fatalf("direct Get = %q", tk.Token)
	}
	if tk := mustGet(t, delegatedRes); tk.Token != "tok-delegated" {
		t.Fatalf("delegated Get = %q", tk.Token)
	}
}

// Each waiter on a shared refresh can cancel independently; the shared
// issuer call and the remaining waiters are unaffected.
func TestWaiterCancellation(t *testing.T) {
	iss := newBlockingIssuer()
	c := New(iss)
	key := Key{Tenant: "t1", Principal: "alice", Service: "db", Scope: "read"}

	ctx, cancel := context.WithCancel(context.Background())
	cancelled := getAsync(c, ctx, key)
	call := iss.next(t)

	staying := getAsync(c, context.Background(), key)
	time.Sleep(50 * time.Millisecond) // let the second waiter join

	cancel()
	select {
	case r := <-cancelled:
		if !errors.Is(r.err, context.Canceled) {
			t.Fatalf("cancelled waiter err = %v, want context.Canceled", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled waiter did not return")
	}

	// The shared refresh is still in flight and must not be duplicated.
	iss.expectNone(t)

	call.respond <- issueResult{ticket: Ticket{Token: "tok-1"}}
	if tk := mustGet(t, staying); tk.Token != "tok-1" {
		t.Fatalf("remaining waiter got %q, want tok-1", tk.Token)
	}
}

// A revocation intercepts an in-flight refresh of the old generation: its
// result is neither cached nor delivered, and the next Get renews afresh.
func TestRevokeInterceptsStaleRefresh(t *testing.T) {
	iss := newBlockingIssuer()
	c := New(iss)
	key := Key{Tenant: "t1", Principal: "alice", Service: "db", Scope: "read"}

	before := getAsync(c, context.Background(), key)
	stale := iss.next(t) // refresh of the old generation, blocked

	c.Revoke(key)
	stale.respond <- issueResult{ticket: Ticket{Token: "tok-stale"}}

	select {
	case r := <-before:
		if !errors.Is(r.err, ErrRevoked) {
			t.Fatalf("stale waiter err = %v, want ErrRevoked", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stale waiter did not return")
	}

	// The stale ticket must not have been cached: the next Get renews.
	after := getAsync(c, context.Background(), key)
	fresh := iss.next(t)
	fresh.respond <- issueResult{ticket: Ticket{Token: "tok-fresh"}}
	if tk := mustGet(t, after); tk.Token != "tok-fresh" {
		t.Fatalf("post-revoke Get = %q, want tok-fresh", tk.Token)
	}
}

// Revoking a cached ticket forces a renewal on the next Get.
func TestRevokeCachedTicket(t *testing.T) {
	iss := newBlockingIssuer()
	c := New(iss)
	key := Key{Tenant: "t1", Principal: "alice", Service: "db", Scope: "read"}

	first := getAsync(c, context.Background(), key)
	call := iss.next(t)
	call.respond <- issueResult{ticket: Ticket{Token: "tok-1"}}
	mustGet(t, first)

	c.Revoke(key)

	second := getAsync(c, context.Background(), key)
	call = iss.next(t)
	call.respond <- issueResult{ticket: Ticket{Token: "tok-2"}}
	if tk := mustGet(t, second); tk.Token != "tok-2" {
		t.Fatalf("Get after revoke = %q, want tok-2", tk.Token)
	}
}

// Expired tickets are renewed; unexpired ones are served from the cache.
func TestExpiredTicketIsRenewed(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	c := New(IssuerFunc(func(ctx context.Context, key Key) (Ticket, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return Ticket{
			Token:     fmt.Sprintf("tok-%d", calls),
			ExpiresAt: time.Now().Add(-time.Second), // already expired
		}, nil
	}))
	key := Key{Tenant: "t1", Principal: "alice", Service: "db", Scope: "read"}

	tk1, err := c.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	tk2, err := c.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if tk1.Token != "tok-1" || tk2.Token != "tok-2" {
		t.Fatalf("got %q then %q, want tok-1 then tok-2", tk1.Token, tk2.Token)
	}
}

// Issuer errors propagate to all waiters, and the next Get retries.
func TestIssuerErrorPropagatesAndRetries(t *testing.T) {
	iss := newBlockingIssuer()
	c := New(iss)
	key := Key{Tenant: "t1", Principal: "alice", Service: "db", Scope: "read"}

	wantErr := errors.New("issuer unavailable")
	first := getAsync(c, context.Background(), key)
	second := getAsync(c, context.Background(), key)
	call := iss.next(t)
	time.Sleep(50 * time.Millisecond)
	call.respond <- issueResult{err: wantErr}

	for _, ch := range []<-chan getResult{first, second} {
		select {
		case r := <-ch:
			if !errors.Is(r.err, wantErr) {
				t.Fatalf("waiter err = %v, want %v", r.err, wantErr)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("waiter did not return")
		}
	}

	retry := getAsync(c, context.Background(), key)
	call = iss.next(t)
	call.respond <- issueResult{ticket: Ticket{Token: "tok-ok"}}
	if tk := mustGet(t, retry); tk.Token != "tok-ok" {
		t.Fatalf("retry got %q, want tok-ok", tk.Token)
	}
}
