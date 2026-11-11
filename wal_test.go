package noncelease

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// frameLen mirrors the on-disk framing so tests can cut writes at byte offsets.
func frameLen(key KeyID, typ byte) int {
	body := 1 + 4 + 2 + len(key)
	if typ == recAllocate {
		body += 8 + 8 + 8
	}
	return headerLen + body + footerLen
}

func newTestAllocator(t *testing.T, disk *crashDisk, opts ...Option) (*Allocator, *crashDisk) {
	t.Helper()
	if disk == nil {
		disk = newCrashDisk()
	}
	store, err := newStore(&crashFile{disk: disk})
	if err != nil {
		t.Fatalf("newStore: %v", err)
	}
	a, err := OpenWithStore(store, opts...)
	if err != nil {
		t.Fatalf("OpenWithStore: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, disk
}

func mustCreate(t *testing.T, a *Allocator, key KeyID) {
	t.Helper()
	if _, err := a.CreateKey(key); err != nil {
		t.Fatalf("CreateKey(%q): %v", key, err)
	}
}

func mustAllocate(t *testing.T, a *Allocator, key KeyID, n uint64) Lease {
	t.Helper()
	l, err := a.Allocate(key, n, time.Minute)
	if err != nil {
		t.Fatalf("Allocate(%q,%d): %v", key, n, err)
	}
	return l
}

// TestTornWriteRecovery tears the next frame at several cut points and also
// drops a write entirely, then restarts the allocator. The torn tail must be
// discarded and retrying the lost allocation must never reuse a counter.
func TestTornWriteRecovery(t *testing.T) {
	key := KeyID("aes-key-1")
	for _, cut := range []int{1, 5, headerLen, headerLen + 3, frameLen(key, recAllocate) - 1} {
		t.Run(fmt.Sprintf("torn-cut-%d", cut), func(t *testing.T) {
			a, disk := newTestAllocator(t, nil)
			mustCreate(t, a, key)
			first := mustAllocate(t, a, key, 100)
			if first.Interval.Start != 0 || first.Interval.End != 100 {
				t.Fatalf("unexpected first interval: %+v", first.Interval)
			}

			// Keep only cut bytes of the next frame on stable storage, then
			// simulate the power loss and reopen.
			disk.armFault(faultTorn, disk.len()+cut)
			if _, err := a.Allocate(key, 50, time.Minute); err == nil {
				t.Fatal("torn Sync should surface as an I/O failure")
			}
			store2, err := reopenCrashStore(disk, a.store)
			if err != nil {
				t.Fatalf("reopen: %v", err)
			}
			a2, err := OpenWithStore(store2)
			if err != nil {
				t.Fatalf("reopen allocator: %v", err)
			}
			high, err := a2.HighWater(key)
			if err != nil {
				t.Fatalf("HighWater: %v", err)
			}
			if high != 100 {
				t.Fatalf("high water after torn write = %d, want 100", high)
			}
			retry := mustAllocate(t, a2, key, 50)
			if retry.Interval.Start != 100 || retry.Interval.End != 150 {
				t.Fatalf("retry interval = %+v, want [100,150)", retry.Interval)
			}
			_ = a2.Close()
		})
	}

	t.Run("lost-write", func(t *testing.T) {
		a, disk := newTestAllocator(t, nil)
		mustCreate(t, a, key)
		mustAllocate(t, a, key, 64)
		disk.armFault(faultLost, 0)
		if _, err := a.Allocate(key, 16, time.Minute); err == nil {
			t.Fatal("lost Sync should surface as an I/O failure")
		}
		store2, err := reopenCrashStore(disk, a.store)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		a2, err := OpenWithStore(store2)
		if err != nil {
			t.Fatalf("reopen allocator: %v", err)
		}
		retry := mustAllocate(t, a2, key, 16)
		if retry.Interval.Start != 64 {
			t.Fatalf("after lost write start = %d, want 64", retry.Interval.Start)
		}
		_ = a2.Close()
	})

	t.Run("torn-full-frame", func(t *testing.T) {
		a, disk := newTestAllocator(t, nil)
		mustCreate(t, a, key)
		mustAllocate(t, a, key, 10)
		// The whole frame reaches storage but one byte inside it is torn:
		// the CRC must reject the frame on replay and truncate it away.
		flen := frameLen(key, recAllocate)
		disk.armFault(faultTorn, disk.len()+flen)
		if _, err := a.Allocate(key, 4, time.Minute); err == nil {
			t.Fatal("torn Sync should surface as an I/O failure")
		}
		store2, err := reopenCrashStore(disk, a.store)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		// Corrupt a byte inside the persisted frame before replay.
		disk.mu.Lock()
		if len(disk.committed) < flen {
			t.Fatalf("committed size %d smaller than frame %d", len(disk.committed), flen)
		}
		disk.committed[len(disk.committed)-2] ^= 0xFF
		disk.mu.Unlock()
		store3, err := reopenCrashStore(disk, store2)
		if err != nil {
			t.Fatalf("reopen after corruption: %v", err)
		}
		a3, err := OpenWithStore(store3)
		if err != nil {
			t.Fatalf("allocator after corruption: %v", err)
		}
		high, _ := a3.HighWater(key)
		if high != 10 {
			t.Fatalf("high water after corrupted frame = %d, want 10", high)
		}
		_ = a3.Close()
	})
}

// TestLeaseRestartAndTTL verifies that leases never cross a restart and that
// TTL expiry fences a lease while the allocator keeps running.
func TestLeaseRestartAndTTL(t *testing.T) {
	key := KeyID("k1")
	base := time.Unix(1_700_000_000, 0)

	a, disk := newTestAllocator(t, nil, WithClock(func() time.Time { return base }))
	mustCreate(t, a, key)
	lease := mustAllocate(t, a, key, 10)
	if err := a.Valid(lease); err != nil {
		t.Fatalf("fresh lease invalid: %v", err)
	}

	// Restart: the durable mark survives, the lease token does not.
	store2, err := reopenCrashStore(disk, a.store)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	a2, err := OpenWithStore(store2, WithClock(func() time.Time { return base }))
	if err != nil {
		t.Fatalf("reopen allocator: %v", err)
	}
	if err := a2.Valid(lease); !errors.Is(err, ErrLeaseInvalid) {
		t.Fatalf("lease after restart err = %v, want ErrLeaseInvalid", err)
	}
	if high, _ := a2.HighWater(key); high != 10 {
		t.Fatal("durable high water did not survive restart")
	}

	// TTL expiry on a fresh instance, using a mutable fake clock.
	mutable := base
	ticking := func() time.Time { return mutable }
	store3, err := reopenCrashStore(disk, store2)
	if err != nil {
		t.Fatalf("second reopen: %v", err)
	}
	a3, err := OpenWithStore(store3, WithClock(ticking))
	if err != nil {
		t.Fatalf("reopen allocator: %v", err)
	}
	ttlLease := mustAllocate(t, a3, key, 5)
	if err := a3.Valid(ttlLease); err != nil {
		t.Fatalf("fresh TTL lease invalid: %v", err)
	}
	mutable = base.Add(2 * time.Minute)
	if err := a3.Valid(ttlLease); !errors.Is(err, ErrLeaseInvalid) {
		t.Fatalf("expired lease err = %v, want ErrLeaseInvalid", err)
	}
	// All tracked leases expired: the tail recycles to zero.
	if high, _ := a3.HighWater(key); high != 0 {
		t.Fatalf("high water after full TTL expiry = %d, want 0", high)
	}
	recycled := mustAllocate(t, a3, key, 7)
	if recycled.Interval.Start != 0 || recycled.Interval.End != 7 {
		t.Fatalf("recycled interval = %+v, want [0,7)", recycled.Interval)
	}

	// A permanent lease (ttl == 0) stays valid and blocks recycling forever.
	forever, err := a3.Allocate(key, 3, 0)
	if err != nil {
		t.Fatalf("permanent allocate: %v", err)
	}
	mutable = base.Add(24 * time.Hour)
	if err := a3.Valid(forever); err != nil {
		t.Fatalf("permanent lease invalid after clock jump: %v", err)
	}
	if high, _ := a3.HighWater(key); high != 10 {
		t.Fatalf("permanent lease should hold watermark, got %d", high)
	}
	_ = a3.Close()
}
