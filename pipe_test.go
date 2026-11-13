package netsec

import (
	"context"
	"errors"
	"testing"
)

func newSecureFixture(t *testing.T) (*Host, *Service, *PipeInstance, *Client) {
	t.Helper()
	host := NewHost(HostIdentity)
	svc := NewService(host)
	pipe := host.NewInstance()
	client := NewClient("A", IdentityA, pipe)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	return host, svc, pipe, client
}

func TestLifecycleHappyPath(t *testing.T) {
	_, svc, pipe, client := newSecureFixture(t)
	if pipe.State() != StateConnected {
		t.Fatalf("state = %v, want Connected", pipe.State())
	}
	if err := client.Send(1, []byte("hello")); err != nil {
		t.Fatalf("send: %v", err)
	}
	out, err := svc.ServeOnce(context.Background(), pipe)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	if out.Identity != IdentityA {
		t.Fatalf("identity = %+v, want %+v", out.Identity, IdentityA)
	}
	if string(out.Payload) != "hello" {
		t.Fatalf("payload = %q", out.Payload)
	}
	if pipe.State() != StateConnected {
		t.Fatalf("state after revert = %v, want Connected", pipe.State())
	}
}

func TestHalfPacketRead(t *testing.T) {
	_, svc, pipe, client := newSecureFixture(t)
	frame := EncodeFrame(7, []byte("split-payload"))
	half := len(frame) / 2
	if err := client.SendRaw(frame[:half]); err != nil {
		t.Fatalf("send first half: %v", err)
	}
	if _, err := svc.Begin(pipe); !errors.Is(err, ErrRequestFailed) {
		t.Fatalf("read of half frame: err = %v, want opaque failure", err)
	}
	if err := client.SendRaw(frame[half:]); err != nil {
		t.Fatalf("send second half: %v", err)
	}
	tx, err := svc.Begin(pipe)
	if err != nil {
		t.Fatalf("read of completed frame: %v", err)
	}
	if string(tx.req.Bytes()) != "split-payload" {
		t.Fatalf("payload = %q", tx.req.Bytes())
	}
}

func TestHalfPacketDroppedOnDisconnect(t *testing.T) {
	host, svc, pipe, client := newSecureFixture(t)
	frame := EncodeFrame(1, []byte("x"))
	if err := client.SendRaw(frame[:4]); err != nil {
		t.Fatalf("send partial: %v", err)
	}
	client.Disconnect()
	if pipe.State() != StateDisconnected {
		t.Fatalf("state = %v, want Disconnected", pipe.State())
	}
	if err := pipe.Listen(); err != nil {
		t.Fatalf("re-listen must succeed after stale bytes were cleared: %v", err)
	}
	// A new client reuses the instance; the old half frame must be gone.
	other := NewClient("B", IdentityB, pipe)
	if err := other.Connect(); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if _, err := svc.Begin(pipe); !errors.Is(err, ErrRequestFailed) {
		t.Fatalf("stale half frame leaked into new connection")
	}
	_ = host
}

func TestStaleSnapshotRejectedAfterReconnect(t *testing.T) {
	_, svc, pipe, client := newSecureFixture(t)
	if err := client.Send(1, PayloadA); err != nil {
		t.Fatalf("send: %v", err)
	}
	tx, err := svc.Begin(pipe)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	genBefore := pipe.Generation()
	client.Disconnect()
	if pipe.Generation() != genBefore+1 {
		t.Fatalf("generation not bumped on disconnect")
	}
	if err := pipe.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}
	// Same client reconnects: even the same principal gets a new snapshot.
	if err := client.Connect(); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if err := tx.Impersonate(); !errors.Is(err, ErrRequestFailed) {
		t.Fatalf("impersonate with stale snapshot: err = %v", err)
	}
}

func TestClientPIDReuseDoesNotForgeSnapshot(t *testing.T) {
	_, svc, pipe, client := newSecureFixture(t)
	if err := client.Send(1, PayloadA); err != nil {
		t.Fatalf("send: %v", err)
	}
	tx, err := svc.Begin(pipe)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	client.Disconnect()
	if err := pipe.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}
	// Attacker reuses A's PID and start time; only the token differs.
	squatter := NewClient("B", Identity{ProcessID: IdentityA.ProcessID, StartTime: IdentityA.StartTime, TokenID: 0xBBBB}, pipe)
	if err := squatter.Connect(); err != nil {
		t.Fatalf("squatter connect: %v", err)
	}
	if err := tx.Impersonate(); !errors.Is(err, ErrRequestFailed) {
		t.Fatalf("impersonate after PID-reuse reconnect: err = %v, want opaque failure", err)
	}
}

func TestMultipleInstancesAreIsolated(t *testing.T) {
	host := NewHost(HostIdentity)
	svc := NewService(host)
	p1, p2 := host.NewInstance(), host.NewInstance()
	c1 := NewClient("A", IdentityA, p1)
	c2 := NewClient("B", IdentityB, p2)
	if err := c1.Connect(); err != nil {
		t.Fatalf("c1 connect: %v", err)
	}
	if err := c2.Connect(); err != nil {
		t.Fatalf("c2 connect: %v", err)
	}
	if err := c1.Send(1, []byte("on-instance-1")); err != nil {
		t.Fatalf("send: %v", err)
	}
	tx, err := svc.Begin(p1)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	// The snapshot from instance 1 must not impersonate on instance 2.
	if _, err := p2.Impersonate(tx.snap); !errors.Is(err, errIdentityMismatch) && !errors.Is(err, errNoPendingRequest) {
		t.Fatalf("cross-instance impersonate: err = %v", err)
	}
	// Instance 2 is unaffected by instance 1's lifecycle.
	c1.Disconnect()
	if p2.State() != StateConnected {
		t.Fatalf("instance 2 state = %v, want Connected", p2.State())
	}
}

func TestDuplicateRequestIDRejected(t *testing.T) {
	_, svc, pipe, client := newSecureFixture(t)
	if err := client.Send(9, []byte("first")); err != nil {
		t.Fatalf("send: %v", err)
	}
	if _, err := svc.ServeOnce(context.Background(), pipe); err != nil {
		t.Fatalf("first serve: %v", err)
	}
	if err := client.Send(9, []byte("replay")); err != nil {
		t.Fatalf("resend: %v", err)
	}
	if _, err := svc.Begin(pipe); !errors.Is(err, ErrRequestFailed) {
		t.Fatalf("duplicate request id: err = %v, want opaque failure", err)
	}
	// A fresh connection generation may reuse the same request id.
	client.Disconnect()
	if err := pipe.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}
	if err := client.Connect(); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if err := client.Send(9, []byte("fresh")); err != nil {
		t.Fatalf("send fresh: %v", err)
	}
	if _, err := svc.Begin(pipe); err != nil {
		t.Fatalf("request id reuse across generations must be allowed: %v", err)
	}
}

func TestNoNextRequestUntilRevert(t *testing.T) {
	_, svc, pipe, client := newSecureFixture(t)
	if err := client.Send(1, []byte("one")); err != nil {
		t.Fatalf("send one: %v", err)
	}
	if err := client.Send(2, []byte("two")); err != nil {
		t.Fatalf("send two: %v", err)
	}
	tx, err := svc.Begin(pipe)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := tx.Impersonate(); err != nil {
		t.Fatalf("impersonate: %v", err)
	}
	// While impersonating, the pipe must not accept the next read.
	if _, _, err := pipe.ReadRequest(); !errors.Is(err, errProtocolState) {
		t.Fatalf("read during impersonation: err = %v", err)
	}
	if err := tx.Revert(); err != nil {
		t.Fatalf("revert: %v", err)
	}
	// Double revert is a protocol failure.
	if err := tx.Revert(); !errors.Is(err, ErrRequestFailed) {
		t.Fatalf("double revert: err = %v", err)
	}
}

func TestDisconnectRevokesHandleBetweenImpersonateAndCall(t *testing.T) {
	_, svc, pipe, client := newSecureFixture(t)
	if err := client.Send(1, PayloadA); err != nil {
		t.Fatalf("send: %v", err)
	}
	tx, err := svc.Begin(pipe)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := tx.Impersonate(); err != nil {
		t.Fatalf("impersonate: %v", err)
	}
	client.Disconnect()
	if _, err := tx.Call(context.Background()); !errors.Is(err, ErrRequestFailed) {
		t.Fatalf("call after disconnect: err = %v, want opaque failure", err)
	}
}

func TestCancellationBetweenImpersonateAndCall(t *testing.T) {
	_, svc, pipe, client := newSecureFixture(t)
	if err := client.Send(1, PayloadA); err != nil {
		t.Fatalf("send: %v", err)
	}
	tx, err := svc.Begin(pipe)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := tx.Impersonate(); err != nil {
		t.Fatalf("impersonate: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tx.Call(ctx); !errors.Is(err, ErrRequestFailed) {
		t.Fatalf("canceled call: err = %v, want opaque failure", err)
	}
	// The pipe is still impersonating; revert must succeed so the service
	// can move on.
	if err := tx.Revert(); err != nil {
		t.Fatalf("revert after cancel: %v", err)
	}
}

func TestPanicPathStillReverts(t *testing.T) {
	_, svc, pipe, client := newSecureFixture(t)
	if err := client.Send(1, []byte("boom")); err != nil {
		t.Fatalf("send: %v", err)
	}
	svc.testHook = func() { panic("sensitive call exploded") }
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected panic to propagate")
			}
		}()
		_, _ = svc.ServeOnce(context.Background(), pipe)
	}()
	svc.testHook = nil
	if pipe.State() != StateConnected {
		t.Fatalf("state after panic = %v, want Connected (reverted)", pipe.State())
	}
	// The next request is processed normally.
	if err := client.Send(2, []byte("after-panic")); err != nil {
		t.Fatalf("send: %v", err)
	}
	out, err := svc.ServeOnce(context.Background(), pipe)
	if err != nil {
		t.Fatalf("serve after panic: %v", err)
	}
	if string(out.Payload) != "after-panic" {
		t.Fatalf("payload = %q", out.Payload)
	}
}

func TestIdentityAndProtocolErrorsAreIndistinguishable(t *testing.T) {
	_, svc, pipe, client := newSecureFixture(t)
	if err := client.Send(1, PayloadA); err != nil {
		t.Fatalf("send: %v", err)
	}
	tx, err := svc.Begin(pipe)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	// Identity failure: snapshot invalidated by disconnect.
	client.Disconnect()
	identityErr := tx.Impersonate()

	// Protocol failure: read with no complete frame on a fresh instance.
	_, svc2, pipe2, client2 := newSecureFixture(t)
	_ = svc2
	if err := client2.SendRaw([]byte{0, 0}); err != nil {
		t.Fatalf("send fragment: %v", err)
	}
	_, protocolErr := svc.Begin(pipe2)

	if identityErr == nil || protocolErr == nil {
		t.Fatalf("want both failures, got identity=%v protocol=%v", identityErr, protocolErr)
	}
	if identityErr.Error() != protocolErr.Error() {
		t.Fatalf("errors distinguishable: %q vs %q", identityErr, protocolErr)
	}
	if !errors.Is(identityErr, ErrRequestFailed) || !errors.Is(protocolErr, ErrRequestFailed) {
		t.Fatalf("both errors must be ErrRequestFailed")
	}
}

func TestRequestBytesAreImmutable(t *testing.T) {
	_, svc, pipe, client := newSecureFixture(t)
	if err := client.Send(1, []byte("original")); err != nil {
		t.Fatalf("send: %v", err)
	}
	tx, err := svc.Begin(pipe)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	b := tx.req.Bytes()
	b[0] = 'X'
	if string(tx.req.Bytes()) != "original" {
		t.Fatalf("request bytes mutated via returned slice")
	}
}
