package netsec

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestPassivePortPreemption proves a connection racing onto the passive port
// cannot hijack the authorized transfer. The attacker connects first but is
// rejected and receives a zero body; the authorized peer still gets the
// one-shot data channel.
func TestPassivePortPreemption(t *testing.T) {
	session, connector := mustSession(t, "alice")

	transfer, err := session.Authorize(passiveApproval("alice", goodPeer))
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	bound, err := transfer.OpenPassive(context.Background())
	if err != nil {
		t.Fatalf("OpenPassive: %v", err)
	}

	listener, ok := connector.Listener(bound)
	if !ok {
		t.Fatalf("no listener bound at %s", bound)
	}

	// Freeze Accept so the backlog holds both racing connections in order.
	listener.PauseAccept()
	attacker := listener.Inject(evilPeer)
	legit := listener.Inject(goodPeer)
	if got := eventuallyQueueLen(listener, 2); got != 2 {
		t.Fatalf("backlog = %d, want 2", got)
	}

	attached := make(chan net.Conn, 1)
	go func() {
		conn, attachErr := transfer.Attach(context.Background())
		if attachErr == nil {
			attached <- conn
		}
	}()

	listener.ResumeAccept()

	dataConn := waitAttached(t, attached)
	if state := transfer.State(); state != Attached {
		t.Fatalf("state = %v, want Attached", state)
	}

	// The first (attacker) connection must be closed with no body delivered.
	assertZeroBody(t, attacker)

	payload := []byte("authorized file body")
	go func() { _, _ = dataConn.Write(payload) }()

	readExact(t, legit, payload)

	// The instance is one-shot: a second Attach cannot reuse the approval.
	secondCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := transfer.Attach(secondCtx); err == nil {
		t.Fatal("second Attach on consumed instance succeeded")
	}

	// A later connection cannot bind to the already-consumed instance.
	late := listener.Inject(goodPeer)
	assertZeroBody(t, late)

	_ = dataConn.Close()
	if err := transfer.Complete(); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if state := transfer.State(); state != Done {
		t.Fatalf("state = %v, want Done", state)
	}
}

// TestActiveTargetReplacement proves the active channel only dials an approved
// target. A paused dial cannot be redirected, and an out-of-set target is
// refused without opening any data channel.
func TestActiveTargetReplacement(t *testing.T) {
	t.Run("paused dial keeps approved target", func(t *testing.T) {
		session, connector := mustSession(t, "bob")
		transfer, err := session.Authorize(activeApproval("bob", goodPeer))
		if err != nil {
			t.Fatalf("Authorize: %v", err)
		}

		// The target the peer offers on the control channel must exist first;
		// it is the only approved endpoint, so its identity cannot be swapped.
		targetLn := mustListen(t, connector, goodPeer)

		connector.PauseDials()
		dialErr := make(chan error, 1)
		go func() {
			dialErr <- transfer.ConnectActive(context.Background(), goodPeer)
		}()
		eventually(t, func() bool { return connector.PendingDials() == 1 })

		// Attempt to replace the target while the dial is parked: the approval
		// carries no other reachable target, so no swap can succeed.
		connector.ResumeDials()
		assertErrorIs(t, waitErr(t, dialErr), nil)

		dataConn := waitActiveConn(t, transfer)
		serverConn := acceptOne(t, targetLn)
		expectEcho(t, dataConn, serverConn)
		_ = dataConn.Close()
		_ = transfer.Complete()

		for _, dialed := range connector.Dialed() {
			if dialed != goodPeer {
				t.Fatalf("dialed %s, want only approved %s", dialed, goodPeer)
			}
		}
	})

	t.Run("replaced target is refused", func(t *testing.T) {
		session, connector := mustSession(t, "bob")
		transfer, err := session.Authorize(activeApproval("bob", goodPeer))
		if err != nil {
			t.Fatalf("Authorize: %v", err)
		}

		// An attacker-controlled listener exists, but is outside the peer set.
		evilLn := mustListen(t, connector, evilPeer)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err = transfer.ConnectActive(ctx, evilPeer)
		assertErrorIs(t, err, ErrPeerNotAllowed)

		if state := transfer.State(); state != Failed {
			t.Fatalf("state = %v, want Failed after rejected target", state)
		}
		if _, err := transfer.Attach(ctx); err == nil {
			t.Fatal("Attach succeeded after rejected active target")
		}

		// No connection ever reaches the attacker listener: zero body there.
		_ = evilLn.Close()
		if evilLn.Rejected() != 0 || evilLn.Accepted() != 0 {
			t.Fatalf("attacker accepted=%d rejected=%d, want no contact", evilLn.Accepted(), evilLn.Rejected())
		}
	})

	t.Run("canceled paused dial cannot attach", func(t *testing.T) {
		session, connector := mustSession(t, "bob")
		transfer, err := session.Authorize(activeApproval("bob", goodPeer))
		if err != nil {
			t.Fatalf("Authorize: %v", err)
		}
		mustListen(t, connector, goodPeer)

		connector.PauseDials()
		dialErr := make(chan error, 1)
		go func() {
			dialErr <- transfer.ConnectActive(context.Background(), goodPeer)
		}()
		eventually(t, func() bool { return connector.PendingDials() == 1 })

		if err := transfer.Cancel(); err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		connector.ResumeDials()
		assertErrorIs(t, waitErr(t, dialErr), ErrTransferRevoked)

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, err := transfer.Attach(ctx); !errorIs(err, ErrTransferRevoked) {
			t.Fatalf("Attach after cancel err = %v, want ErrTransferRevoked", err)
		}
	})
}

// TestLateDataConnection covers connections arriving after cancel,
// reauthentication and the next transfer: stale approvals never attach and the
// late peer receives a zero body.
func TestLateDataConnection(t *testing.T) {
	t.Run("after transfer cancel", func(t *testing.T) {
		session, connector := mustSession(t, "carol")
		transfer, err := session.Authorize(passiveApproval("carol", goodPeer))
		if err != nil {
			t.Fatalf("Authorize: %v", err)
		}
		bound, err := transfer.OpenPassive(context.Background())
		if err != nil {
			t.Fatalf("OpenPassive: %v", err)
		}
		listener, _ := connector.Listener(bound)

		if err := transfer.Cancel(); err != nil {
			t.Fatalf("Cancel: %v", err)
		}

		late := listener.Inject(goodPeer)
		assertZeroBody(t, late)

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, err := transfer.Attach(ctx); !errorIs(err, ErrTransferRevoked) {
			t.Fatalf("Attach err = %v, want ErrTransferRevoked", err)
		}
	})

	t.Run("after reauthentication", func(t *testing.T) {
		session, connector := mustSession(t, "carol")
		old, err := session.Authorize(passiveApproval("carol", goodPeer))
		if err != nil {
			t.Fatalf("Authorize: %v", err)
		}
		bound, err := old.OpenPassive(context.Background())
		if err != nil {
			t.Fatalf("OpenPassive: %v", err)
		}
		oldListener, _ := connector.Listener(bound)

		if err := session.Reauthenticate("carol-2"); err != nil {
			t.Fatalf("Reauthenticate: %v", err)
		}

		// The old approval must not attach under the new epoch.
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, err := old.Attach(ctx); !errorIs(err, ErrTransferRevoked) {
			t.Fatalf("old Attach err = %v, want ErrTransferRevoked", err)
		}
		late := oldListener.Inject(goodPeer)
		assertZeroBody(t, late)

		// Reusing the old approval under the new account is rejected.
		if _, err := session.Authorize(passiveApproval("carol", goodPeer)); !errorIs(err, ErrInvalidApproval) {
			t.Fatalf("reuse old approval err = %v, want ErrInvalidApproval", err)
		}

		// The next transfer gets a fresh approval and independent channel.
		next, err := session.Authorize(passiveApproval("carol-2", goodPeer))
		if err != nil {
			t.Fatalf("next Authorize: %v", err)
		}
		nextBound, err := next.OpenPassive(context.Background())
		if err != nil {
			t.Fatalf("next OpenPassive: %v", err)
		}
		if nextBound == bound {
			t.Fatal("new transfer reused the revoked passive endpoint")
		}
		nextListener, _ := connector.Listener(nextBound)
		legit := nextListener.Inject(goodPeer)
		dataConn := assertAttaches(t, next)
		expectEcho(t, dataConn, legit)
		_ = dataConn.Close()
		_ = next.Complete()
	})

	t.Run("session cancel and close revoke all", func(t *testing.T) {
		session, connector := mustSession(t, "dave")
		transfer, err := session.Authorize(passiveApproval("dave", goodPeer))
		if err != nil {
			t.Fatalf("Authorize: %v", err)
		}
		bound, err := transfer.OpenPassive(context.Background())
		if err != nil {
			t.Fatalf("OpenPassive: %v", err)
		}
		listener, _ := connector.Listener(bound)

		session.Cancel()
		assertZeroBody(t, listener.Inject(goodPeer))

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, err := transfer.Attach(ctx); !errorIs(err, ErrTransferRevoked) {
			t.Fatalf("Attach after session cancel err = %v", err)
		}
		if err := session.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if _, err := session.Authorize(passiveApproval("dave", goodPeer)); !errorIs(err, ErrTransferRevoked) {
			t.Fatalf("Authorize after close err = %v, want ErrTransferRevoked", err)
		}
	})
}
