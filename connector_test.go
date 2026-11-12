package netsec

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

// TestPausableConnector exercises the deterministic test transport itself.
func TestPausableConnector(t *testing.T) {
	connector := NewPausableConnector()

	t.Run("pause and resume dial", func(t *testing.T) {
		ln := mustListen(t, connector, goodPeer)
		connector.PauseDials()

		errCh := make(chan error, 1)
		go func() {
			_, err := connector.Dial(context.Background(), goodPeer)
			errCh <- err
		}()
		eventually(t, func() bool { return connector.PendingDials() == 1 })

		select {
		case err := <-errCh:
			t.Fatalf("dial proceeded while paused: %v", err)
		case <-time.After(30 * time.Millisecond):
		}

		connector.ResumeDials()
		if err := waitErr(t, errCh); err != nil {
			t.Fatalf("dial after resume: %v", err)
		}
		conn := acceptOne(t, ln)
		remote, ok := RemoteAddrPort(conn)
		if !ok {
			t.Fatal("dialed connection has no parseable remote")
		}
		if remote == goodPeer {
			t.Fatalf("client ephemeral address equals listener address: %s", remote)
		}
		_ = conn.Close()
	})

	t.Run("dial to unknown target is refused", func(t *testing.T) {
		_, err := connector.Dial(context.Background(), otherPeer)
		if !errors.Is(err, ErrConnectionRefused) {
			t.Fatalf("err = %v, want ErrConnectionRefused", err)
		}
	})

	t.Run("paused accept queues and close rejects", func(t *testing.T) {
		ln := mustListen(t, connector, evilPeer)
		ln.PauseAccept()

		first := ln.Inject(goodPeer)
		second := ln.Inject(otherPeer)
		if got := eventuallyQueueLen(ln, 2); got != 2 {
			t.Fatalf("backlog = %d, want 2", got)
		}

		ln.ResumeAccept()
		if _, err := ln.Accept(); err != nil {
			t.Fatalf("Accept after resume: %v", err)
		}

		// Closing drains the remaining backlog; late peers get zero body.
		if err := ln.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		assertZeroBody(t, second)

		late := ln.Inject(goodPeer)
		assertZeroBody(t, late)
		if _, err := ln.Accept(); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Accept after close err = %v, want net.ErrClosed", err)
		}
		_ = first
	})

	t.Run("explicit listen address is honored", func(t *testing.T) {
		want := netip.MustParseAddrPort("127.0.0.1:39999")
		ln, addr, err := connector.Listen(context.Background(), want)
		if err != nil {
			t.Fatalf("Listen: %v", err)
		}
		if addr != want {
			t.Fatalf("addr = %s", addr)
		}
		_ = ln.Close()
	})
}
