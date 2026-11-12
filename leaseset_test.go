package netsec

import (
	"errors"
	"testing"
	"time"
)

var (
	destA = [32]byte{0x01}
	destB = [32]byte{0x02}
)

func acceptVerify(ls *LeaseSet) error { return nil }

func rejectVerify(ls *LeaseSet) error { return errors.New("bad signature") }

// validLeaseSet builds a LeaseSet that passes every admission rule:
// published at base, 300s lifetime, two non-expired leases.
func validLeaseSet(dest [32]byte, base time.Time) *LeaseSet {
	return &LeaseSet{
		Destination: dest,
		Published:   base,
		Expires:     300 * time.Second,
		Leases: []Lease{
			{Ends: base.Add(100 * time.Second)},
			{Ends: base.Add(200 * time.Second)},
		},
		Raw:       []byte("payload"),
		Signature: []byte("signature"),
	}
}

// TestReplay_OldVersionRejected is the old-version replay scenario: an entry
// whose Published is not strictly newer than the stored one is discarded.
func TestReplay_OldVersionRejected(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	store := NewMemoryStore()

	stored := validLeaseSet(destA, now.Add(-30*time.Second))
	store.Put(stored)

	cases := []struct {
		name      string
		published time.Time
	}{
		{"older published", now.Add(-60 * time.Second)},
		{"equal published", stored.Published},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			incoming := validLeaseSet(destA, tc.published)
			got, err := Admit(incoming, store, acceptVerify, now)
			if !errors.Is(err, ErrReplay) {
				t.Fatalf("want ErrReplay, got err=%v ls=%v", err, got)
			}
			if got != nil {
				t.Fatalf("rejected entry must not be returned, got %+v", got)
			}
		})
	}

	// Strictly newer published for the same destination is accepted.
	newer := validLeaseSet(destA, now.Add(-10*time.Second))
	accepted, err := Admit(newer, store, acceptVerify, now)
	if err != nil {
		t.Fatalf("newer entry should replace the stored one: %v", err)
	}
	if accepted.Published != newer.Published {
		t.Fatalf("accepted published=%s want %s", accepted.Published, newer.Published)
	}

	// No stored entry for another destination means no replay conflict.
	other := validLeaseSet(destB, now.Add(-200*time.Second))
	if _, err := Admit(other, store, acceptVerify, now); err != nil {
		t.Fatalf("entry for a different destination must not hit replay protection: %v", err)
	}
}

// TestFuturePublished_Rejected rejects entries stamped more than 60s into the
// future while accepting the exact 60s boundary.
func TestFuturePublished_Rejected(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	store := NewMemoryStore()

	future := validLeaseSet(destA, now.Add(MaxFutureSkew+time.Second))
	if _, err := Admit(future, store, acceptVerify, now); !errors.Is(err, ErrFuturePublished) {
		t.Fatalf("want ErrFuturePublished, got %v", err)
	}

	boundary := validLeaseSet(destA, now.Add(MaxFutureSkew))
	if _, err := Admit(boundary, store, acceptVerify, now); err != nil {
		t.Fatalf("published exactly now+60s is allowed, got %v", err)
	}
}

// TestExpires_LengthLimits rejects oversized or non-positive Expires offsets
// and accepts the 660s boundary.
func TestExpires_LengthLimits(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	store := NewMemoryStore()

	cases := []struct {
		name    string
		expires time.Duration
	}{
		{"expires over 660s", MaxExpires + time.Second},
		{"expires zero", 0},
		{"expires negative", -time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ls := validLeaseSet(destA, now)
			ls.Expires = tc.expires
			if _, err := Admit(ls, store, acceptVerify, now); !errors.Is(err, ErrExpiresOutOfRange) {
				t.Fatalf("want ErrExpiresOutOfRange, got %v", err)
			}
		})
	}

	boundary := validLeaseSet(destA, now)
	boundary.Expires = MaxExpires
	boundary.Leases = []Lease{{Ends: now.Add(MaxExpires)}}
	if _, err := Admit(boundary, store, acceptVerify, now); err != nil {
		t.Fatalf("expires exactly 660s with lease at the boundary is allowed, got %v", err)
	}
}

// TestOfflineSignature_Timing covers both offline-signature rules: it must
// cover the whole LeaseSet lifetime and must not already be expired.
func TestOfflineSignature_Timing(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	store := NewMemoryStore()

	ls := validLeaseSet(destA, now)
	ls.OfflineSignature = &OfflineSignature{
		Expires:   ls.Expiration().Add(-time.Second),
		Signature: []byte("offline"),
	}
	if _, err := Admit(ls, store, acceptVerify, now); !errors.Is(err, ErrOfflineTooShort) {
		t.Fatalf("want ErrOfflineTooShort, got %v", err)
	}

	// Equal to the LeaseSet expiration is allowed ("not earlier than").
	ls.OfflineSignature.Expires = ls.Expiration()
	if _, err := Admit(ls, store, acceptVerify, now); err != nil {
		t.Fatalf("offline expiration equal to leaseset expiration is allowed, got %v", err)
	}

	// Covers the LeaseSet but has itself already expired.
	expired := validLeaseSet(destB, now)
	expired.OfflineSignature = &OfflineSignature{Expires: now.Add(-time.Second)}
	if _, err := Admit(expired, store, acceptVerify, now); !errors.Is(err, ErrOfflineExpired) {
		t.Fatalf("want ErrOfflineExpired, got %v", err)
	}

	// Fresh and long enough: accepted.
	good := validLeaseSet(destA, now.Add(time.Second))
	good.OfflineSignature = &OfflineSignature{Expires: good.Expiration().Add(time.Hour)}
	if _, err := Admit(good, store, acceptVerify, now); err != nil {
		t.Fatalf("valid offline signature should be accepted, got %v", err)
	}
}

func TestLeaseCount_Limits(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	store := NewMemoryStore()

	empty := validLeaseSet(destA, now)
	empty.Leases = nil
	if _, err := Admit(empty, store, acceptVerify, now); !errors.Is(err, ErrLeaseCount) {
		t.Fatalf("want ErrLeaseCount for zero leases, got %v", err)
	}

	tooMany := validLeaseSet(destA, now)
	tooMany.Leases = make([]Lease, MaxLeases+1)
	for i := range tooMany.Leases {
		tooMany.Leases[i] = Lease{Ends: now.Add(time.Duration(i+1) * time.Second)}
	}
	if _, err := Admit(tooMany, store, acceptVerify, now); !errors.Is(err, ErrLeaseCount) {
		t.Fatalf("want ErrLeaseCount for %d leases, got %v", MaxLeases+1, err)
	}

	exactMax := validLeaseSet(destB, now)
	exactMax.Leases = make([]Lease, MaxLeases)
	for i := range exactMax.Leases {
		exactMax.Leases[i] = Lease{Ends: now.Add(time.Duration(i+1) * time.Second)}
	}
	if _, err := Admit(exactMax, store, acceptVerify, now); err != nil {
		t.Fatalf("exactly %d leases are allowed, got %v", MaxLeases, err)
	}
}

func TestLease_EndsBeyondExpiration(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	store := NewMemoryStore()

	ls := validLeaseSet(destA, now)
	ls.Leases = []Lease{{Ends: ls.Expiration().Add(time.Second)}}
	if _, err := Admit(ls, store, acceptVerify, now); !errors.Is(err, ErrLeaseBeyondExpiry) {
		t.Fatalf("want ErrLeaseBeyondExpiry, got %v", err)
	}

	// A lease ending exactly at Published+Expires is permitted.
	boundary := validLeaseSet(destA, now)
	boundary.Leases = []Lease{{Ends: boundary.Expiration()}}
	if _, err := Admit(boundary, store, acceptVerify, now); err != nil {
		t.Fatalf("lease ending exactly at expiration is allowed, got %v", err)
	}
}

func TestAdmit_ExpiredLeasesFiltered(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	store := NewMemoryStore()

	ls := validLeaseSet(destA, now)
	ls.Leases = []Lease{
		{Ends: now.Add(-time.Second)}, // expired, must be dropped
		{Ends: now},                   // exactly now: still valid
		{Ends: now.Add(10 * time.Second)},
	}
	originalCount := len(ls.Leases)

	accepted, err := Admit(ls, store, acceptVerify, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(accepted.Leases) != 2 {
		t.Fatalf("want 2 surviving leases, got %d: %+v", len(accepted.Leases), accepted.Leases)
	}
	for _, lease := range accepted.Leases {
		if lease.Ends.Before(now) {
			t.Fatalf("expired lease survived filtering: %+v", lease)
		}
	}

	// The caller's input must not be mutated.
	if len(ls.Leases) != originalCount {
		t.Fatalf("input leases were mutated: have %d want %d", len(ls.Leases), originalCount)
	}

	// An entry whose leases are all expired is still admitted with an empty
	// lease slice (structural validation happens before filtering).
	allExpired := validLeaseSet(destB, now)
	allExpired.Leases = []Lease{{Ends: now.Add(-time.Second)}}
	accepted, err = Admit(allExpired, store, acceptVerify, now)
	if err != nil {
		t.Fatalf("all-expired leases should be filtered, not rejected: %v", err)
	}
	if len(accepted.Leases) != 0 {
		t.Fatalf("want empty surviving leases, got %+v", accepted.Leases)
	}
}

func TestAdmit_SignatureVerifierInjected(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	ls := validLeaseSet(destA, now)

	if _, err := Admit(ls, store, rejectVerify, now); !errors.Is(err, ErrSignature) {
		t.Fatalf("want ErrSignature, got %v", err)
	}

	var seen *LeaseSet
	captureVerify := func(in *LeaseSet) error {
		seen = in
		return nil
	}
	if _, err := Admit(ls, store, captureVerify, now); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seen != ls {
		t.Fatal("verifier did not receive the submitted leaseset")
	}

	if _, err := Admit(nil, store, acceptVerify, now); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil leaseset want ErrInvalidArgument, got %v", err)
	}
	if _, err := Admit(ls, store, nil, now); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil verifier want ErrInvalidArgument, got %v", err)
	}
}

func TestMemoryStore_DefensiveCopy(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	ls := validLeaseSet(destA, now)
	store.Put(ls)

	ls.Leases[0].TunnelID = 4242
	ls.Published = now.Add(time.Hour)

	got, ok := store.GetLeaseSet(destA)
	if !ok {
		t.Fatal("stored entry not found")
	}
	if got.Published.Equal(now.Add(time.Hour)) {
		t.Fatal("store must keep its own copy of the leaseset")
	}
	if got.Leases[0].TunnelID == 4242 {
		t.Fatal("store must keep its own copy of the leases")
	}
}
