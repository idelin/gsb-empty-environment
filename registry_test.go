package netsec

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func testPeer(pid int) PeerCreds {
	return PeerCreds{PID: pid, UID: 1000, GID: 1000, Labels: map[string]string{"zone": "local"}}
}

// TestSameNameRebuildOldConnCannotImpersonate is the headline security
// property: deleting and re-registering an endpoint name mints a new
// instance, and a connection bound to the old instance can never speak for
// the new one.
func TestSameNameRebuildOldConnCannotImpersonate(t *testing.T) {
	r := NewRegistry()
	tr := NewMockTransport(r)
	const name = "db.sock"

	first, err := r.Register(name)
	if err != nil {
		t.Fatalf("first register: %v", err)
	}

	auth1 := newDelayedAuth(testPeer(42), Challenge{})
	tc1, conn1, ch1, err := tr.Dial(name, testPeer(42), auth1)
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	auth1.SetExpectedChallenge(ch1)
	if !conn1.Instance().Equal(first.Instance()) {
		t.Fatal("first conn not bound to first instance")
	}
	auth1.Resolve()
	if err := conn1.WaitAuth(); err != nil {
		t.Fatalf("first auth: %v", err)
	}
	if err := conn1.Deliver([]byte("before rebuild")); err != nil {
		t.Fatalf("first deliver: %v", err)
	}

	// Rebuild under the same name. The old instance is revoked even though
	// the name still resolves.
	second, err := r.Register(name)
	if err != nil {
		t.Fatalf("second register: %v", err)
	}
	if second.Instance().Equal(first.Instance()) {
		t.Fatal("re-registration reused the old instance identity")
	}
	if got := r.Names(); len(got) != 1 || got[0] != name {
		t.Fatalf("names after rebuild = %v", got)
	}

	// The old connection keeps its fixed binding and cannot reach the new
	// instance; revocation blocks any further message on it.
	if conn1.Instance().Equal(second.Instance()) {
		t.Fatal("old connection rebound to replacement instance")
	}
	if !first.Revoked() {
		t.Fatal("old instance should be revoked after same-name rebuild")
	}
	if err := conn1.Deliver([]byte("impersonation attempt")); !errors.Is(err, ErrEndpointRevoked) {
		t.Fatalf("old conn deliver after rebuild err = %v, want ErrEndpointRevoked", err)
	}

	// A fresh handshake binds to the new generation, even from the same
	// peer credentials.
	auth2 := newDelayedAuth(testPeer(42), Challenge{})
	tc2, conn2, ch2, err := tr.Dial(name, testPeer(42), auth2)
	if err != nil {
		t.Fatalf("second dial: %v", err)
	}
	auth2.SetExpectedChallenge(ch2)
	if conn2.Instance().Equal(conn1.Instance()) {
		t.Fatal("new connection bound to stale instance")
	}
	if !conn2.Instance().Equal(second.Instance()) {
		t.Fatal("new connection not bound to current instance")
	}
	auth2.Resolve()
	if err := conn2.WaitAuth(); err != nil {
		t.Fatalf("second auth: %v", err)
	}
	if err := conn2.Deliver([]byte("after rebuild")); err != nil {
		t.Fatalf("second deliver: %v", err)
	}

	// Received messages stay attached to the fixed instance that handled
	// them, so in-flight work can finish against the old generation.
	oldMsgs := first.Messages()
	if len(oldMsgs) != 1 || string(oldMsgs[0].Payload) != "before rebuild" {
		t.Fatalf("old instance messages = %v", oldMsgs)
	}
	if !oldMsgs[0].Instance.Equal(first.Instance()) {
		t.Fatal("accepted message not tagged with fixed instance")
	}
	newMsgs := second.Messages()
	if len(newMsgs) != 1 || string(newMsgs[0].Payload) != "after rebuild" {
		t.Fatalf("new instance messages = %v", newMsgs)
	}

	tr.HangUp(tc1, conn1)
	tr.HangUp(tc2, conn2)
}

// TestLateAuthenticationCallback exercises authentication responses that
// arrive after the endpoint instance was revoked, replaced or the
// connection closed. Such callbacks can never retroactively admit it.
func TestLateAuthenticationCallback(t *testing.T) {
	r := NewRegistry()
	tr := NewMockTransport(r)
	const name = "auth.sock"

	ep, err := r.Register(name)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// Case 1: callback lands after explicit revocation.
	a1 := newDelayedAuth(testPeer(7), Challenge{})
	tc1, c1, ch1, err := tr.Dial(name, testPeer(7), a1)
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	a1.SetExpectedChallenge(ch1)
	if err := c1.Deliver(nil); !errors.Is(err, ErrConnUnauthenticated) {
		t.Fatalf("pre-auth deliver err = %v, want ErrConnUnauthenticated", err)
	}
	if err := r.Revoke(name); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	a1.Resolve()
	if err := c1.WaitAuth(); err != nil {
		t.Fatalf("late success callback should still complete auth: %v", err)
	}
	if !c1.AuthDone() {
		t.Fatal("expected auth state to be recorded after late success callback")
	}
	if err := c1.Deliver([]byte("late")); !errors.Is(err, ErrEndpointRevoked) {
		t.Fatalf("deliver after revoke err = %v, want ErrEndpointRevoked", err)
	}

	// Case 2: callback lands after a same-name replacement.
	ep2, err := r.Register(name)
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}
	a2 := newDelayedAuth(testPeer(7), Challenge{})
	tc2, c2, ch2, err := tr.Dial(name, testPeer(7), a2)
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	a2.SetExpectedChallenge(ch2)
	ep3, err := r.Register(name)
	if err != nil {
		t.Fatalf("third register: %v", err)
	}
	a2.Resolve()
	if err := c2.WaitAuth(); err != nil {
		t.Fatalf("late callback on replaced instance should record auth: %v", err)
	}
	if err := c2.Deliver([]byte("late")); !errors.Is(err, ErrEndpointRevoked) {
		t.Fatalf("deliver on replaced instance err = %v, want ErrEndpointRevoked", err)
	}
	if len(ep2.Messages()) != 0 {
		t.Fatal("replaced instance must not accept late-authenticated messages")
	}

	// Case 3: callback lands after Close; it is a no-op and the connection
	// stays unusable.
	a3 := newDelayedAuth(testPeer(7), Challenge{})
	tc3, c3, ch3, err := tr.Dial(name, testPeer(7), a3)
	if err != nil {
		t.Fatalf("dial 3: %v", err)
	}
	a3.SetExpectedChallenge(ch3)
	tr.HangUp(tc3, c3)
	a3.Resolve()
	if c3.AuthDone() {
		t.Fatal("callback after Close must not mark the connection authed")
	}
	if err := c3.Deliver([]byte("late")); !errors.Is(err, ErrConnClosed) {
		t.Fatalf("deliver after close err = %v", err)
	}

	// Case 4: a late negative response rejects a still-open connection.
	a4 := newDelayedAuth(testPeer(8), Challenge{})
	tc4, c4, ch4, err := tr.Dial(name, testPeer(8), a4)
	if err != nil {
		t.Fatalf("dial 4: %v", err)
	}
	a4.SetExpectedChallenge(ch4)
	a4.ResolveWith(errBoom)
	if err := c4.WaitAuth(); !errors.Is(err, errBoom) {
		t.Fatalf("WaitAuth negative err = %v", err)
	}
	if err := c4.Deliver([]byte("nope")); !errors.Is(err, errBoom) {
		t.Fatalf("deliver after failed auth err = %v", err)
	}

	// Case 5: duplicate callbacks do not flip established state.
	a5 := newDelayedAuth(testPeer(9), Challenge{})
	tc5, c5, ch5, err := tr.Dial(name, testPeer(9), a5)
	if err != nil {
		t.Fatalf("dial 5: %v", err)
	}
	a5.SetExpectedChallenge(ch5)
	a5.Resolve()
	if err := c5.WaitAuth(); err != nil {
		t.Fatalf("auth 5: %v", err)
	}
	a5.ResolveWith(errBoom)
	if err := c5.Deliver([]byte("still good")); err != nil {
		t.Fatalf("duplicate callback changed auth state: %v", err)
	}

	tr.HangUp(tc1, c1)
	tr.HangUp(tc2, c2)
	tr.HangUp(tc4, c4)
	tr.HangUp(tc5, c5)
	_ = ep
	_ = ep3
}

// TestHandleReuse proves recycled transport handles carry no authority:
// two unrelated connections may share a handle number while staying bound
// to distinct instances and peer credentials.
func TestHandleReuse(t *testing.T) {
	r := NewRegistry()
	tr := NewMockTransport(r)
	const name = "fd.sock"

	ep1, err := r.Register(name)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	a1 := newDelayedAuth(testPeer(11), Challenge{})
	tc1, c1, ch1, err := tr.Dial(name, testPeer(11), a1)
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	a1.SetExpectedChallenge(ch1)
	firstHandle := tc1.handle
	a1.Resolve()
	if err := c1.WaitAuth(); err != nil {
		t.Fatalf("auth 1: %v", err)
	}
	if err := c1.Deliver([]byte("generation one")); err != nil {
		t.Fatalf("deliver 1: %v", err)
	}

	// Free the handle, rebuild the endpoint, then reacquire the same
	// numeric handle for a connection from a different peer.
	tr.HangUp(tc1, c1)
	ep2, err := r.Register(name)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	a2 := newDelayedAuth(testPeer(22), Challenge{})
	tc2, c2, ch2, err := tr.Dial(name, testPeer(22), a2)
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	a2.SetExpectedChallenge(ch2)
	if tc2.handle != firstHandle {
		t.Fatalf("expected recycled handle %d, got %d", firstHandle, tc2.handle)
	}
	if tr.ReuseCount() == 0 {
		t.Fatal("handle pool did not observe a reuse")
	}
	if c2.PeerCreds().Equal(c1.PeerCreds()) {
		t.Fatal("reused handle inherited stale peer credentials")
	}
	if c2.Instance().Equal(c1.Instance()) {
		t.Fatal("reused handle inherited stale instance binding")
	}
	a2.Resolve()
	if err := c2.WaitAuth(); err != nil {
		t.Fatalf("auth 2: %v", err)
	}
	if err := c2.Deliver([]byte("generation two")); err != nil {
		t.Fatalf("deliver 2: %v", err)
	}

	// Both generations keep their own fixed message histories.
	if got := ep1.Messages(); len(got) != 1 || string(got[0].Payload) != "generation one" {
		t.Fatalf("ep1 messages = %v", got)
	}
	if got := ep2.Messages(); len(got) != 1 || string(got[0].Payload) != "generation two" {
		t.Fatalf("ep2 messages = %v", got)
	}

	tr.HangUp(tc2, c2)
}

// TestChallengeAndCredsBinding checks the creation-time binding: the
// challenge is unique per connection, an authenticator expecting a
// different challenge rejects, and accepted messages carry the exact peer
// credentials captured at dial time.
func TestChallengeAndCredsBinding(t *testing.T) {
	r := NewRegistry()
	tr := NewMockTransport(r)
	const name = "bind.sock"
	if _, err := r.Register(name); err != nil {
		t.Fatalf("register: %v", err)
	}

	a1 := newDelayedAuth(testPeer(1), Challenge{})
	_, c1, ch1, err := tr.Dial(name, testPeer(1), a1)
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	a1.SetExpectedChallenge(ch1)
	a2 := newDelayedAuth(testPeer(2), Challenge{})
	_, c2, ch2, err := tr.Dial(name, testPeer(2), a2)
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	a2.SetExpectedChallenge(ch2)
	if ch1.Equal(ch2) {
		t.Fatal("two connections received the same challenge")
	}

	// A foreign authenticator expecting the other connection's challenge
	// must fail when used on a new connection.
	foreign := newDelayedAuth(testPeer(1), ch2)
	_, c3, _, err := tr.Dial(name, testPeer(1), foreign)
	if err != nil {
		t.Fatalf("dial 3: %v", err)
	}
	if err := c3.WaitAuth(); !errors.Is(err, ErrChallengeMismatch) {
		t.Fatalf("foreign challenge err = %v", err)
	}
	if err := c3.Deliver(nil); !errors.Is(err, ErrChallengeMismatch) {
		t.Fatalf("deliver with mismatched challenge err = %v", err)
	}

	a1.Resolve()
	a2.Resolve()
	if err := c1.WaitAuth(); err != nil {
		t.Fatalf("auth 1: %v", err)
	}
	if err := c2.WaitAuth(); err != nil {
		t.Fatalf("auth 2: %v", err)
	}
	if err := c1.Deliver([]byte("hi")); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	msg := c1.Endpoint().Messages()[0]
	if !msg.Peer.Equal(testPeer(1)) {
		t.Fatalf("message peer creds = %+v", msg.Peer)
	}
	if !msg.Instance.Equal(c1.Instance()) {
		t.Fatal("message instance does not match connection binding")
	}

	// Handshake against an unknown name is rejected outright.
	if _, _, _, err := tr.Dial("missing", testPeer(1), a1); !errors.Is(err, ErrConnRejected) {
		t.Fatalf("dial unknown name err = %v, want ErrConnRejected", err)
	}

	c1.Close()
	c2.Close()
	c3.Close()
}

// TestRevocationBlocksNewButFinishesReceived verifies the message
// lifecycle: once admitted, messages remain on the fixed instance after
// revocation, while no new message can enter and Wait unblocks.
func TestRevocationBlocksNewButFinishesReceived(t *testing.T) {
	r := NewRegistry()
	tr := NewMockTransport(r)
	const name = "lifecycle.sock"
	ep, err := r.Register(name)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	a := newDelayedAuth(testPeer(3), Challenge{})
	tc, c, ch, err := tr.Dial(name, testPeer(3), a)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	a.SetExpectedChallenge(ch)
	a.Resolve()
	if err := c.WaitAuth(); err != nil {
		t.Fatalf("auth: %v", err)
	}
	for _, payload := range []string{"one", "two"} {
		if err := c.Deliver([]byte(payload)); err != nil {
			t.Fatalf("deliver %s: %v", payload, err)
		}
	}

	waited := make(chan []Message, 1)
	go func() { waited <- ep.Wait() }()
	select {
	case msgs := <-waited:
		if len(msgs) != 2 {
			t.Fatalf("Wait returned %d messages", len(msgs))
		}
	case <-time.After(time.Second):
		t.Fatal("Wait did not return after messages were admitted")
	}

	if err := r.Revoke(name); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := c.Deliver([]byte("three")); !errors.Is(err, ErrEndpointRevoked) {
		t.Fatalf("post-revoke deliver err = %v", err)
	}
	// Already received work is still there, pinned to the fixed instance.
	msgs := ep.Messages()
	if len(msgs) != 2 {
		t.Fatalf("revocation altered received messages: %v", msgs)
	}
	for _, m := range msgs {
		if !m.Instance.Equal(ep.Instance()) {
			t.Fatal("received message lost its fixed instance after revocation")
		}
	}
	// Wait on an empty, revoked endpoint returns promptly.
	empty, err := r.Register("empty.sock")
	if err != nil {
		t.Fatalf("register empty: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = empty.Wait()
		close(done)
	}()
	empty.Revoke()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Wait did not unblock on revocation")
	}

	tr.HangUp(tc, c)
}

// TestConcurrentRebuildAndDeliver hammers the registry with concurrent
// registrations, dials, deliveries and closes to make sure no connection
// ever lands a message on a generation other than the one it bound to.
func TestConcurrentRebuildAndDeliver(t *testing.T) {
	r := NewRegistry()
	tr := NewMockTransport(r)
	const name = "concurrent.sock"
	if _, err := r.Register(name); err != nil {
		t.Fatalf("register: %v", err)
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				auth := newDelayedAuth(testPeer(100+worker), Challenge{})
				tc, conn, ch, err := tr.Dial(name, testPeer(100+worker), auth)
				if err != nil {
					continue // lost a race against revocation/rebuild
				}
				auth.SetExpectedChallenge(ch)
				auth.Resolve()
				if err := conn.WaitAuth(); err != nil {
					_ = conn.Close()
					tc.close()
					continue
				}
				bound := conn.Instance()
				if err := conn.Deliver([]byte("x")); err == nil {
					if !conn.Completed() {
						t.Error("admitted message did not mark connection completed")
					}
					for _, m := range conn.Endpoint().Messages() {
						if !m.Instance.Equal(bound) {
							t.Errorf("message on %s tagged with foreign instance", m.Endpoint)
						}
					}
				}
				if worker%2 == 0 {
					if _, err := r.Register(name); err != nil {
						t.Errorf("concurrent register: %v", err)
					}
				}
				tr.HangUp(tc, conn)
			}
		}(worker)
	}
	wg.Wait()

	// Every endpoint generation only ever holds messages tagged with its
	// own instance identity.
	for _, ep := range collectEndpoints(r) {
		for _, m := range ep.Messages() {
			if !m.Instance.Equal(ep.Instance()) {
				t.Fatalf("endpoint %s holds a foreign-instance message", ep.Name())
			}
		}
	}
}

func collectEndpoints(r *Registry) []*Endpoint {
	// Endpoints are not retained in a public list after revocation, so this
	// helper relies on the internal map plus ActiveConns reachability via
	// current registrations only; the per-message check above is the main
	// invariant and this loop covers the still-registered generation.
	var out []*Endpoint
	for _, name := range r.Names() {
		if ep, err := r.Lookup(name); err == nil {
			out = append(out, ep)
		}
	}
	return out
}

// TestSyncAndFuncAuthenticators covers the two built-in Authenticator
// adapters, including synchronous verification failure.
func TestSyncAndFuncAuthenticators(t *testing.T) {
	r := NewRegistry()
	tr := NewMockTransport(r)
	const name = "adapters.sock"
	if _, err := r.Register(name); err != nil {
		t.Fatalf("register: %v", err)
	}

	syncAuth := SyncAuthenticator{Verify: func(_ PeerCreds, _ Challenge) error { return nil }}
	_, ok, ch, err := tr.Dial(name, testPeer(1), syncAuth)
	if err != nil {
		t.Fatalf("sync dial: %v", err)
	}
	if err := ok.WaitAuth(); err != nil {
		t.Fatalf("sync auth: %v", err)
	}
	if err := ok.Deliver([]byte("sync")); err != nil {
		t.Fatalf("sync deliver: %v", err)
	}

	failAuth := AuthenticatorFunc(func(_ PeerCreds, _ Challenge, result func(error)) {
		result(errBoom)
	})
	_, bad, _, err := tr.Dial(name, testPeer(2), failAuth)
	if err != nil {
		t.Fatalf("func dial: %v", err)
	}
	if err := bad.WaitAuth(); !errors.Is(err, errBoom) {
		t.Fatalf("func auth err = %v", err)
	}
	if err := bad.Deliver(nil); !errors.Is(err, errBoom) {
		t.Fatalf("func deliver err = %v", err)
	}

	// A nil authenticator must be rejected at handshake time.
	if _, _, _, err := tr.Dial(name, testPeer(3), nil); !errors.Is(err, ErrConnRejected) {
		t.Fatalf("nil auth dial err = %v", err)
	}

	var zero Challenge
	if ch == zero {
		t.Fatal("challenge should be non-zero")
	}
	ok.Close()
	bad.Close()
}
