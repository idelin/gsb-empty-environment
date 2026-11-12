package netsec

import (
	"errors"
	"syscall"
	"testing"
)

func newTestMapper() *Mapper {
	return NewMapper(
		map[uint32]uint32{1000: 5000, 1001: 5001},
		map[uint32]uint32{100: 600, 101: 601},
	)
}

func TestUnmappedUIDRejected(t *testing.T) {
	m := newTestMapper()
	_, err := m.MapRequest(Header{UID: 9999, GID: 100}, OpOpen)
	if !errors.Is(err, syscall.EACCES) {
		t.Fatalf("unmapped uid: got %v, want EACCES", err)
	}
}

func TestUnmappedGIDRejected(t *testing.T) {
	m := newTestMapper()
	_, err := m.MapRequest(Header{UID: 1000, GID: 999}, OpOpen)
	if !errors.Is(err, syscall.EACCES) {
		t.Fatalf("unmapped gid: got %v, want EACCES", err)
	}
}

func TestRootNotInTableRejected(t *testing.T) {
	m := newTestMapper()
	// uid 0 is not in the table: it must not fall back to the daemon
	// identity, it must be denied like any other unmapped uid.
	_, err := m.MapRequest(Header{UID: 0, GID: 100}, OpOpen)
	if !errors.Is(err, syscall.EACCES) {
		t.Fatalf("unmapped root: got %v, want EACCES", err)
	}
}

func TestRootExplicitlyMapped(t *testing.T) {
	m := NewMapper(map[uint32]uint32{0: 7000}, map[uint32]uint32{0: 7001})
	id, err := m.MapRequest(Header{UID: 0, GID: 0}, OpOpen)
	if err != nil {
		t.Fatalf("explicitly mapped root: unexpected error %v", err)
	}
	if id != (Identity{UID: 7000, GID: 7001}) {
		t.Fatalf("explicitly mapped root: got %+v", id)
	}
}

func TestMappedCaller(t *testing.T) {
	m := newTestMapper()
	id, err := m.MapRequest(Header{UID: 1000, GID: 100}, OpRead)
	if err != nil {
		t.Fatalf("mapped caller: unexpected error %v", err)
	}
	if id != (Identity{UID: 5000, GID: 600}) {
		t.Fatalf("mapped caller: got %+v", id)
	}
}

func TestGIDPassThroughWithoutGIDTable(t *testing.T) {
	m := NewMapper(map[uint32]uint32{1000: 5000}, nil)
	id, err := m.MapRequest(Header{UID: 1000, GID: 4242}, OpOpen)
	if err != nil {
		t.Fatalf("no gid table: unexpected error %v", err)
	}
	if id.GID != 4242 {
		t.Fatalf("no gid table: gid got %d, want passthrough 4242", id.GID)
	}
}

func TestForgetAndInterruptNotMapped(t *testing.T) {
	m := newTestMapper()
	hdr := Header{UID: 9999, GID: 9999} // unmapped on purpose
	for _, op := range []Op{OpForget, OpInterrupt, OpInit, OpDestroy} {
		if _, err := m.MapRequest(hdr, op); err != nil {
			t.Fatalf("op %d must not be mapped: got %v", op, err)
		}
	}
}

func TestNewFileOwnerIsMappedCaller(t *testing.T) {
	m := newTestMapper()
	uid, gid, err := m.CreateOwner(Header{UID: 1001, GID: 101})
	if err != nil {
		t.Fatalf("create owner: unexpected error %v", err)
	}
	if uid != 5001 || gid != 601 {
		t.Fatalf("create owner: got (%d, %d), want (5001, 601)", uid, gid)
	}
	// An unmapped caller cannot create files at all.
	if _, _, err := m.CreateOwner(Header{UID: 4242, GID: 100}); !errors.Is(err, syscall.EACCES) {
		t.Fatalf("create owner unmapped: got %v, want EACCES", err)
	}
}
