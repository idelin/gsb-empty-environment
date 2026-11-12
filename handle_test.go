package netsec

import (
	"errors"
	"syscall"
	"testing"
)

func TestHandleBoundToOpener(t *testing.T) {
	m := newTestMapper()
	handles := NewHandleTable()

	alice, err := m.MapRequest(Header{UID: 1000, GID: 100}, OpOpen)
	if err != nil {
		t.Fatalf("map alice: %v", err)
	}
	const fh = 42
	handles.Bind(fh, alice)

	if err := handles.Check(fh, alice); err != nil {
		t.Fatalf("opener reuse: got %v, want nil", err)
	}
}

func TestCrossUserHandleUseDenied(t *testing.T) {
	m := newTestMapper()
	handles := NewHandleTable()

	alice, err := m.MapRequest(Header{UID: 1000, GID: 100}, OpOpen)
	if err != nil {
		t.Fatalf("map alice: %v", err)
	}
	bob, err := m.MapRequest(Header{UID: 1001, GID: 101}, OpOpen)
	if err != nil {
		t.Fatalf("map bob: %v", err)
	}

	const fh = 7
	handles.Bind(fh, alice)

	if err := handles.Check(fh, bob); !errors.Is(err, syscall.EACCES) {
		t.Fatalf("cross-user handle: got %v, want EACCES", err)
	}
	// The rightful owner is unaffected by the rejected attempt.
	if err := handles.Check(fh, alice); err != nil {
		t.Fatalf("owner after cross-user attempt: got %v, want nil", err)
	}
}

func TestUnknownAndReleasedHandle(t *testing.T) {
	handles := NewHandleTable()
	id := Identity{UID: 5000, GID: 600}

	if err := handles.Check(1234, id); !errors.Is(err, syscall.EBADF) {
		t.Fatalf("unknown handle: got %v, want EBADF", err)
	}

	const fh = 9
	handles.Bind(fh, id)
	handles.Release(fh)
	if err := handles.Check(fh, id); !errors.Is(err, syscall.EBADF) {
		t.Fatalf("released handle: got %v, want EBADF", err)
	}
}
