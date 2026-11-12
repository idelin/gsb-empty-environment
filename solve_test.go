package netsec

import (
	"errors"
	"testing"
)

func sigIDs(ws []Witness) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.SignatureID
	}
	return out
}

func equalSet(got []Witness, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	gotIDs := sigIDs(got)
	for i := range want {
		if gotIDs[i] != want[i] {
			return false
		}
	}
	return true
}

// baseInput builds four independent organizations, each with one
// certificate and one signature, spread over two regions and two categories.
func baseInput() Input {
	return Input{
		Organizations: []Organization{
			{ID: "o1", Region: "us", Category: "ca"},
			{ID: "o2", Region: "us", Category: "notary"},
			{ID: "o3", Region: "eu", Category: "ca"},
			{ID: "o4", Region: "eu", Category: "notary"},
		},
		Certificates: []Certificate{
			{ID: "c1", Holder: "o1"},
			{ID: "c2", Holder: "o2"},
			{ID: "c3", Holder: "o3"},
			{ID: "c4", Holder: "o4"},
		},
		Signatures: []Signature{
			{SignatureID: "s1", CertificateID: "c1"},
			{SignatureID: "s2", CertificateID: "c2"},
			{SignatureID: "s3", CertificateID: "c3"},
			{SignatureID: "s4", CertificateID: "c4"},
		},
		Thresholds: Thresholds{MinWitnesses: 2},
	}
}

func TestMinimalWitnessSet(t *testing.T) {
	in := baseInput()
	res, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if len(res.Witnesses) != 2 {
		t.Fatalf("want 2 witnesses, got %d (%v)", len(res.Witnesses), sigIDs(res.Witnesses))
	}
	// Deterministic: two smallest domain IDs picked first.
	if got := sigIDs(res.Witnesses); got[0] != "s1" || got[1] != "s2" {
		t.Fatalf("unexpected deterministic pick: %v", got)
	}
}

func TestControlChainMergesDomain(t *testing.T) {
	in := baseInput()
	// A single control chain over all four organizations collapses them into
	// one domain, so two independent witnesses are impossible.
	in.ControlEdges = []ControlEdge{
		{From: "o1", To: "o2"},
		{From: "o2", To: "o3"},
		{From: "o3", To: "o4"},
	}
	if _, err := Solve(in); !errors.Is(err, ErrNoQualifiedSet) {
		t.Fatalf("want ErrNoQualifiedSet, got %v", err)
	}

	// One independent witness should still work with threshold 1.
	in.Thresholds = Thresholds{MinWitnesses: 1}
	res, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if len(res.Witnesses) != 1 {
		t.Fatalf("want 1 witness, got %d", len(res.Witnesses))
	}
	if res.ControlDomain["o1"] != res.ControlDomain["o3"] {
		t.Fatalf("o1 and o3 must share a control domain: %v", res.ControlDomain)
	}
	if res.ControlDomain["o4"] != res.ControlDomain["o1"] {
		t.Fatalf("o4 must be merged into the chain domain: %v", res.ControlDomain)
	}

	// With o4 left outside the chain, two independent domains exist and the
	// threshold of two becomes satisfiable again.
	in.ControlEdges = in.ControlEdges[:2]
	in.Thresholds = Thresholds{MinWitnesses: 2}
	res, err = Solve(in)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if len(res.Witnesses) != 2 {
		t.Fatalf("want 2 independent witnesses, got %d", len(res.Witnesses))
	}
}

func TestDiamondAndCycleMergesDomain(t *testing.T) {
	in := baseInput()
	// Diamond o1->{o2,o3}->o4 plus a reverse edge forming a cycle: all merge.
	in.ControlEdges = []ControlEdge{
		{From: "o1", To: "o2"},
		{From: "o1", To: "o3"},
		{From: "o2", To: "o4"},
		{From: "o3", To: "o4"},
		{From: "o4", To: "o1"},
	}
	in.Thresholds = Thresholds{MinWitnesses: 2}
	if _, err := Solve(in); !errors.Is(err, ErrNoQualifiedSet) {
		t.Fatalf("want ErrNoQualifiedSet, got %v", err)
	}

	res, err := Solve(Input{
		Organizations: in.Organizations,
		Certificates:  in.Certificates,
		ControlEdges:  in.ControlEdges,
		Signatures:    in.Signatures,
		Thresholds:    Thresholds{MinWitnesses: 1},
	})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	domain := res.ControlDomain["o1"]
	for _, id := range []string{"o2", "o3", "o4"} {
		if res.ControlDomain[id] != domain {
			t.Fatalf("%s not merged into diamond domain %s", id, domain)
		}
	}
}

func TestCertificateAliasesCountOnce(t *testing.T) {
	in := baseInput()
	// o1 holds a second certificate c1b producing signature s1b. It is an
	// alias of c1 inside the same domain and must not add a second vote.
	in.Certificates = append(in.Certificates, Certificate{ID: "c1b", Holder: "o1"})
	in.Signatures = append(in.Signatures, Signature{SignatureID: "s1b", CertificateID: "c1b"})
	in.Thresholds = Thresholds{MinWitnesses: 2}

	res, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if len(res.Witnesses) != 2 {
		t.Fatalf("want 2 witnesses, got %d", len(res.Witnesses))
	}
	for _, w := range res.Witnesses {
		if w.ControlDomain == res.ControlDomain["o1"] && w.SignatureID == "s1b" {
			t.Fatalf("alias s1b must not replace or duplicate s1 in minimal set")
		}
	}

	// Five signatures but only four control domains, so threshold 5 is
	// impossible even though two signatures come from o1.
	in.Thresholds = Thresholds{MinWitnesses: 5}
	if _, err := Solve(in); !errors.Is(err, ErrNoQualifiedSet) {
		t.Fatalf("want ErrNoQualifiedSet, got %v", err)
	}
}

func TestCrossControlCertificatesMerged(t *testing.T) {
	in := baseInput()
	// o2 is controlled by o1. A certificate on o2 must not count as an
	// independent party from o1, even though it is a separate organization
	// with its own certificate and signature.
	in.ControlEdges = []ControlEdge{{From: "o1", To: "o2"}}
	in.Thresholds = Thresholds{
		MinWitnesses:  3,
		MinRegions:    2,
		MinCategories: 2,
	}
	// Domains: {o1,o2}, {o3}, {o4} => three independent domains available.
	res, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if len(res.Witnesses) != 3 {
		t.Fatalf("want 3 witnesses, got %d", len(res.Witnesses))
	}
	domains := map[string]bool{}
	for _, w := range res.Witnesses {
		if domains[w.ControlDomain] {
			t.Fatalf("domain %s counted twice", w.ControlDomain)
		}
		domains[w.ControlDomain] = true
	}
}

func TestRegionCategoryThresholds(t *testing.T) {
	in := baseInput()
	in.Thresholds = Thresholds{
		MinWitnesses:  3,
		MinRegions:    2,
		MinCategories: 2,
		MinPairs: map[string]int{
			"us|ca":     1,
			"eu|notary": 1,
		},
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if len(res.Witnesses) != 3 {
		t.Fatalf("want 3 witnesses, got %d", len(res.Witnesses))
	}
	regions, categories, pairs := map[string]bool{}, map[string]bool{}, map[string]int{}
	for _, w := range res.Witnesses {
		regions[w.Region] = true
		categories[w.Category] = true
		pairs[pairKey(w.Region, w.Category)]++
	}
	if len(regions) < 2 || len(categories) < 2 {
		t.Fatalf("region/category coverage missing: %v", res.Witnesses)
	}
	if pairs["us|ca"] < 1 || pairs["eu|notary"] < 1 {
		t.Fatalf("required pairs missing: %v", pairs)
	}
}

func TestImpossiblePairThreshold(t *testing.T) {
	in := baseInput()
	in.Thresholds = Thresholds{
		MinWitnesses: 2,
		MinPairs:     map[string]int{"ap|ca": 2},
	}
	if _, err := Solve(in); !errors.Is(err, ErrNoQualifiedSet) {
		t.Fatalf("want ErrNoQualifiedSet, got %v", err)
	}
}

func TestCapEnforcedAndTwentyWitnesses(t *testing.T) {
	in := Input{Thresholds: Thresholds{MinWitnesses: 21}}
	for i := 0; i < 21; i++ {
		id := string(rune('a' + i))
		in.Organizations = append(in.Organizations,
			Organization{ID: id, Region: "r", Category: "c"})
		in.Certificates = append(in.Certificates, Certificate{ID: id, Holder: id})
		in.Signatures = append(in.Signatures, Signature{SignatureID: id, CertificateID: id})
	}
	// 21 independent domains exist, but the protocol caps sets at 20.
	if _, err := Solve(in); !errors.Is(err, ErrNoQualifiedSet) {
		t.Fatalf("want ErrNoQualifiedSet, got %v", err)
	}

	in.Thresholds = Thresholds{MinWitnesses: 20}
	res, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if len(res.Witnesses) != 20 {
		t.Fatalf("want 20 witnesses, got %d", len(res.Witnesses))
	}

	if _, err := Solve(Input{
		Organizations: in.Organizations,
		Certificates:  in.Certificates,
		Signatures:    in.Signatures,
		Thresholds:    Thresholds{MinWitnesses: 1},
		MaxWitnesses:  21,
	}); !errors.Is(err, ErrNoQualifiedSet) {
		t.Fatalf("MaxWitnesses above 20 must be rejected")
	}
}

func TestUnknownReferencesAndDuplicates(t *testing.T) {
	in := baseInput()
	in.Signatures[0].CertificateID = "ghost"
	if _, err := Solve(in); !errors.Is(err, ErrUnknownReference) {
		t.Fatalf("want ErrUnknownReference, got %v", err)
	}

	in = baseInput()
	in.Signatures = append(in.Signatures, Signature{SignatureID: "s1", CertificateID: "c2"})
	if _, err := Solve(in); !errors.Is(err, ErrDuplicateSignature) {
		t.Fatalf("want ErrDuplicateSignature, got %v", err)
	}

	in = baseInput()
	in.ControlEdges = []ControlEdge{{From: "o1", To: "ghost"}}
	if _, err := Solve(in); !errors.Is(err, ErrUnknownReference) {
		t.Fatalf("want ErrUnknownReference for edge, got %v", err)
	}

	if _, err := Solve(Input{}); !errors.Is(err, ErrEmptyInput) {
		t.Fatalf("want ErrEmptyInput, got %v", err)
	}
}

func TestZeroThresholdReturnsEmptySet(t *testing.T) {
	in := baseInput()
	in.Thresholds = Thresholds{}
	res, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if len(res.Witnesses) != 0 {
		t.Fatalf("want empty witness set, got %v", res.Witnesses)
	}
	if res.ControlDomain["o1"] == "" {
		t.Fatalf("control-domain map should still be populated")
	}
}

// TestEnumerationStaysBounded builds 25 independent domains but requires 20
// witnesses spread over regions/categories, exercising the subset enumeration
// near the protocol cap while asserting it stays well inside the budget.
func TestEnumerationStaysBounded(t *testing.T) {
	in := Input{
		Thresholds: Thresholds{
			MinWitnesses:  20,
			MinRegions:    3,
			MinCategories: 2,
		},
	}
	regions := []string{"us", "eu", "ap"}
	categories := []string{"ca", "notary"}
	for i := 0; i < 25; i++ {
		id := fmtID(i)
		in.Organizations = append(in.Organizations, Organization{
			ID:       id,
			Region:   regions[i%len(regions)],
			Category: categories[i%len(categories)],
		})
		in.Certificates = append(in.Certificates, Certificate{ID: id, Holder: id})
		in.Signatures = append(in.Signatures, Signature{SignatureID: id, CertificateID: id})
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if len(res.Witnesses) != 20 {
		t.Fatalf("want 20 witnesses, got %d", len(res.Witnesses))
	}
	if res.EnumVisited > enumBudget {
		t.Fatalf("enumeration exceeded budget: %d", res.EnumVisited)
	}
	regionSeen, categorySeen := map[string]bool{}, map[string]bool{}
	for _, w := range res.Witnesses {
		regionSeen[w.Region] = true
		categorySeen[w.Category] = true
	}
	if len(regionSeen) < 3 || len(categorySeen) < 2 {
		t.Fatalf("coverage not met: regions=%v categories=%v", regionSeen, categorySeen)
	}
}

func fmtID(i int) string {
	return "org" + string(rune('a'+i/26)) + string(rune('a'+i%26))
}

func TestResultIsDeterministic(t *testing.T) {
	first, err := Solve(baseInput())
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := Solve(baseInput())
		if err != nil {
			t.Fatalf("Solve: %v", err)
		}
		if !equalSet(again.Witnesses, sigIDs(first.Witnesses)) {
			t.Fatalf("non-deterministic result: %v vs %v",
				sigIDs(again.Witnesses), sigIDs(first.Witnesses))
		}
	}

	// Output must be sorted by SignatureID.
	ids := sigIDs(first.Witnesses)
	for i := 1; i < len(ids); i++ {
		if ids[i-1] > ids[i] {
			t.Fatalf("witnesses not sorted: %v", ids)
		}
	}
}
