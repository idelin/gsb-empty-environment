package authsolve

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"prescreen.local/ps-e39099b0dc/clockwire"
)

func fixedWindowProblem(offsetLo, offsetHi int64, local [2]int64, windows []Window) Problem {
	return Problem{
		Devices: []Device{
			{ID: "ref", Reference: true},
			{ID: "dev"},
		},
		Credentials: []Credential{{
			ID:        "c1",
			Identity:  "signer-1",
			Device:    "dev",
			Role:      "release",
			NotBefore: local[0],
			NotAfter:  local[1],
		}},
		Policy:  PolicyNode{Role: "release"},
		Windows: windows,
		Clock: []ClockConstraint{{
			Device: "dev",
			Other:  "ref",
			Bound:  clockwire.New(offsetLo, offsetHi),
		}},
		ReferenceDevice: "ref",
	}
}

func TestTwoWindowsAlternate(t *testing.T) {
	p := fixedWindowProblem(0, 1, [2]int64{1, 3}, []Window{
		{Start: 0, End: 1},
		{Start: 2, End: 3},
	})
	w, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if len(w.Signers) != 1 || w.Signers[0] != "signer-1" {
		t.Fatalf("unexpected witness: %#v", w)
	}
	if err := Verify(p, w); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := VerifyProof(p, w.Proof); err != nil {
		t.Fatalf("VerifyProof: %v", err)
	}
}

func TestExactAdjacentWindows(t *testing.T) {
	p := fixedWindowProblem(0, 0, [2]int64{0, 8}, []Window{
		{Start: 4, End: 8},
		{Start: 0, End: 4},
	})
	w, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if err := Verify(p, w); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestPerOffsetChoiceButNoFixedWitness(t *testing.T) {
	p := Problem{
		Devices: []Device{
			{ID: "ref", Reference: true},
			{ID: "dev"},
		},
		Credentials: []Credential{
			{ID: "late", Identity: "late", Device: "dev", Role: "release", NotBefore: 2, NotAfter: 3},
			{ID: "early", Identity: "early", Device: "dev", Role: "release", NotBefore: 1, NotAfter: 2},
		},
		Policy: PolicyNode{Role: "release", Threshold: 1},
		Windows: []Window{
			{Start: 0, End: 1},
			{Start: 2, End: 3},
		},
		Clock: []ClockConstraint{{
			Device: "dev",
			Other:  "ref",
			Bound:  clockwire.New(0, 1),
		}},
		ReferenceDevice: "ref",
	}
	if _, err := Solve(p); !errors.Is(err, ErrNoWitness) || !errors.Is(err, ErrWitnessNotRobust) {
		t.Fatalf("expected ErrNoWitness/ErrWitnessNotRobust, got %v", err)
	}
}

func TestSharedOffset(t *testing.T) {
	p := Problem{
		Devices: []Device{{ID: "ref", Reference: true}},
		Credentials: []Credential{
			{ID: "a", Identity: "a", Device: "ref", Role: "release", NotBefore: 0, NotAfter: 10},
			{ID: "b", Identity: "b", Device: "ref", Role: "release", NotBefore: 4, NotAfter: 8},
		},
		Policy: PolicyNode{Threshold: 1, Children: []PolicyNode{
			{Role: "release"}, {Role: "release"},
		}},
		Windows:         []Window{{Start: 0, End: 5}, {Start: 6, End: 9}},
		ReferenceDevice: "ref",
	}
	w, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if len(w.Signers) != 1 {
		t.Fatalf("expected one signer, got %v", w.Signers)
	}
	if err := Verify(p, w); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestSameDeviceIntervalsMustIntersect(t *testing.T) {
	p := Problem{
		Devices: []Device{{ID: "ref", Reference: true}},
		Credentials: []Credential{
			{ID: "a", Identity: "a", Device: "ref", Role: "r", NotBefore: 0, NotAfter: 2},
			{ID: "b", Identity: "b", Device: "ref", Role: "r", NotBefore: 3, NotAfter: 5},
		},
		Policy: PolicyNode{Threshold: 2, Children: []PolicyNode{
			{Role: "r"}, {Role: "r"},
		}},
		Windows:         []Window{{Start: 0, End: 5}, {Start: 6, End: 7}},
		ReferenceDevice: "ref",
	}
	if _, err := Solve(p); !errors.Is(err, ErrNoWitness) {
		t.Fatalf("disjoint local intervals must not release: %v", err)
	}
}

func TestClockContradictionIsDistinct(t *testing.T) {
	p := fixedWindowProblem(0, 0, [2]int64{0, 10}, []Window{{0, 2}, {4, 6}})
	p.Clock = append(p.Clock,
		ClockConstraint{Device: "dev", Other: "ref", Bound: clockwire.New(1, 2)},
	)
	_, err := Solve(p)
	if !errors.Is(err, ErrClockInconsistent) {
		t.Fatalf("expected ErrClockInconsistent, got %v", err)
	}
}

func TestVerifyRejectsNonRobustWitness(t *testing.T) {
	p := fixedWindowProblem(0, 10, [2]int64{0, 5}, []Window{{0, 1}, {1, 2}})
	w := Witness{
		Signers:     []string{"signer-1"},
		Credentials: []string{"c1"},
	}
	if err := Verify(p, w); !errors.Is(err, ErrWitnessNotRobust) {
		t.Fatalf("expected ErrWitnessNotRobust, got %v", err)
	}
}

func TestProofTamperDetected(t *testing.T) {
	p := fixedWindowProblem(0, 1, [2]int64{1, 3}, []Window{{0, 1}, {2, 3}})
	w, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Proof.Certificates) == 0 {
		t.Fatal("expected certificates")
	}
	w.Proof.Certificates[0].Edges[0].Weight++
	if err := VerifyProof(p, w.Proof); err == nil {
		t.Fatal("tampered proof accepted")
	}
}

func TestSignedOverflowIsDistinct(t *testing.T) {
	p := fixedWindowProblem(0, 0, [2]int64{0, 10}, []Window{{0, 2}, {4, 6}})
	min := int64(-1 << 63)
	p.Clock = append(p.Clock,
		ClockConstraint{Device: "dev", Other: "ref", Bound: clockwire.New(min, 1)},
	)
	_, err := Solve(p)
	if !errors.Is(err, ErrSignedOverflow) || !errors.Is(err, clockwire.ErrOverflow) {
		t.Fatalf("expected ErrSignedOverflow, got %v", err)
	}
}

func TestLargeIntegerDomainWithoutEnumeration(t *testing.T) {
	const base int64 = 1_000_000_000_000
	p := fixedWindowProblem(0, 0, [2]int64{base, base + 10}, []Window{
		{Start: base, End: base + 4},
		{Start: base + 5, End: base + 10},
	})
	w, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if err := VerifyProof(p, w.Proof); err != nil {
		t.Fatalf("VerifyProof: %v", err)
	}
}

// bruteRobust enumerates every integer offset assignment in a finite box.
func bruteRobust(p Problem, selected []Credential, ranges map[string][]int64) bool {
	w1, w2, _ := windowsOf(p)
	devices := make([]string, 0, len(ranges))
	for d := range ranges {
		devices = append(devices, d)
	}
	var walk func(int, map[string]int64) bool
	walk = func(i int, offsets map[string]int64) bool {
		if i == len(devices) {
			for _, c := range selected {
				last := c.NotAfter - 1
				off := offsets[c.Device]
				lo := c.NotBefore - off
				hi := last - off
				_ = lo
				_ = hi
			}
			var lo, hi int64
			first := true
			for _, c := range selected {
				off := offsets[c.Device]
				a := c.NotBefore - off
				b := c.NotAfter - 1 - off
				if first {
					lo, hi, first = a, b, false
				} else {
					if a > lo {
						lo = a
					}
					if b < hi {
						hi = b
					}
				}
			}
			if first {
				return true
			}
			if intersects(lo, hi, w1) || intersects(lo, hi, w2) {
				return true
			}
			return false
		}
		d := devices[i]
		for _, v := range ranges[d] {
			offsets[d] = v
			if !walk(i+1, offsets) {
				return false
			}
		}
		return true
	}
	return walk(0, make(map[string]int64))
}

func intersects(lo, hi int64, w Window) bool {
	return lo <= hi && lo < w.End && hi >= w.Start
}

func TestRobustnessMatchesExhaustiveOffsets(t *testing.T) {
	ranges := map[string][]int64{
		"a": {-2, -1, 0, 1, 2},
		"b": {-1, 0, 1},
	}
	problems := []Problem{
		{
			Devices: []Device{{ID: "ref", Reference: true}, {ID: "a"}, {ID: "b"}},
			Credentials: []Credential{
				{ID: "c1", Identity: "1", Device: "a", Role: "r", NotBefore: -1, NotAfter: 4},
				{ID: "c2", Identity: "2", Device: "b", Role: "r", NotBefore: 1, NotAfter: 6},
			},
			Policy:  PolicyNode{Role: "r", Threshold: 1},
			Windows: []Window{{Start: 0, End: 2}, {Start: 3, End: 5}},
			Clock: []ClockConstraint{
				{Device: "a", Other: "ref", Bound: clockwire.New(-2, 2)},
				{Device: "b", Other: "ref", Bound: clockwire.New(-1, 1)},
				{Device: "a", Other: "b", Bound: clockwire.New(-3, 3)},
			},
		},
		{
			Devices: []Device{{ID: "ref", Reference: true}, {ID: "a"}, {ID: "b"}},
			Credentials: []Credential{
				{ID: "c1", Identity: "1", Device: "a", Role: "r", NotBefore: 0, NotAfter: 3},
				{ID: "c2", Identity: "2", Device: "a", Role: "r", NotBefore: 2, NotAfter: 5},
				{ID: "c3", Identity: "3", Device: "b", Role: "r", NotBefore: 1, NotAfter: 4},
			},
			Policy:  PolicyNode{Role: "r", Threshold: 1},
			Windows: []Window{{Start: -1, End: 1}, {Start: 2, End: 4}},
			Clock: []ClockConstraint{
				{Device: "a", Other: "ref", Bound: clockwire.New(-2, 2)},
				{Device: "b", Other: "ref", Bound: clockwire.New(-1, 1)},
			},
		},
	}
	for pi, p := range problems {
		t.Run(fmt.Sprint(pi), func(t *testing.T) {
			for mask := 0; mask < 1<<len(p.Credentials); mask++ {
				selected := selectCredentials(uint64(mask), p.Credentials)
				got, _, err := witnessRobust(p, selected)
				if err != nil {
					t.Fatalf("mask %d: %v", mask, err)
				}
				want := bruteRobust(p, selected, ranges)
				if got != want {
					t.Fatalf("mask %d: solver robust=%v brute=%v", mask, got, want)
				}
			}
		})
	}
}

func bruteSolve(p Problem, ranges map[string][]int64) (Witness, bool) {
	var best Witness
	have := false
	bestCount := len(p.Credentials) + 1
	for mask := 0; mask < 1<<len(p.Credentials); mask++ {
		selected := selectCredentials(uint64(mask), p.Credentials)
		if bruteDuplicateIdentity(selected) {
			continue
		}
		if len(selected) == 0 {
			continue // the test policies all require one signer
		}
		if !bruteRobust(p, selected, ranges) {
			continue
		}
		w := makeWitness(uint64(mask), p.Credentials, nil)
		count := len(selected)
		if !have || witnessBetter(w, best, count, bestCount) {
			best, have, bestCount = w, true, count
		}
	}
	return best, have
}

func bruteDuplicateIdentity(creds []Credential) bool {
	seen := make(map[string]bool)
	for _, c := range creds {
		if seen[c.Identity] {
			return true
		}
		seen[c.Identity] = true
	}
	return false
}

func TestSolveMatchesExhaustiveSearch(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 40; trial++ {
		aRange := []int64{-2, -1, 0, 1, 2}
		bRange := []int64{-1, 0, 1}
		p := Problem{
			Devices: []Device{{ID: "ref", Reference: true}, {ID: "a"}, {ID: "b"}},
			Policy:  PolicyNode{Role: "r"},
			Clock: []ClockConstraint{
				{Device: "a", Other: "ref", Bound: clockwire.New(aRange[0], aRange[len(aRange)-1])},
				{Device: "b", Other: "ref", Bound: clockwire.New(bRange[0], bRange[len(bRange)-1])},
				{Device: "a", Other: "b", Bound: clockwire.New(-4, 4)},
			},
			ReferenceDevice: "ref",
		}
		// Two disjoint non-empty integer windows in a small time box,
		// including both adjacent and gapped variants.
		points := []int64{-4, -3, -2, -1, 0, 1, 2, 3, 4, 5, 6, 7}
		perm := rng.Perm(len(points))
		chosen := []int64{points[perm[0]], points[perm[1]], points[perm[2]], points[perm[3]]}
		sort.Slice(chosen, func(i, j int) bool { return chosen[i] < chosen[j] })
		p.Windows = []Window{
			{Start: chosen[0], End: chosen[1]},
			{Start: chosen[2], End: chosen[3]},
		}
		for i := 0; i < 4; i++ {
			device := "a"
			if rng.Intn(2) == 0 {
				device = "b"
			}
			nb := int64(rng.Intn(6) - 3)
			length := int64(1 + rng.Intn(4))
			p.Credentials = append(p.Credentials, Credential{
				ID:        fmt.Sprintf("c%d", i),
				Identity:  fmt.Sprintf("s%d", rng.Intn(4)),
				Device:    device,
				Role:      "r",
				NotBefore: nb,
				NotAfter:  nb + length,
			})
		}
		ranges := map[string][]int64{"a": aRange, "b": bRange}
		want, ok := bruteSolve(p, ranges)
		got, err := Solve(p)
		if !ok {
			if err == nil {
				t.Fatalf("trial %d: solver found %v, brute found none", trial, got)
			}
			if !errors.Is(err, ErrNoWitness) {
				t.Fatalf("trial %d: expected ErrNoWitness, got %v", trial, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("trial %d: Solve: %v; brute witness %v", trial, err, want)
		}
		if compareStringSlices(got.Signers, want.Signers) != 0 ||
			compareStringSlices(got.Credentials, want.Credentials) != 0 {
			t.Fatalf("trial %d: solver %v/%v, brute %v/%v",
				trial, got.Signers, got.Credentials, want.Signers, want.Credentials)
		}
	}
}
