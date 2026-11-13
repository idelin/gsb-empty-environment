package vbmeta

import (
	"errors"
	"testing"
)

var (
	rootOfTrust = Digest{0xAA}
	otherKey    = Digest{0xBB}
	chainKey    = Digest{0xCC}
)

func goodHeader() Header {
	return Header{
		Magic:           Magic,
		RollbackIndex:   5,
		PublicKeyDigest: rootOfTrust,
	}
}

func goodChain() []ChainDescriptor {
	return []ChainDescriptor{
		{PartitionName: "boot", PublicKeyDigest: chainKey, RollbackIndexLocation: 1},
		{PartitionName: "vendor", PublicKeyDigest: chainKey, RollbackIndexLocation: 2},
	}
}

func TestAdmitSuccessAdvancesRollback(t *testing.T) {
	dev := &Device{Locked: true, Rollback: map[uint32]uint64{0: 3}}
	if err := dev.Admit(goodHeader(), goodChain(), rootOfTrust); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if got := dev.Rollback[0]; got != 5 {
		t.Fatalf("stored rollback = %d, want 5", got)
	}
}

func TestAdmitSuccessKeepsHigherStoredValue(t *testing.T) {
	dev := &Device{Locked: true, Rollback: map[uint32]uint64{0: 5}}
	if err := dev.Admit(goodHeader(), goodChain(), rootOfTrust); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if got := dev.Rollback[0]; got != 5 {
		t.Fatalf("stored rollback = %d, want 5", got)
	}
}

func TestBadMagicRejected(t *testing.T) {
	dev := &Device{Locked: true}
	h := goodHeader()
	h.Magic = "AVB1"
	if err := dev.Admit(h, goodChain(), rootOfTrust); !errors.Is(err, ErrBadMagic) {
		t.Fatalf("err = %v, want ErrBadMagic", err)
	}
}

func TestOldRollbackRejected(t *testing.T) {
	dev := &Device{Locked: true, Rollback: map[uint32]uint64{0: 10}}
	if err := dev.Admit(goodHeader(), goodChain(), rootOfTrust); !errors.Is(err, ErrRollback) {
		t.Fatalf("err = %v, want ErrRollback", err)
	}
	if got := dev.Rollback[0]; got != 10 {
		t.Fatalf("stored rollback = %d, want 10 (must not change)", got)
	}
}

func TestOldRollbackRejectedEvenWhenUnlocked(t *testing.T) {
	dev := &Device{Locked: false, Rollback: map[uint32]uint64{0: 10}}
	if err := dev.Admit(goodHeader(), goodChain(), rootOfTrust); !errors.Is(err, ErrRollback) {
		t.Fatalf("err = %v, want ErrRollback", err)
	}
}

func TestLockedDisableFlagsRejected(t *testing.T) {
	for _, flag := range []uint32{FlagHashtreeDisabled, FlagVerificationDisabled, 0x3} {
		dev := &Device{Locked: true, Rollback: map[uint32]uint64{0: 1}}
		h := goodHeader()
		h.Flags = flag
		if err := dev.Admit(h, goodChain(), rootOfTrust); !errors.Is(err, ErrDisableFlagLocked) {
			t.Fatalf("flags=0x%x: err = %v, want ErrDisableFlagLocked", flag, err)
		}
		if got := dev.Rollback[0]; got != 1 {
			t.Fatalf("flags=0x%x: stored rollback = %d, want 1 (must not change)", flag, got)
		}
	}
}

func TestLockedUntrustedKeyRejected(t *testing.T) {
	dev := &Device{Locked: true}
	h := goodHeader()
	h.PublicKeyDigest = otherKey
	if err := dev.Admit(h, goodChain(), rootOfTrust); !errors.Is(err, ErrUntrustedKey) {
		t.Fatalf("err = %v, want ErrUntrustedKey", err)
	}
}

func TestDuplicateLocationRejected(t *testing.T) {
	dev := &Device{Locked: true}
	chain := []ChainDescriptor{
		{PartitionName: "boot", PublicKeyDigest: chainKey, RollbackIndexLocation: 1},
		{PartitionName: "vendor", PublicKeyDigest: chainKey, RollbackIndexLocation: 1},
	}
	if err := dev.Admit(goodHeader(), chain, rootOfTrust); !errors.Is(err, ErrLocationDuplicated) {
		t.Fatalf("err = %v, want ErrLocationDuplicated", err)
	}
}

func TestLocationZeroRejected(t *testing.T) {
	dev := &Device{Locked: true}
	chain := []ChainDescriptor{
		{PartitionName: "boot", PublicKeyDigest: chainKey, RollbackIndexLocation: 0},
	}
	if err := dev.Admit(goodHeader(), chain, rootOfTrust); !errors.Is(err, ErrLocationZero) {
		t.Fatalf("err = %v, want ErrLocationZero", err)
	}
}

// A failure in a later chain entry must not advance the stored rollback
// value, even though the header's rollback index was newer.
func TestLaterChainFailureDoesNotAdvance(t *testing.T) {
	dev := &Device{Locked: true, Rollback: map[uint32]uint64{0: 1}}
	chain := []ChainDescriptor{
		{PartitionName: "boot", PublicKeyDigest: chainKey, RollbackIndexLocation: 1},
		{PartitionName: "vendor", PublicKeyDigest: chainKey, RollbackIndexLocation: 0},
	}
	if err := dev.Admit(goodHeader(), chain, rootOfTrust); !errors.Is(err, ErrLocationZero) {
		t.Fatalf("err = %v, want ErrLocationZero", err)
	}
	if got := dev.Rollback[0]; got != 1 {
		t.Fatalf("stored rollback = %d, want 1 (must not be advanced)", got)
	}
}

func TestUnlockedDeviceAcceptsForeignKeyAndDisableFlags(t *testing.T) {
	dev := &Device{Locked: false, Rollback: map[uint32]uint64{0: 1}}
	h := goodHeader()
	h.PublicKeyDigest = otherKey
	h.Flags = FlagHashtreeDisabled | FlagVerificationDisabled
	if err := dev.Admit(h, goodChain(), rootOfTrust); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if got := dev.Rollback[0]; got != 5 {
		t.Fatalf("stored rollback = %d, want 5", got)
	}
}
