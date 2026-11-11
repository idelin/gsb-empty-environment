package netsec

import (
	"fmt"
	"math/big"
	"math/rand/v2"
	"slices"
	"testing"
)

func tenantSpec() Spec {
	return Spec{Segments: []Segment{
		{Name: "tenant", Domain: []string{"t1", "t2", "t3", "t9"}},
		{Name: "region", Domain: []string{"cn", "eu", "us"}},
		{Name: "kind", Domain: []string{"bucket", "queue"}},
	}}
}

func TestCheckLeakWitness(t *testing.T) {
	pol := Policy{
		Allow: []Pattern{
			{Segments: []SegmentFilter{
				{Allow: []string{Wildcard}, Deny: []string{"t1"}},
				{Allow: []string{Wildcard}},
				{Allow: []string{Wildcard}},
			}},
		},
		Deny: []Pattern{
			{Segments: []SegmentFilter{
				{Allow: []string{"t9"}},
				{Allow: []string{"us"}},
				{Allow: []string{Wildcard}},
			}},
		},
	}
	forbidden := []Pattern{{Segments: []SegmentFilter{
		{Allow: []string{"t9"}},
		{Allow: []string{Wildcard}},
		{Allow: []string{Wildcard}},
	}}}

	res, err := Check(tenantSpec(), pol, forbidden)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !res.Leaked {
		t.Fatal("expected leak for tenant t9")
	}
	// t9 visible in cn/eu across both kinds: 2*2 = 4 tuples.
	if want := big.NewInt(4); res.Count.Cmp(want) != 0 {
		t.Fatalf("count = %v, want %v", res.Count, want)
	}
	if want := []string{"t9", "cn", "bucket"}; !slices.Equal(res.Witness, want) {
		t.Fatalf("witness = %v, want %v", res.Witness, want)
	}
}

func TestCheckDenyPriority(t *testing.T) {
	pol := Policy{
		Allow: []Pattern{{Segments: []SegmentFilter{
			{Allow: []string{Wildcard}},
			{Allow: []string{Wildcard}},
			{Allow: []string{Wildcard}},
		}}},
		Deny: []Pattern{{Segments: []SegmentFilter{
			{Allow: []string{"t9"}},
			{Allow: []string{Wildcard}},
			{Allow: []string{Wildcard}},
		}}},
	}
	forbidden := []Pattern{{Segments: []SegmentFilter{
		{Allow: []string{"t9"}},
		{Allow: []string{Wildcard}},
		{Allow: []string{Wildcard}},
	}}}

	res, err := Check(tenantSpec(), pol, forbidden)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Leaked {
		t.Fatalf("explicit deny must win, got witness %v", res.Witness)
	}
	if res.Count.Sign() != 0 {
		t.Fatalf("count = %v, want 0", res.Count)
	}
}

func TestCheckAllowUnion(t *testing.T) {
	pol := Policy{
		Allow: []Pattern{
			{Segments: []SegmentFilter{
				{Allow: []string{"t1"}},
				{Allow: []string{"cn"}},
				{Allow: []string{"bucket"}},
			}},
			{Segments: []SegmentFilter{
				{Allow: []string{"t9"}},
				{Allow: []string{"eu"}},
				{Allow: []string{"queue"}},
			}},
		},
	}
	forbidden := []Pattern{{Segments: []SegmentFilter{
		{Allow: []string{"t9"}},
		{Allow: []string{Wildcard}},
		{Allow: []string{Wildcard}},
	}}}

	res, err := Check(tenantSpec(), pol, forbidden)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if want := []string{"t9", "eu", "queue"}; !slices.Equal(res.Witness, want) {
		t.Fatalf("witness = %v, want %v", res.Witness, want)
	}
	if want := big.NewInt(1); res.Count.Cmp(want) != 0 {
		t.Fatalf("count = %v, want %v", res.Count, want)
	}
}

func TestCheckHugeDomainNotEnumerated(t *testing.T) {
	const n = 2000
	mkDomain := func(prefix string) []string {
		d := make([]string, n)
		for i := range d {
			d[i] = fmt.Sprintf("%s%04d", prefix, i)
		}
		return d
	}
	spec := Spec{Segments: []Segment{
		{Name: "tenant", Domain: mkDomain("tn")},
		{Name: "region", Domain: mkDomain("rg")},
		{Name: "kind", Domain: mkNameList()},
	}}
	pol := Policy{
		Allow: []Pattern{{Segments: []SegmentFilter{
			{Allow: []string{Wildcard}},
			{Allow: []string{Wildcard}},
			{Allow: []string{Wildcard}},
		}}},
		Deny: []Pattern{{Segments: []SegmentFilter{
			{Allow: []string{"tn1999"}},
			{Allow: []string{Wildcard}},
			{Allow: []string{Wildcard}},
		}}},
	}
	forbidden := []Pattern{{Segments: []SegmentFilter{
		{Allow: []string{"tn0000", "tn1999"}},
		{Allow: []string{"rg0007"}},
		{Allow: []string{Wildcard}},
	}}}

	res, err := Check(spec, pol, forbidden)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	// Only tn0000 x rg0007 leaks (tn1999 denied): 1*1*2 kinds.
	if want := big.NewInt(2); res.Count.Cmp(want) != 0 {
		t.Fatalf("count = %v, want %v", res.Count, want)
	}
	if want := []string{"tn0000", "rg0007", "bucket"}; !slices.Equal(res.Witness, want) {
		t.Fatalf("witness = %v, want %v", res.Witness, want)
	}
}

func mkNameList() []string { return []string{"bucket", "queue"} }

func TestCheckErrors(t *testing.T) {
	spec := tenantSpec()
	cases := []struct {
		name string
		spec Spec
		pol  Policy
	}{
		{"empty spec", Spec{}, Policy{}},
		{"empty domain", Spec{Segments: []Segment{{Name: "x"}}}, Policy{}},
		{"dup value", Spec{Segments: []Segment{{Name: "x", Domain: []string{"a", "a"}}}}, Policy{}},
		{"unknown allow", spec, Policy{Allow: []Pattern{{Segments: []SegmentFilter{
			{Allow: []string{"nope"}}, {Allow: []string{Wildcard}}, {Allow: []string{Wildcard}}}}}}},
		{"bad arity", spec, Policy{Allow: []Pattern{{Segments: []SegmentFilter{
			{Allow: []string{Wildcard}}}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Check(tc.spec, tc.pol, nil); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

// TestCheckRandomizedCrossCheck compares Check against an independent
// brute-force implementation over many random small policies.
func TestCheckRandomizedCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewPCG(27, 7))
	for trial := 0; trial < 1000; trial++ {
		k := 1 + rng.IntN(3)
		spec := Spec{Segments: make([]Segment, k)}
		for i := range spec.Segments {
			d := 1 + rng.IntN(4)
			seg := Segment{Name: fmt.Sprintf("s%d", i), Domain: make([]string, d)}
			for j := range seg.Domain {
				seg.Domain[j] = fmt.Sprintf("v%d", j)
			}
			spec.Segments[i] = seg
		}
		randPattern := func() Pattern {
			p := Pattern{Segments: make([]SegmentFilter, k)}
			for i, seg := range spec.Segments {
				for _, v := range seg.Domain {
					if rng.IntN(2) == 0 {
						p.Segments[i].Allow = append(p.Segments[i].Allow, v)
					}
					if rng.IntN(4) == 0 {
						p.Segments[i].Deny = append(p.Segments[i].Deny, v)
					}
				}
				if rng.IntN(3) == 0 {
					p.Segments[i].Allow = append(p.Segments[i].Allow, Wildcard)
				}
				if rng.IntN(6) == 0 {
					p.Segments[i].Deny = append(p.Segments[i].Deny, Wildcard)
				}
			}
			return p
		}
		var pol Policy
		for i := 0; i < 1+rng.IntN(3); i++ {
			pol.Allow = append(pol.Allow, randPattern())
		}
		for i := 0; i < rng.IntN(3); i++ {
			pol.Deny = append(pol.Deny, randPattern())
		}
		var forbidden []Pattern
		for i := 0; i < 1+rng.IntN(2); i++ {
			forbidden = append(forbidden, randPattern())
		}

		res, err := Check(spec, pol, forbidden)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}

		domIndex, _ := validateSpec(spec)
		allowSets := compileAll(t, spec, domIndex, pol.Allow)
		denySets := compileAll(t, spec, domIndex, pol.Deny)
		forbSets := compileAll(t, spec, domIndex, forbidden)

		idx := make([]int, k)
		count := int64(0)
		var first []string
		for {
			vis := matchesAny(allowSets, idx) && !matchesAny(denySets, idx)
			if vis && matchesAny(forbSets, idx) {
				count++
				if first == nil {
					first = tupleStrings(spec, idx)
				}
			}
			if !nextTuple(spec, idx) {
				break
			}
		}
		if res.Count.Cmp(big.NewInt(count)) != 0 {
			t.Fatalf("trial %d: count = %v, brute force %d", trial, res.Count, count)
		}
		if (count > 0) != res.Leaked {
			t.Fatalf("trial %d: leaked = %v, brute force %v", trial, res.Leaked, count > 0)
		}
		if !slices.Equal(first, res.Witness) {
			t.Fatalf("trial %d: witness = %v, brute force %v", trial, res.Witness, first)
		}
	}
}

func compileAll(t *testing.T, spec Spec, domIndex []map[string]int, ps []Pattern) [][]map[int]bool {
	t.Helper()
	out := make([][]map[int]bool, len(ps))
	for i, p := range ps {
		sets, err := resolvePattern(spec, domIndex, p, "test")
		if err != nil {
			t.Fatal(err)
		}
		out[i] = sets
	}
	return out
}
