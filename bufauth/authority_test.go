package bufauth

import (
	"errors"
	"testing"
)

func fullPerms() Perm {
	return PermRead | PermWrite | PermReexport | PermMap
}

func TestIssueAndCheck(t *testing.T) {
	auth := NewAuthority()
	root, err := auth.Issue("buf0", fullPerms())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if root.ID() == 0 || root.ParentID() != 0 || !root.Live() {
		t.Fatalf("unexpected root grant: %+v", root)
	}
	if err := auth.Check(root, PermWrite|PermMap); err != nil {
		t.Fatalf("Check full perms: %v", err)
	}
	if _, err := auth.Issue("buf0", PermNone); !errors.Is(err, ErrNoPerms) {
		t.Fatalf("empty perms err = %v, want ErrNoPerms", err)
	}
	if _, err := auth.Issue("buf0", Perm(1<<31)); !errors.Is(err, ErrUnknownPerm) {
		t.Fatalf("unknown perms err = %v, want ErrUnknownPerm", err)
	}
}

func TestReadOnlyCannotEscalateToWrite(t *testing.T) {
	auth := NewAuthority()
	// A read-only upstream that nevertheless may be forwarded.
	up, err := auth.Issue("buf0", PermRead|PermReexport)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Equal or lesser masks are accepted.
	derived, err := auth.Forward(up, PermRead)
	if err != nil {
		t.Fatalf("Forward read-only subset: %v", err)
	}
	if derived.Perms() != PermRead || derived.BufferID() != up.BufferID() {
		t.Fatalf("derived grant mismatch: %+v", derived)
	}

	// Turning a read-only buffer into a writable one must be rejected.
	if _, err := auth.Forward(up, PermRead|PermWrite); !errors.Is(err, ErrPermEscalation) {
		t.Fatalf("read->write err = %v, want ErrPermEscalation", err)
	}
	if _, err := auth.Forward(up, PermWrite); !errors.Is(err, ErrPermEscalation) {
		t.Fatalf("write-only err = %v, want ErrPermEscalation", err)
	}
	if err := auth.Check(derived, PermWrite); !errors.Is(err, ErrPermEscalation) {
		t.Fatalf("Check write on read grant err = %v, want ErrPermEscalation", err)
	}
}

func TestReexportAndMapCannotBeGained(t *testing.T) {
	auth := NewAuthority()
	// Read/write only: no reexport, no map.
	up, err := auth.Issue("buf0", PermRead|PermWrite)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Forwarding itself requires the reexport bit.
	if _, err := auth.Forward(up, PermRead); !errors.Is(err, ErrReexportDenied) {
		t.Fatalf("forward without reexport err = %v, want ErrReexportDenied", err)
	}

	// An upstream that can reexport still cannot hand out mapping rights it
	// does not hold itself.
	fwd, err := auth.Issue("buf0", PermRead|PermReexport)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := auth.Forward(fwd, PermRead|PermMap); !errors.Is(err, ErrPermEscalation) {
		t.Fatalf("gain map err = %v, want ErrPermEscalation", err)
	}
	if _, err := auth.Forward(fwd, PermRead|PermReexport|PermWrite); !errors.Is(err, ErrPermEscalation) {
		t.Fatalf("gain write+reexport err = %v, want ErrPermEscalation", err)
	}
}

func TestCrossLevelReexportChain(t *testing.T) {
	auth := NewAuthority()
	root, err := auth.Issue("buf0", fullPerms())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Level 1 keeps reexport, so it may continue the chain.
	level1, err := auth.Forward(root, PermRead|PermReexport)
	if err != nil {
		t.Fatalf("Forward level1: %v", err)
	}
	// Level 2 drops reexport; the chain must terminate there.
	level2, err := auth.Forward(level1, PermRead)
	if err != nil {
		t.Fatalf("Forward level2: %v", err)
	}
	if _, err := auth.Forward(level2, PermRead); !errors.Is(err, ErrReexportDenied) {
		t.Fatalf("level2 reexport err = %v, want ErrReexportDenied", err)
	}

	// Every derived node stays strictly within its parent's mask.
	if !level1.Perms().IsSubsetOf(root.Perms()) || !level2.Perms().IsSubsetOf(level1.Perms()) {
		t.Fatal("derived grant escaped its parent permission mask")
	}
	if level2.ParentID() != level1.ID() || level1.ParentID() != root.ID() {
		t.Fatal("derived tree parentage broken")
	}
}

func TestRevokeCascadesDownTree(t *testing.T) {
	auth := NewAuthority()
	root, err := auth.Issue("buf0", fullPerms())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	mid, err := auth.Forward(root, PermRead|PermWrite|PermReexport)
	if err != nil {
		t.Fatalf("Forward mid: %v", err)
	}
	leaf, err := auth.Forward(mid, PermRead)
	if err != nil {
		t.Fatalf("Forward leaf: %v", err)
	}
	sibling, err := auth.Forward(root, PermRead|PermReexport)
	if err != nil {
		t.Fatalf("Forward sibling: %v", err)
	}

	report, err := auth.Revoke(mid)
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	wantIDs := map[int64]bool{mid.ID(): true, leaf.ID(): true}
	if len(report.RevokedIDs) != len(wantIDs) {
		t.Fatalf("revoked ids = %v, want %v", report.RevokedIDs, wantIDs)
	}
	for _, id := range report.RevokedIDs {
		if !wantIDs[id] {
			t.Fatalf("unexpected revoked id %d", id)
		}
	}
	if !root.Live() {
		t.Fatal("root grant was revoked outside the target subtree")
	}
	if !sibling.Live() {
		t.Fatal("revocation escaped the target subtree")
	}
	if mid.Live() || leaf.Live() {
		t.Fatal("target subtree still live after revocation")
	}
	if err := auth.Check(leaf, PermRead); !errors.Is(err, ErrGrantRevoked) {
		t.Fatalf("Check revoked err = %v, want ErrGrantRevoked", err)
	}
	if _, err := auth.Forward(leaf, PermRead); !errors.Is(err, ErrGrantRevoked) {
		t.Fatalf("Forward revoked err = %v, want ErrGrantRevoked", err)
	}

	// Repeated revocation is a no-op rather than an error.
	again, err := auth.Revoke(mid)
	if err != nil || len(again.RevokedIDs) != 0 {
		t.Fatalf("repeated Revoke = %+v, %v", again, err)
	}
}
