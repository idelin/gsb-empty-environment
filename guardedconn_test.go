package dialpolicy

import (
	"errors"
	"net"
	"testing"
)

func TestGuardedConnSeversAfterSwap(t *testing.T) {
	guard := New(allowOnly(goodIP))
	server, client := net.Pipe()
	conn := guard.tracker.wrap(client, guard, MustAddr(goodIP))

	go func() {
		_, _ = server.Write([]byte("hello"))
	}()
	buf := make([]byte, 5)
	if _, err := conn.Read(buf); err != nil {
		t.Fatalf("read under allowed policy: %v", err)
	}

	guard.UpdatePolicy(allowOnly(goodIP2))
	go func() {
		_, _ = server.Write([]byte("world"))
	}()
	_, err := conn.Read(buf)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden after swap, got %v", err)
	}
}
