package cacheaudit

import (
	"math/rand"
	"testing"
	"time"
)

// TestAuditorMatchesReference cross-validates the memoized linearization
// search against the independent brute-force permutation checker on random
// small histories.
func TestAuditorMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(20260924))
	base := time.Unix(1_700_000_000, 0)
	for iter := 0; iter < 2000; iter++ {
		n := 1 + rng.Intn(7) // 1..7 ops keeps n! tractable
		ops := make([]op, 0, n)
		for i := 0; i < n; i++ {
			start := base.Add(time.Duration(rng.Intn(8)) * time.Second)
			o := op{version: uint64(1 + rng.Intn(3)), start: start}
			if rng.Intn(2) == 0 {
				o.get = true
				o.end = start.Add(time.Duration(rng.Intn(4)) * time.Second)
			} else {
				o.end = start // publish: point event
			}
			ops = append(ops, o)
		}
		got := linearizable(ops)
		want := referenceLinearizable(ops)
		if got != want {
			t.Fatalf("iter %d: auditor=%v reference=%v ops=%+v", iter, got, want, ops)
		}
	}
}

// TestBruteFallbackMatchesReference checks the no-memo fallback path too.
func TestBruteFallbackMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	base := time.Unix(1_700_000_000, 0)
	for iter := 0; iter < 300; iter++ {
		n := 1 + rng.Intn(7)
		ops := make([]op, 0, n)
		for i := 0; i < n; i++ {
			start := base.Add(time.Duration(rng.Intn(6)) * time.Second)
			o := op{version: uint64(1 + rng.Intn(3)), start: start}
			if rng.Intn(2) == 0 {
				o.get = true
				o.end = start.Add(time.Duration(rng.Intn(3)) * time.Second)
			} else {
				o.end = start
			}
			ops = append(ops, o)
		}
		if got, want := bruteLinearizable(ops), referenceLinearizable(ops); got != want {
			t.Fatalf("iter %d: brute=%v reference=%v ops=%+v", iter, got, want, ops)
		}
	}
}

// TestReferenceSanity pins the reference checker itself on tiny known cases.
func TestReferenceSanity(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	s := func(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }

	stale := []op{
		{version: 1, start: s(1), end: s(1)},            // publish v1 @1
		{version: 2, start: s(2), end: s(2)},            // publish v2 @2
		{get: true, version: 1, start: s(3), end: s(4)}, // get -> v1 over [3,4]
	}
	if referenceLinearizable(stale) {
		t.Fatalf("reference accepted a stale read")
	}
	fresh := []op{
		{version: 1, start: s(1), end: s(1)},
		{version: 2, start: s(2), end: s(2)},
		{get: true, version: 2, start: s(3), end: s(4)},
	}
	if !referenceLinearizable(fresh) {
		t.Fatalf("reference rejected a fresh read")
	}
	overlap := []op{
		{version: 1, start: s(1), end: s(1)},
		{get: true, version: 1, start: s(2), end: s(5)},
		{version: 2, start: s(3), end: s(3)},
	}
	if !referenceLinearizable(overlap) {
		t.Fatalf("reference rejected an overlapping (linearizable) read")
	}
}
