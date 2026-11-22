package tokenmigrate

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"
)

var (
	testAt    = time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	domainKey = []byte("domain-key")
	partyKeys = map[PartyID][]byte{
		"party-a": []byte("party-a-key"),
		"party-b": []byte("party-b-key"),
	}
)

func baseOptions(at time.Time) Options {
	return Options{
		At:        at,
		Verifier:  testVerifier(),
		Allocator: DeterministicAllocator{},
		Issuer:    testIssuer(),
	}
}

func testVerifier() HMACVerifier {
	return HMACVerifier{
		IssuanceKeys: map[DomainID][]byte{
			"A":  domainKey,
			"B":  domainKey,
			"C":  domainKey,
			"D":  domainKey,
			"X":  domainKey,
			"P":  domainKey,
			"C1": domainKey,
			"C2": domainKey,
			"C3": domainKey,
		},
		EqualityKeys: partyKeys,
	}
}

func testIssuer() HMACIssuer {
	return HMACIssuer{Keys: map[DomainID][]byte{
		"A":  domainKey,
		"B":  domainKey,
		"C":  domainKey,
		"D":  domainKey,
		"X":  domainKey,
		"P":  domainKey,
		"C1": domainKey,
		"C2": domainKey,
		"C3": domainKey,
	}}
}

func newRecord(t *testing.T, id, domain string, gen uint64, token, commitment string) Issuance {
	t.Helper()
	r := Issuance{
		ID:         RecordID(id),
		Domain:     DomainID(domain),
		Generation: Generation(gen),
		Token:      Token(token),
		Commitment: Commitment(commitment),
	}
	if err := SignIssuanceHMAC(domainKey, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func makeShards(records []Issuance) []Shard {
	type key struct {
		domain DomainID
		gen    Generation
	}
	grouped := make(map[key]*Shard)
	for _, r := range records {
		k := key{r.Domain, r.Generation}
		s := grouped[k]
		if s == nil {
			s = &Shard{
				ID:                 fmt.Sprintf("s-%s-%d", r.Domain, r.Generation),
				Domain:             r.Domain,
				Generation:         r.Generation,
				TokenToCommitment:  make(map[Token]Commitment),
				CommitmentToTokens: make(map[Commitment][]Token),
			}
			grouped[k] = s
		}
		s.TokenToCommitment[r.Token] = r.Commitment
		if !containsToken(s.CommitmentToTokens[r.Commitment], r.Token) {
			s.CommitmentToTokens[r.Commitment] = append(s.CommitmentToTokens[r.Commitment], r.Token)
		}
	}
	out := make([]Shard, 0, len(grouped))
	for _, s := range grouped {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func equalityProof(t *testing.T, id, left, right string) EqualityProof {
	t.Helper()
	p := EqualityProof{ID: id, Left: Commitment(left), Right: Commitment(right)}
	for _, party := range []PartyID{"party-a", "party-b"} {
		sig, err := SignEqualityHMAC(partyKeys[party], p, party)
		if err != nil {
			t.Fatal(err)
		}
		p.Signatures = append(p.Signatures, sig)
	}
	return p
}

func mergeChange(id string, target string, gen uint64, sources ...string) Change {
	c := Change{
		ID:    ChangeID(id),
		Kind:  MergeChange,
		Valid: Period{NotBefore: testAt.Add(-time.Hour), NotAfter: testAt.Add(time.Hour)},
	}
	for _, source := range sources {
		c.Mappings = append(c.Mappings, Mapping{
			From:       DomainID(source),
			To:         DomainID(target),
			Generation: Generation(gen),
		})
	}
	return c
}

func TestMergeChoosesMinimumReissuesAndResolvesScopedAliases(t *testing.T) {
	records := []Issuance{
		newRecord(t, "a1", "A", 1, "shared", "c-a"),
		newRecord(t, "a2", "A", 1, "shared", "c-a"),
		newRecord(t, "b1", "B", 1, "shared", "c-b"),
	}
	in := Input{
		Domains: []Domain{{ID: "A"}, {ID: "B"}, {ID: "X"}},
		Changes: []Change{mergeChange("m", "X", 7, "A", "B")},
		Shards:  makeShards(records),
		Records: records,
	}
	plan, err := Build(in, baseOptions(testAt))
	if err != nil {
		t.Fatal(err)
	}
	if plan.ReissueCount() != 1 || len(plan.Reissues) != 1 {
		t.Fatalf("reissues = %d, want 1", plan.ReissueCount())
	}
	if plan.Reissues[0].RecordID != "b1" {
		t.Fatalf("reissued %s, want b1", plan.Reissues[0].RecordID)
	}
	if plan.Certificate.ReissueLowerBound != 1 || !plan.Certificate.Optimal {
		t.Fatalf("certificate = %+v", plan.Certificate)
	}

	a1, err := plan.Lookup(ReadRequest{Tenant: "X", Origin: Scope{"A", 1}, Token: "shared", At: testAt})
	if err != nil {
		t.Fatal(err)
	}
	b1, err := plan.Lookup(ReadRequest{Tenant: "X", Origin: Scope{"B", 1}, Token: "shared", At: testAt})
	if err != nil {
		t.Fatal(err)
	}
	if a1.Token != "shared" || a1.Commitment != "c-a" {
		t.Fatalf("A alias = %+v", a1)
	}
	if b1.Token == "shared" || b1.Commitment != "c-b" || b1.Target != (Scope{"X", 7}) {
		t.Fatalf("B alias = %+v", b1)
	}
	if _, err := plan.Lookup(ReadRequest{Tenant: "B", Origin: Scope{"A", 1}, Token: "shared", At: testAt}); err != ErrNotFound {
		t.Fatalf("outside-tenant read err = %v, want ErrNotFound", err)
	}
	if _, err := plan.Lookup(ReadRequest{Tenant: "X", Origin: Scope{"A", 2}, Token: "shared", At: testAt}); err != ErrNotFound {
		t.Fatalf("wrong-generation read err = %v, want ErrNotFound", err)
	}
	if _, err := plan.LookupUnique("X", "shared", testAt); err != ErrNotFound {
		t.Fatalf("ambiguous unscoped read err = %v, want ErrNotFound", err)
	}
	entries := plan.CompatibilityEntries()
	if len(entries) != 2 || entries[0].Origin.Domain != "A" || entries[1].Origin.Domain != "B" {
		t.Fatalf("administrative table export = %+v", entries)
	}
}

func TestEqualCostTieUsesRecordIDSequence(t *testing.T) {
	records := []Issuance{
		newRecord(t, "a1", "A", 1, "t", "c-a"),
		newRecord(t, "b1", "B", 1, "t", "c-b"),
	}
	in := Input{
		Domains: []Domain{{ID: "A"}, {ID: "B"}, {ID: "X"}},
		Changes: []Change{mergeChange("m", "X", 1, "A", "B")},
		Shards:  makeShards(records),
		Records: records,
	}
	plan, err := Build(in, baseOptions(testAt))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Reissues) != 1 || plan.Reissues[0].RecordID != "a1" {
		t.Fatalf("tie selected %+v, want lexicographically first reissue sequence [a1]", plan.Reissues)
	}
	b, err := plan.Lookup(ReadRequest{Tenant: "X", Origin: Scope{"B", 1}, Token: "t", At: testAt})
	if err != nil || b.Token != "t" {
		t.Fatalf("unexpected retained B read: %+v, %v", b, err)
	}
}

func TestTransitiveEqualityAndOverlappingTargetDomains(t *testing.T) {
	records := []Issuance{
		newRecord(t, "a1", "A", 1, "t", "c1"),
		newRecord(t, "b1", "B", 1, "t", "c2"),
		newRecord(t, "c1", "C", 1, "t", "c3"),
		newRecord(t, "d1", "D", 1, "t", "c4"),
	}
	in := Input{
		Domains: []Domain{{ID: "A"}, {ID: "B"}, {ID: "C"}, {ID: "D"}, {ID: "X"}},
		Changes: []Change{
			mergeChange("m1", "X", 2, "A", "B"),
			mergeChange("m2", "X", 2, "C", "D"),
		},
		Shards:  makeShards(records),
		Records: records,
		Proofs: []EqualityProof{
			equalityProof(t, "e1", "c1", "c2"),
			equalityProof(t, "e2", "c2", "c3"),
		},
	}
	plan, err := Build(in, baseOptions(testAt))
	if err != nil {
		t.Fatal(err)
	}
	if plan.ReissueCount() != 1 || plan.Reissues[0].RecordID != "d1" {
		t.Fatalf("transitive class handling reissued %+v", plan.Reissues)
	}
	class := plan.Assignments[0].Class
	if class != "c1" {
		t.Fatalf("canonical class = %s, want c1", class)
	}
	for _, a := range plan.Assignments {
		if a.RecordID == "d1" {
			continue
		}
		if a.Class != class || a.NewToken != "t" {
			t.Fatalf("equal record %s not unified: %+v", a.RecordID, a)
		}
	}
}

func TestEqualityRequiresTwoSignatures(t *testing.T) {
	records := []Issuance{
		newRecord(t, "a1", "A", 1, "t", "c-a"),
		newRecord(t, "b1", "B", 1, "t", "c-b"),
	}
	proof := equalityProof(t, "e", "c-a", "c-b")
	proof.Signatures = proof.Signatures[:1]
	in := Input{
		Domains: []Domain{{ID: "A"}, {ID: "B"}, {ID: "X"}},
		Changes: []Change{mergeChange("m", "X", 1, "A", "B")},
		Shards:  makeShards(records),
		Records: records,
		Proofs:  []EqualityProof{proof},
	}
	if _, err := Build(in, baseOptions(testAt)); err == nil {
		t.Fatal("one-signature equality proof was accepted")
	}
}

func TestSplitOnlyMigratesExplicitSubsetAndEmptyScopeIsIsolated(t *testing.T) {
	records := []Issuance{
		newRecord(t, "p1", "P", 1, "p-one", "v1"),
		newRecord(t, "p2", "P", 1, "p-two", "v2"),
		newRecord(t, "p3", "P", 1, "p-three", "v3"),
	}
	split := Change{
		ID:    "s",
		Kind:  SplitChange,
		Valid: Period{NotBefore: testAt.Add(-time.Hour), NotAfter: testAt.Add(time.Hour)},
		Mappings: []Mapping{
			{From: "P", To: "C1", Generation: 5, Explicit: true, Records: []RecordID{"p1", "p2"}},
			{From: "P", To: "C2", Generation: 5, Explicit: true, Records: []RecordID{}},
		},
	}
	in := Input{
		Domains: []Domain{{ID: "P"}, {ID: "C1"}, {ID: "C2"}},
		Changes: []Change{split},
		Shards:  makeShards(records),
		Records: records,
	}
	plan, err := Build(in, baseOptions(testAt))
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[RecordID]Assignment)
	for _, a := range plan.Assignments {
		byID[a.RecordID] = a
	}
	if byID["p1"].Target != (Scope{"C1", 5}) || byID["p2"].Target != (Scope{"C1", 5}) {
		t.Fatalf("authorized split targets = %+v, %+v", byID["p1"], byID["p2"])
	}
	if byID["p3"].Target != (Scope{"P", 1}) || byID["p3"].Moved {
		t.Fatalf("unauthorized record moved: %+v", byID["p3"])
	}
	if _, err := plan.Lookup(ReadRequest{Tenant: "C1", Origin: Scope{"P", 1}, Token: "p-one", At: testAt}); err != nil {
		t.Fatalf("authorized child read failed: %v", err)
	}
	for _, token := range []Token{"p-one", "p-two", "p-three"} {
		if _, err := plan.Lookup(ReadRequest{Tenant: "C2", Origin: Scope{"P", 1}, Token: token, At: testAt}); err != ErrNotFound {
			t.Fatalf("empty child read %s err = %v, want ErrNotFound", token, err)
		}
	}
	var empty *TargetScopeCertificate
	for i := range plan.Certificate.TargetScopes {
		if plan.Certificate.TargetScopes[i].Scope == (Scope{"C2", 5}) {
			empty = &plan.Certificate.TargetScopes[i]
		}
	}
	if empty == nil || !empty.Empty || empty.Records != 0 || empty.ReadEntries != 0 {
		t.Fatalf("empty scope certificate = %+v", empty)
	}

	snapshot, err := plan.Materialize(in, baseOptions(testAt))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range snapshot.Records {
		switch r.ID {
		case "p1", "p2":
			if r.Domain != "C1" || r.Generation != 5 {
				t.Fatalf("materialized %s = %+v", r.ID, r)
			}
		case "p3":
			if r.Domain != "P" || r.Generation != 1 {
				t.Fatalf("materialized %s = %+v", r.ID, r)
			}
		}
	}
}

func TestDeniedReadsAreIndistinguishable(t *testing.T) {
	records := []Issuance{
		newRecord(t, "x1", "A", 1, "t", "c"),
	}
	in := Input{
		Domains: []Domain{{ID: "A"}, {ID: "X"}},
		Changes: []Change{mergeChange("m", "X", 1, "A")},
		Shards:  makeShards(records),
		Records: records,
	}
	plan, err := Build(in, baseOptions(testAt))
	if err != nil {
		t.Fatal(err)
	}
	requests := []ReadRequest{
		{Tenant: "B", Origin: Scope{"A", 1}, Token: "t", At: testAt},
		{Tenant: "X", Origin: Scope{"A", 2}, Token: "t", At: testAt},
		{Tenant: "X", Origin: Scope{"A", 1}, Token: "missing", At: testAt},
		{Tenant: "X", Origin: Scope{"A", 1}, Token: "t", At: testAt.Add(-2 * time.Hour)},
		{},
	}
	for i, req := range requests {
		result, err := plan.Lookup(req)
		if err != ErrNotFound {
			t.Fatalf("request %d err = %v, want sentinel ErrNotFound", i, err)
		}
		if !reflect.DeepEqual(result, ReadResult{}) {
			t.Fatalf("request %d leaked result %+v", i, result)
		}
	}
}

func TestShardAndSignatureValidation(t *testing.T) {
	records := []Issuance{newRecord(t, "a1", "A", 1, "t", "c")}
	shards := makeShards(records)
	shards[0].CommitmentToTokens = nil
	in := Input{
		Domains: []Domain{{ID: "A"}},
		Shards:  shards,
		Records: records,
	}
	if _, err := Build(in, baseOptions(testAt)); err == nil {
		t.Fatal("one-directional shard was accepted")
	}

	bad := newRecord(t, "bad", "A", 1, "bad", "c")
	bad.Signature[0] ^= 0xff
	in = Input{
		Domains: []Domain{{ID: "A"}},
		Shards:  makeShards([]Issuance{bad}),
		Records: []Issuance{bad},
	}
	if _, err := Build(in, baseOptions(testAt)); err == nil {
		t.Fatal("bad issuance signature was accepted")
	}
}

func TestMigrationRollbackOnAllocatorAndSignerFailure(t *testing.T) {
	records := []Issuance{
		newRecord(t, "a1", "A", 1, "t", "c-a"),
		newRecord(t, "b1", "B", 1, "t", "c-b"),
	}
	in := Input{
		Domains: []Domain{{ID: "A"}, {ID: "B"}, {ID: "X"}},
		Changes: []Change{mergeChange("m", "X", 1, "A", "B")},
		Shards:  makeShards(records),
		Records: records,
	}
	before := cloneInput(in)

	badAllocator := baseOptions(testAt)
	badAllocator.Allocator = AllocatorFunc(func(AllocationRequest) (Token, error) {
		return "", fmt.Errorf("allocator offline")
	})
	if _, err := Migrate(in, badAllocator); err == nil {
		t.Fatal("allocator failure did not abort migration")
	}
	if !reflect.DeepEqual(in, before) {
		t.Fatal("input changed after allocator failure")
	}

	calls := 0
	badSigner := baseOptions(testAt)
	badSigner.Issuer = SignerFunc(func(r Issuance) ([]byte, error) {
		calls++
		if calls == 2 {
			return nil, fmt.Errorf("signing service offline")
		}
		return testIssuer().SignIssuance(r)
	})
	if _, err := Migrate(in, badSigner); err == nil {
		t.Fatal("signer failure did not abort migration")
	}
	if !reflect.DeepEqual(in, before) {
		t.Fatal("input changed after signer failure")
	}

	noSigner := baseOptions(testAt)
	noSigner.Issuer = nil
	if _, err := Migrate(in, noSigner); err != ErrSignerRequired {
		t.Fatalf("missing signer err = %v, want ErrSignerRequired", err)
	}
	if !reflect.DeepEqual(in, before) {
		t.Fatal("input changed after missing signer")
	}
}

func TestMaterializeReturnsSignedConsistentSnapshot(t *testing.T) {
	records := []Issuance{
		newRecord(t, "a1", "A", 1, "t", "c-a"),
		newRecord(t, "b1", "B", 1, "t", "c-b"),
	}
	in := Input{
		Domains: []Domain{{ID: "A"}, {ID: "B"}, {ID: "X"}},
		Changes: []Change{mergeChange("m", "X", 3, "A", "B")},
		Shards:  makeShards(records),
		Records: records,
	}
	result, err := Migrate(in, baseOptions(testAt))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Snapshot.Shards) != 1 || result.Snapshot.Shards[0].Scope() != (Scope{"X", 3}) {
		t.Fatalf("snapshot shards = %+v", result.Snapshot.Shards)
	}
	for _, r := range result.Snapshot.Records {
		if err := testVerifier().VerifyIssuance(r); err != nil {
			t.Fatalf("materialized signature for %s: %v", r.ID, err)
		}
	}
	if result.Plan.ReissueCount() != 1 {
		t.Fatalf("reissues = %d", result.Plan.ReissueCount())
	}
}

func TestMergeIntoExistingSourceDomain(t *testing.T) {
	records := []Issuance{
		newRecord(t, "a1", "A", 1, "t", "c-a"),
		newRecord(t, "b1", "B", 1, "t", "c-b"),
	}
	in := Input{
		Domains: []Domain{{ID: "A"}, {ID: "B"}},
		Changes: []Change{mergeChange("m", "A", 1, "A", "B")},
		Shards:  makeShards(records),
		Records: records,
	}
	result, err := Migrate(in, baseOptions(testAt))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range result.Snapshot.Records {
		if r.Domain != "A" {
			t.Fatalf("record %s ended in %s, want A", r.ID, r.Domain)
		}
	}
	if result.Plan.ReissueCount() != 1 {
		t.Fatalf("reissues = %d, want 1", result.Plan.ReissueCount())
	}
}

func TestChangeAndRecordValidity(t *testing.T) {
	expired := newRecord(t, "old", "A", 1, "old-token", "c-old")
	expired.Valid = Period{NotBefore: testAt.Add(-4 * time.Hour), NotAfter: testAt.Add(-time.Hour)}
	if err := SignIssuanceHMAC(domainKey, &expired); err != nil {
		t.Fatal(err)
	}
	live := newRecord(t, "live", "A", 2, "live-token", "c-live")
	records := []Issuance{expired, live}
	future := Change{
		ID:    "future",
		Kind:  MergeChange,
		Valid: Period{NotBefore: testAt.Add(time.Hour), NotAfter: testAt.Add(2 * time.Hour)},
		Mappings: []Mapping{
			{From: "A", To: "X", Generation: 9},
		},
	}
	in := Input{
		Domains: []Domain{{ID: "A"}, {ID: "X"}},
		Changes: []Change{future},
		Shards:  makeShards(records),
		Records: records,
	}
	plan, err := Build(in, baseOptions(testAt))
	if err != nil {
		t.Fatal(err)
	}
	if plan.ReissueCount() != 0 {
		t.Fatalf("future change caused reissues: %+v", plan.Reissues)
	}
	if len(plan.Assignments) != 1 || plan.Assignments[0].RecordID != "live" || plan.Assignments[0].Moved {
		t.Fatalf("validity filtering produced assignments %+v", plan.Assignments)
	}
	if _, err := plan.Lookup(ReadRequest{Tenant: "X", Origin: Scope{"A", 2}, Token: "live-token", At: testAt}); err != ErrNotFound {
		t.Fatalf("inactive change leaked read: %v", err)
	}

	active := future
	active.ID = "active"
	active.Valid = Period{NotBefore: testAt.Add(-time.Hour), NotAfter: testAt.Add(time.Hour)}
	in.Changes = []Change{active}
	plan, err = Build(in, baseOptions(testAt))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Assignments) != 1 || plan.Assignments[0].RecordID != "live" || plan.Assignments[0].Target != (Scope{"X", 9}) {
		t.Fatalf("active change assignments %+v", plan.Assignments)
	}
}

func TestSplitCollisionWithExistingChildCanReissueStationaryRecord(t *testing.T) {
	records := []Issuance{
		newRecord(t, "p1", "P", 1, "t", "c-parent"),
		newRecord(t, "c1", "C", 1, "t", "c-child"),
	}
	split := Change{
		ID:    "s",
		Kind:  SplitChange,
		Valid: Period{NotBefore: testAt.Add(-time.Hour), NotAfter: testAt.Add(time.Hour)},
		Mappings: []Mapping{
			{From: "P", To: "C", Generation: 1, Explicit: true, Records: []RecordID{"p1"}},
		},
	}
	in := Input{
		Domains: []Domain{{ID: "P"}, {ID: "C"}},
		Changes: []Change{split},
		Shards:  makeShards(records),
		Records: records,
	}
	plan, err := Build(in, baseOptions(testAt))
	if err != nil {
		t.Fatal(err)
	}
	if plan.ReissueCount() != 1 || plan.Reissues[0].RecordID != "c1" {
		t.Fatalf("collision choice = %+v, want stationary c1 reissued by ID tie-break", plan.Reissues)
	}
	childOld, err := plan.Lookup(ReadRequest{Tenant: "C", Origin: Scope{"C", 1}, Token: "t", At: testAt})
	if err != nil {
		t.Fatal(err)
	}
	moved, err := plan.Lookup(ReadRequest{Tenant: "C", Origin: Scope{"P", 1}, Token: "t", At: testAt})
	if err != nil {
		t.Fatal(err)
	}
	if childOld.Token == "t" || moved.Token != "t" {
		t.Fatalf("aliases child=%+v moved=%+v", childOld, moved)
	}
	if _, err := plan.LookupUnique("C", "t", testAt); err != ErrNotFound {
		t.Fatalf("unscoped collision did not fail closed: %v", err)
	}
	if _, err := plan.Materialize(in, baseOptions(testAt)); err != nil {
		t.Fatal(err)
	}
}

func TestPlanIsDeterministicAcrossInputOrder(t *testing.T) {
	records := []Issuance{
		newRecord(t, "a1", "A", 1, "t", "c1"),
		newRecord(t, "b1", "B", 1, "t", "c2"),
		newRecord(t, "c1", "C", 1, "t", "c3"),
	}
	proofs := []EqualityProof{
		equalityProof(t, "e1", "c1", "c2"),
		equalityProof(t, "e2", "c2", "c3"),
	}
	base := Input{
		Domains: []Domain{{ID: "A"}, {ID: "B"}, {ID: "C"}, {ID: "X"}},
		Changes: []Change{mergeChange("m", "X", 1, "A", "B", "C")},
		Shards:  makeShards(records),
		Records: records,
		Proofs:  proofs,
	}
	first, err := Build(base, baseOptions(testAt))
	if err != nil {
		t.Fatal(err)
	}

	shuffled := base
	shuffled.Domains = []Domain{{ID: "X"}, {ID: "C"}, {ID: "A"}, {ID: "B"}}
	shuffled.Records = []Issuance{records[2], records[0], records[1]}
	shuffled.Proofs = []EqualityProof{proofs[1], proofs[0]}
	shuffled.Shards = []Shard{base.Shards[2], base.Shards[0], base.Shards[1]}
	second, err := Build(shuffled, baseOptions(testAt))
	if err != nil {
		t.Fatal(err)
	}
	if first.InputDigest != second.InputDigest || first.PlanDigest != second.PlanDigest {
		t.Fatalf("digests differ: %s/%s vs %s/%s", first.InputDigest, first.PlanDigest, second.InputDigest, second.PlanDigest)
	}
	if !reflect.DeepEqual(first.Reissues, second.Reissues) {
		t.Fatalf("reissues differ: %+v vs %+v", first.Reissues, second.Reissues)
	}
}

func cloneInput(in Input) Input {
	out := in
	out.Domains = append([]Domain(nil), in.Domains...)
	out.Changes = append([]Change(nil), in.Changes...)
	for i := range out.Changes {
		out.Changes[i].Mappings = append([]Mapping(nil), in.Changes[i].Mappings...)
	}
	out.Records = append([]Issuance(nil), in.Records...)
	for i := range out.Records {
		out.Records[i].Signature = append([]byte(nil), in.Records[i].Signature...)
	}
	out.Proofs = append([]EqualityProof(nil), in.Proofs...)
	out.Shards = make([]Shard, len(in.Shards))
	for i := range in.Shards {
		out.Shards[i] = in.Shards[i]
		out.Shards[i].TokenToCommitment = make(map[Token]Commitment, len(in.Shards[i].TokenToCommitment))
		for token, commitment := range in.Shards[i].TokenToCommitment {
			out.Shards[i].TokenToCommitment[token] = commitment
		}
		out.Shards[i].CommitmentToTokens = make(map[Commitment][]Token, len(in.Shards[i].CommitmentToTokens))
		for commitment, tokens := range in.Shards[i].CommitmentToTokens {
			out.Shards[i].CommitmentToTokens[commitment] = append([]Token(nil), tokens...)
		}
	}
	return out
}

func TestExhaustiveSmallMapAgainstBruteForce(t *testing.T) {
	partitions := [][][]int{
		{{0}, {1}, {2}},
		{{0, 1}, {2}},
		{{0, 2}, {1}},
		{{1, 2}, {0}},
		{{0, 1, 2}},
	}
	domains := []DomainID{"A", "B", "C"}
	commitments := []Commitment{"c0", "c1", "c2"}

	for partitionIndex, partition := range partitions {
		for config := 0; config < 64; config++ {
			for multiplicity := 0; multiplicity < 8; multiplicity++ {
				var records []Issuance
				for d, domain := range domains {
					choice := (config >> (2 * d)) & 3
					if choice == 0 {
						continue
					}
					classIndex := choice - 1
					copies := 1 + ((multiplicity >> d) & 1)
					for copyIndex := 0; copyIndex < copies; copyIndex++ {
						id := fmt.Sprintf("%s-%d", domain, copyIndex+1)
						records = append(records, newRecord(
							t, id, string(domain), 1, "t", string(commitments[classIndex]),
						))
					}
				}
				if len(records) == 0 {
					continue
				}

				var proofs []EqualityProof
				for blockIndex, block := range partition {
					for i := 1; i < len(block); i++ {
						proofs = append(proofs, equalityProof(
							t,
							fmt.Sprintf("p-%d-%d-%d-%d", partitionIndex, config, blockIndex, i),
							string(commitments[block[0]]),
							string(commitments[block[i]]),
						))
					}
				}

				in := Input{
					Domains: []Domain{{ID: "A"}, {ID: "B"}, {ID: "C"}, {ID: "X"}},
					Changes: []Change{mergeChange("m", "X", 1, "A", "B", "C")},
					Shards:  makeShards(records),
					Records: records,
					Proofs:  proofs,
				}
				plan, err := Build(in, baseOptions(testAt))
				if err != nil {
					t.Fatalf("partition %d config %d multiplicity %d: %v", partitionIndex, config, multiplicity, err)
				}

				expectedIDs, expectedLower := bruteReissues(records, partition)
				gotIDs := make([]RecordID, 0, len(plan.Reissues))
				for _, reissue := range plan.Reissues {
					gotIDs = append(gotIDs, reissue.RecordID)
				}
				sort.Slice(gotIDs, func(i, j int) bool { return gotIDs[i] < gotIDs[j] })
				if !reflect.DeepEqual(gotIDs, expectedIDs) {
					t.Fatalf("partition %d config %d multiplicity %d: reissues %v, want %v", partitionIndex, config, multiplicity, gotIDs, expectedIDs)
				}
				if plan.Certificate.ReissueLowerBound != expectedLower || plan.ReissueCount() != expectedLower {
					t.Fatalf("partition %d config %d multiplicity %d: lower=%d count=%d want=%d",
						partitionIndex, config, multiplicity,
						plan.Certificate.ReissueLowerBound, plan.ReissueCount(), expectedLower)
				}

				for _, r := range records {
					result, err := plan.Lookup(ReadRequest{
						Tenant: "X",
						Origin: r.Scope(),
						Token:  r.Token,
						At:     testAt,
					})
					if err != nil {
						t.Fatalf("partition %d config %d: old alias %s failed: %v", partitionIndex, config, r.ID, err)
					}
					if result.Commitment != r.Commitment {
						t.Fatalf("partition %d config %d: alias %s returned %s, want %s", partitionIndex, config, r.ID, result.Commitment, r.Commitment)
					}
				}
			}
		}
	}
}

func bruteReissues(records []Issuance, partition [][]int) ([]RecordID, int) {
	commitmentToClass := make(map[Commitment]int)
	for classIndex, block := range partition {
		for _, member := range block {
			commitmentToClass[Commitment(fmt.Sprintf("c%d", member))] = classIndex
		}
	}
	weights := make(map[int][]RecordID)
	for _, r := range records {
		class := commitmentToClass[r.Commitment]
		weights[class] = append(weights[class], r.ID)
	}
	maxWeight := 0
	for _, ids := range weights {
		if len(ids) > maxWeight {
			maxWeight = len(ids)
		}
	}

	var best []RecordID
	for class, ids := range weights {
		if len(ids) != maxWeight {
			continue
		}
		keep := make(map[RecordID]struct{}, len(ids))
		for _, id := range ids {
			keep[id] = struct{}{}
		}
		var candidate []RecordID
		for _, r := range records {
			if commitmentToClass[r.Commitment] == class {
				continue
			}
			candidate = append(candidate, r.ID)
		}
		sort.Slice(candidate, func(i, j int) bool { return candidate[i] < candidate[j] })
		if best == nil || lessRecordIDs(candidate, best) {
			best = candidate
		}
	}
	if best == nil {
		best = []RecordID{}
	}
	return best, len(records) - maxWeight
}
