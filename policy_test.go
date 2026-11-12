package netsec

import (
	"errors"
	"testing"
)

// TestTrustedZeroCopyFlow drives the full trusted path end to end: a
// zero-copy bind is authorized, its descriptors are validated against the
// UMEM, and producer indices wrap onto legal ring slots.
func TestTrustedZeroCopyFlow(t *testing.T) {
	p := New()
	tenant := Tenant{ID: "trusted-tenant", Trusted: true}

	if err := p.AuthorizeBind(BindRequest{Tenant: tenant, Mode: ModeZeroCopy}); err != nil {
		t.Fatalf("trusted zero-copy bind rejected: %v", err)
	}

	umem := UMEMConfig{Size: 4096, FrameSize: 2048, Unaligned: true}
	tx := Descriptor{Addr: 100, Len: 900}
	if err := p.CheckDescriptor(umem, tx); err != nil {
		t.Fatalf("valid TX descriptor rejected: %v", err)
	}

	ring, err := NewRing(8)
	if err != nil {
		t.Fatalf("NewRing: %v", err)
	}
	for _, cursor := range []uint32{8, 9, 15, 16} {
		slot := ring.Wrap(cursor)
		if err := ring.CheckIndex(slot); err != nil {
			t.Fatalf("cursor %d wrapped to bad slot: %v", cursor, err)
		}
	}
}

// TestUntrustedZeroCopyFlow proves an untrusted tenant never reaches ring
// or descriptor processing with a zero-copy socket: the bind is refused
// rather than silently downgraded.
func TestUntrustedZeroCopyFlow(t *testing.T) {
	p := New()
	tenant := Tenant{ID: "untrusted-tenant", Trusted: false}

	err := p.AuthorizeBind(BindRequest{Tenant: tenant, Mode: ModeZeroCopy})
	if !errors.Is(err, ErrZeroCopyDenied) {
		t.Fatalf("untrusted zero-copy err = %v, want ErrZeroCopyDenied", err)
	}

	// The same tenant can still serve traffic through copy mode.
	if err := p.AuthorizeBind(BindRequest{Tenant: tenant, Mode: ModeCopy}); err != nil {
		t.Fatalf("untrusted copy bind rejected: %v", err)
	}
}
