package handover

import (
	"math/rand"
	"sort"
	"testing"
)

// toPages is the test-only page-by-page expansion used as the reference
// oracle. Production code never enumerates pages; tests deliberately use tiny
// bit fields so exhaustive checks stay cheap.
func toPages(t *testing.T, s IntervalSet) []uint64 {
	t.Helper()
	var out []uint64
	for _, iv := range s {
		for p := iv.Start; p < iv.End; p++ {
			out = append(out, p)
		}
	}
	return out
}

func fromPages(pages []uint64) IntervalSet {
	if len(pages) == 0 {
		return nil
	}
	sorted := append([]uint64(nil), pages...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var out IntervalSet
	start := sorted[0]
	end := sorted[0] + 1
	for _, p := range sorted[1:] {
		if p == end {
			end = p + 1
			continue
		}
		out = append(out, Interval{start, end})
		start, end = p, p+1
	}
	out = append(out, Interval{start, end})
	return out
}

func pageSet(pages ...uint64) IntervalSet { return fromPages(pages) }

func assertPages(t *testing.T, got IntervalSet, want []uint64) {
	t.Helper()
	gotPages := toPages(t, got)
	if len(gotPages) != len(want) {
		t.Fatalf("page set mismatch:\n got %v (pages %v)\nwant pages %v", got, gotPages, want)
	}
	for i := range want {
		if gotPages[i] != want[i] {
			t.Fatalf("page set mismatch:\n got pages %v\nwant pages %v", gotPages, want)
		}
	}
}

// refApply is an independent, straightforward permutation implementation.
func refApply(p Perm, v uint64) uint64 {
	var out uint64
	for src, dst := range p.Map {
		if v&(uint64(1)<<uint(src)) != 0 {
			out |= uint64(1) << dst
		}
	}
	return out | (v &^ ((uint64(1) << p.N) - 1))
}

// refImage expands every page of in (small domains only) and maps it through
// refApply, then re-normalizes.
func refImage(t *testing.T, p Perm, in IntervalSet) IntervalSet {
	t.Helper()
	var mapped []uint64
	for _, page := range toPages(t, in) {
		mapped = append(mapped, refApply(p, page))
	}
	return fromPages(mapped)
}

// refSafety recomputes safe physical pages one page at a time using only the
// exact-tuple + exact-permission invalidation rule.
func refSafety(t *testing.T, m *Manifest, physical IntervalSet) IntervalSet {
	t.Helper()
	var safe []uint64
	for _, page := range toPages(t, physical) {
		blocked := false
		for _, lease := range m.Leases {
			if !lease.Pages.Contains(page) {
				continue
			}
			for _, bit := range []PermBits{PermRead, PermWrite} {
				if lease.Perm&bit == 0 {
					continue
				}
				invalidated := false
				for _, conf := range m.Confirmations {
					if conf.Owner != lease.Owner || conf.Perm&bit == 0 {
						continue
					}
					if conf.Pages.Contains(page) {
						invalidated = true
						break
					}
				}
				if !invalidated {
					blocked = true
				}
			}
		}
		if !blocked {
			safe = append(safe, page)
		}
	}
	return fromPages(safe)
}

func randomPerm(rng *rand.Rand, n uint) Perm {
	m := rng.Perm(int(n))
	out := make([]uint, n)
	for i, v := range m {
		out[i] = uint(v)
	}
	return Perm{N: n, Map: out}
}

func mustNormalize(t *testing.T, ivs []Interval) IntervalSet {
	t.Helper()
	s, err := Normalize(ivs)
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	return s
}

func id(device string, boot BootGen, domain DomainGen, queue string) Identity {
	return Identity{Device: DeviceID(device), Boot: boot, Domain: domain, Queue: QueueID(queue)}
}
