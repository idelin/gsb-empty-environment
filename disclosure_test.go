package disclosure

import (
	"encoding/json"
	"errors"
	"testing"
)

type fixture struct {
	leaves      []Leaf
	checkpoint  Checkpoint
	roots       map[string]uint64
	corrections map[string]uint64
	tombstone   uint64
}

func makeFixture(t *testing.T) fixture {
	t.Helper()
	var leaves []Leaf
	appendLeaf := func(leaf Leaf) Leaf {
		leaf.Seq = uint64(len(leaves))
		if err := leaf.Seal(); err != nil {
			t.Fatalf("seal seq %d: %v", leaf.Seq, err)
		}
		leaves = append(leaves, leaf)
		return leaf
	}
	event := func(tenant string, start, end int64, body string) Leaf {
		return appendLeaf(Leaf{
			Kind:       KindEvent,
			Tenant:     tenant,
			EventStart: start,
			EventEnd:   end,
			Body:       []byte(body),
		})
	}
	correct := func(target Leaf, tenant string, start, end int64, body string) Leaf {
		leaf := Leaf{
			Kind:       KindCorrection,
			Tenant:     tenant,
			EventStart: start,
			EventEnd:   end,
			PrevDigest: target.Digest(),
			RefSeq:     SeqRef(target.Seq),
		}
		if body != "" {
			leaf.Body = []byte(body)
		}
		return appendLeaf(leaf)
	}

	roots := make(map[string]uint64)
	roots["a"] = event("a", 10, 20, "a-original").Seq
	roots["same_instant"] = event("a", 10, 10, "same-instant").Seq
	roots["b"] = event("b", 10, 20, "b-original").Seq
	roots["definite"] = event("a", 12, 14, "definite-body").Seq
	roots["same_start"] = event("a", 12, 12, "same-start-body").Seq
	roots["possible"] = event("a", 0, 100, "possible-body").Seq
	roots["out"] = event("a", 40, 50, "out-body").Seq
	roots["same_instant_2"] = event("a", 12, 12, "same-instant-two").Seq

	a := leaves[roots["a"]]
	b := leaves[roots["b"]]
	corrections := make(map[string]uint64)
	corrections["move_a_to_b"] = correct(a, "b", 12, 18, "a-corrected").Seq
	corrections["move_b_to_a"] = correct(b, "a", 30, 40, "b-corrected").Seq

	tomb := appendLeaf(Leaf{
		Kind:          KindTombstone,
		PrevDigest:    leaves[roots["same_instant"]].Digest(),
		RefSeq:        SeqRef(roots["same_instant"]),
		Authorization: []byte("legal-erasure-order-17"),
	})
	corrections["erased_after_tombstone"] = correct(leaves[roots["same_instant"]], "c", 11, 12, "").Seq

	checkpoint, err := BuildCheckpoint(leaves)
	if err != nil {
		t.Fatalf("build checkpoint: %v", err)
	}
	return fixture{
		leaves:      leaves,
		checkpoint:  checkpoint,
		roots:       roots,
		corrections: corrections,
		tombstone:   tomb.Seq,
	}
}

func TestDisclosureReconstructsCorrectionsAndPartitions(t *testing.T) {
	fx := makeFixture(t)

	disclosure, err := BuildDisclosureWithOptions(fx.leaves, fx.checkpoint, Query{
		Tenant: "a",
		From:   10,
		To:     15,
	}, DiscloseOptions{
		IncludeBody: func(rootSeq uint64, _ Leaf) bool {
			return rootSeq != fx.roots["definite"]
		},
	})
	if err != nil {
		t.Fatalf("build disclosure: %v", err)
	}
	result, err := VerifyDisclosure(disclosure, VerifyOptions{})
	if err != nil {
		t.Fatalf("verify disclosure: %v", err)
	}

	assertRoots(t, result.Definite, fx.roots["definite"], fx.roots["same_start"], fx.roots["same_instant_2"])
	assertRoots(t, result.Possible, fx.roots["possible"])
	if len(result.Erased) != 0 {
		t.Fatalf("unexpected erased records in tenant a: %+v", result.Erased)
	}
	for _, record := range result.Definite {
		if record.RootSeq == fx.roots["definite"] {
			if record.BodyKnown {
				t.Fatal("withheld body was reported as known")
			}
			if record.Bucket != Definite {
				t.Fatalf("withheld-body record has bucket %v", record.Bucket)
			}
		}
	}

	// The correction moved the original a leaf into b. It must not appear in
	// tenant a, and its narrowed interval must be used in b.
	inB, err := BuildDisclosure(fx.leaves, fx.checkpoint, Query{Tenant: "b", From: 10, To: 11})
	if err != nil {
		t.Fatalf("build b disclosure: %v", err)
	}
	bResult, err := VerifyDisclosure(inB, VerifyOptions{})
	if err != nil {
		t.Fatalf("verify b disclosure: %v", err)
	}
	if len(bResult.Definite)+len(bResult.Possible)+len(bResult.Erased) != 0 {
		t.Fatalf("narrowed correction should not intersect [10,11]: %+v", bResult)
	}

	inB2, err := BuildDisclosure(fx.leaves, fx.checkpoint, Query{Tenant: "b", From: 10, To: 15})
	if err != nil {
		t.Fatalf("build b disclosure 2: %v", err)
	}
	bResult2, err := VerifyDisclosure(inB2, VerifyOptions{})
	if err != nil {
		t.Fatalf("verify b disclosure 2: %v", err)
	}
	assertRoots(t, bResult2.Possible, fx.roots["a"])
	if got := bResult2.Possible[0].VersionSeq; got != fx.corrections["move_a_to_b"] {
		t.Fatalf("effective version = %d, want correction %d", got, fx.corrections["move_a_to_b"])
	}
}

func TestDisclosureMovedInAndErasedCorrectionTarget(t *testing.T) {
	fx := makeFixture(t)

	movedIn, err := BuildDisclosure(fx.leaves, fx.checkpoint, Query{Tenant: "a", From: 30, To: 40})
	if err != nil {
		t.Fatalf("build moved-in disclosure: %v", err)
	}
	movedResult, err := VerifyDisclosure(movedIn, VerifyOptions{})
	if err != nil {
		t.Fatalf("verify moved-in disclosure: %v", err)
	}
	assertRoots(t, movedResult.Definite, fx.roots["b"])
	if movedResult.Definite[0].VersionSeq != fx.corrections["move_b_to_a"] {
		t.Fatalf("moved-in record did not use correction version")
	}

	// The effective record moved out of b, so a b query cannot return the old
	// pre-correction leaf as a valid b record.
	movedOut, err := BuildDisclosure(fx.leaves, fx.checkpoint, Query{Tenant: "b", From: 30, To: 35})
	if err != nil {
		t.Fatalf("build moved-out disclosure: %v", err)
	}
	outResult, err := VerifyDisclosure(movedOut, VerifyOptions{})
	if err != nil {
		t.Fatalf("verify moved-out disclosure: %v", err)
	}
	if len(outResult.Definite)+len(outResult.Possible)+len(outResult.Erased) != 0 {
		t.Fatalf("corrected-out record leaked into old tenant: %+v", outResult)
	}

	erased, err := BuildDisclosure(fx.leaves, fx.checkpoint, Query{Tenant: "c", From: 11, To: 12})
	if err != nil {
		t.Fatalf("build erased disclosure: %v", err)
	}
	erasedResult, err := VerifyDisclosure(erased, VerifyOptions{})
	if err != nil {
		t.Fatalf("verify erased disclosure: %v", err)
	}
	assertRoots(t, erasedResult.Erased, fx.roots["same_instant"])
	record := erasedResult.Erased[0]
	if !record.Erased || record.BodyKnown || record.TombstoneSeq != fx.tombstone {
		t.Fatalf("erased record not reconstructed correctly: %+v", record)
	}
	if record.VersionSeq != fx.corrections["erased_after_tombstone"] {
		t.Fatalf("later correction referenced erased leaf but was not effective")
	}
	wantLeaves := map[uint64]bool{
		fx.roots["same_instant"]:                 true,
		fx.tombstone:                             true,
		fx.corrections["erased_after_tombstone"]: true,
	}
	for _, seq := range record.LeafSeqs {
		delete(wantLeaves, seq)
	}
	if len(wantLeaves) != 0 {
		t.Fatalf("erased record did not include full chain/tombstone: %+v", record.LeafSeqs)
	}
}

func TestIndexRangeBoundaryCannotDropOneEntry(t *testing.T) {
	fx := makeFixture(t)
	disclosure, err := BuildDisclosure(fx.leaves, fx.checkpoint, Query{Tenant: "a", From: 10, To: 15})
	if err != nil {
		t.Fatalf("build disclosure: %v", err)
	}
	if len(disclosure.IndexProof.Entries) != 4 {
		t.Fatalf("expected four effective records in range, got %d", len(disclosure.IndexProof.Entries))
	}

	// Dropping only the final in-range item must be detected even though the
	// remaining prefix is internally valid.
	disclosure.IndexProof.Entries = disclosure.IndexProof.Entries[:3]
	disclosure.IndexProof.Count = 3
	_, err = VerifyDisclosure(disclosure, VerifyOptions{})
	if !errors.Is(err, ErrIncompleteIndexRange) {
		t.Fatalf("dropped last entry error = %v, want ErrIncompleteIndexRange", err)
	}
}

func TestIndexRangeBoundaryCannotBeOmitted(t *testing.T) {
	fx := makeFixture(t)
	disclosure, err := BuildDisclosure(fx.leaves, fx.checkpoint, Query{Tenant: "b", From: 10, To: 15})
	if err != nil {
		t.Fatalf("build disclosure: %v", err)
	}
	if disclosure.IndexProof.LeftBoundary == nil {
		t.Fatal("fixture did not exercise a non-zero left boundary")
	}
	disclosure.IndexProof.LeftBoundary = nil
	_, err = VerifyDisclosure(disclosure, VerifyOptions{})
	if !errors.Is(err, ErrIncompleteIndexRange) {
		t.Fatalf("missing left boundary error = %v, want ErrIncompleteIndexRange", err)
	}
}

func TestCrossRootSpliceFails(t *testing.T) {
	fx := makeFixture(t)
	disclosure, err := BuildDisclosure(fx.leaves, fx.checkpoint, Query{Tenant: "a", From: 12, To: 14})
	if err != nil {
		t.Fatalf("build disclosure: %v", err)
	}

	otherLeaves := append([]Leaf(nil), fx.leaves...)
	otherLeaves = append(otherLeaves, Leaf{
		Seq:        uint64(len(otherLeaves)),
		Kind:       KindEvent,
		Tenant:     "z",
		EventStart: 1,
		EventEnd:   2,
	})
	otherCheckpoint, err := BuildCheckpoint(otherLeaves)
	if err != nil {
		t.Fatalf("build other checkpoint: %v", err)
	}

	// Mix a log root from another checkpoint into this proof.
	disclosure.LogProof.Root = otherCheckpoint.LogRoot
	_, err = VerifyDisclosure(disclosure, VerifyOptions{})
	if !errors.Is(err, ErrCrossRoot) {
		t.Fatalf("cross-root error = %v, want ErrCrossRoot", err)
	}
}

func TestMissingCorrectionTargetFails(t *testing.T) {
	fx := makeFixture(t)
	disclosure, err := BuildDisclosure(fx.leaves, fx.checkpoint, Query{Tenant: "b", From: 10, To: 15})
	if err != nil {
		t.Fatalf("build disclosure: %v", err)
	}

	filteredLeaves := disclosure.Leaves[:0]
	filteredProofs := disclosure.LogProof.Proofs[:0]
	for i, leaf := range disclosure.Leaves {
		if leaf.Seq == fx.roots["a"] {
			continue
		}
		filteredLeaves = append(filteredLeaves, leaf)
		filteredProofs = append(filteredProofs, disclosure.LogProof.Proofs[i])
	}
	disclosure.Leaves = filteredLeaves
	disclosure.LogProof.Proofs = filteredProofs
	_, err = VerifyDisclosure(disclosure, VerifyOptions{})
	if !errors.Is(err, ErrMissingLeaf) {
		t.Fatalf("missing correction target error = %v, want ErrMissingLeaf", err)
	}
}

func TestTamperedBodyFailsCommitment(t *testing.T) {
	fx := makeFixture(t)
	disclosure, err := BuildDisclosure(fx.leaves, fx.checkpoint, Query{Tenant: "a", From: 12, To: 14})
	if err != nil {
		t.Fatalf("build disclosure: %v", err)
	}
	tampered := false
	for i := range disclosure.Leaves {
		if len(disclosure.Leaves[i].Body) == 0 {
			continue
		}
		disclosure.Leaves[i].Body = append([]byte(nil), disclosure.Leaves[i].Body...)
		disclosure.Leaves[i].Body[0] ^= 0xff
		tampered = true
		break
	}
	if !tampered {
		t.Fatal("fixture did not include a disclosed body")
	}
	_, err = VerifyDisclosure(disclosure, VerifyOptions{})
	if !errors.Is(err, ErrBodyCommitment) {
		t.Fatalf("tampered body error = %v, want ErrBodyCommitment", err)
	}
}

func TestSignedCheckpointAndTombstoneAuthorizer(t *testing.T) {
	fx := makeFixture(t)
	checkpointPublic, checkpointPrivate, err := ed25519Pair()
	if err != nil {
		t.Fatal(err)
	}
	if err := SignCheckpoint(checkpointPrivate, &fx.checkpoint); err != nil {
		t.Fatalf("sign checkpoint: %v", err)
	}

	authorityPublic, authorityPrivate, err := ed25519Pair()
	if err != nil {
		t.Fatal(err)
	}
	for i := range fx.leaves {
		if fx.leaves[i].Kind != KindTombstone {
			continue
		}
		target := fx.leaves[fx.roots["same_instant"]].Digest()
		authorization, err := SignTombstoneAuthorization(authorityPrivate, target)
		if err != nil {
			t.Fatal(err)
		}
		fx.leaves[i].Authorization = authorization
	}
	checkpoint, err := BuildCheckpoint(fx.leaves)
	if err != nil {
		t.Fatal(err)
	}
	if err := SignCheckpoint(checkpointPrivate, &checkpoint); err != nil {
		t.Fatal(err)
	}

	disclosure, err := BuildDisclosure(fx.leaves, checkpoint, Query{Tenant: "c", From: 11, To: 12})
	if err != nil {
		t.Fatal(err)
	}
	_, err = VerifyDisclosure(disclosure, VerifyOptions{
		VerifyCheckpoint:   Ed25519CheckpointVerifier(checkpointPublic),
		AuthorizeTombstone: Ed25519TombstoneAuthorizer(authorityPublic),
	})
	if err != nil {
		t.Fatalf("verify signed disclosure: %v", err)
	}
	_, err = VerifyDisclosure(disclosure, VerifyOptions{
		VerifyCheckpoint:   Ed25519CheckpointVerifier(checkpointPublic),
		AuthorizeTombstone: Ed25519TombstoneAuthorizer(checkpointPublic),
	})
	if !errors.Is(err, ErrUnauthorizedTombstone) {
		t.Fatalf("wrong tombstone authority error = %v, want ErrUnauthorizedTombstone", err)
	}
}

func TestEmptyLogDisclosure(t *testing.T) {
	checkpoint, err := BuildCheckpoint(nil)
	if err != nil {
		t.Fatalf("build empty checkpoint: %v", err)
	}
	disclosure, err := BuildDisclosure(nil, checkpoint, Query{Tenant: "none", From: 0, To: 1})
	if err != nil {
		t.Fatalf("build empty disclosure: %v", err)
	}
	result, err := VerifyDisclosure(disclosure, VerifyOptions{})
	if err != nil {
		t.Fatalf("verify empty disclosure: %v", err)
	}
	if len(result.Definite)+len(result.Possible)+len(result.Erased) != 0 {
		t.Fatalf("empty log returned records: %+v", result)
	}
}

func TestChainedCorrectionsWithNonZeroBase(t *testing.T) {
	event := Leaf{
		Seq:        100,
		Kind:       KindEvent,
		Tenant:     "old",
		EventStart: 1,
		EventEnd:   10,
		Body:       []byte("original"),
	}
	if err := event.Seal(); err != nil {
		t.Fatal(err)
	}
	mid := Leaf{
		Seq:        101,
		Kind:       KindCorrection,
		Tenant:     "middle",
		EventStart: 2,
		EventEnd:   9,
		PrevDigest: event.Digest(),
		RefSeq:     SeqRef(event.Seq),
	}
	if err := mid.Seal(); err != nil {
		t.Fatal(err)
	}
	latest := Leaf{
		Seq:        102,
		Kind:       KindCorrection,
		Tenant:     "new",
		EventStart: 3,
		EventEnd:   8,
		PrevDigest: mid.Digest(),
		RefSeq:     SeqRef(mid.Seq),
		Body:       []byte("latest"),
	}
	if err := latest.Seal(); err != nil {
		t.Fatal(err)
	}
	leaves := []Leaf{event, mid, latest}
	checkpoint, err := BuildCheckpoint(leaves)
	if err != nil {
		t.Fatalf("build checkpoint: %v", err)
	}
	if checkpoint.BaseSeq != 100 || checkpoint.Size != 3 {
		t.Fatalf("unexpected checkpoint bounds: %+v", checkpoint)
	}
	disclosure, err := BuildDisclosure(leaves, checkpoint, Query{Tenant: "new", From: 0, To: 20})
	if err != nil {
		t.Fatalf("build disclosure: %v", err)
	}
	result, err := VerifyDisclosure(disclosure, VerifyOptions{})
	if err != nil {
		t.Fatalf("verify disclosure: %v", err)
	}
	if len(result.Definite) != 1 {
		t.Fatalf("definite records = %d, want 1", len(result.Definite))
	}
	record := result.Definite[0]
	if record.VersionSeq != latest.Seq || string(record.Body) != "latest" {
		t.Fatalf("latest correction was not reconstructed: %+v", record)
	}
	if len(record.LeafSeqs) != 3 ||
		record.LeafSeqs[0] != event.Seq ||
		record.LeafSeqs[1] != mid.Seq ||
		record.LeafSeqs[2] != latest.Seq {
		t.Fatalf("correction chain = %v", record.LeafSeqs)
	}
}

func TestCorrectionBranchRejected(t *testing.T) {
	event := Leaf{Seq: 0, Kind: KindEvent, Tenant: "a", EventStart: 1, EventEnd: 2}
	if err := event.Seal(); err != nil {
		t.Fatal(err)
	}
	first := Leaf{
		Seq:        1,
		Kind:       KindCorrection,
		Tenant:     "b",
		EventStart: 1,
		EventEnd:   2,
		PrevDigest: event.Digest(),
		RefSeq:     SeqRef(0),
	}
	second := first
	second.Seq = 2
	second.Tenant = "c"
	_, err := BuildCheckpoint([]Leaf{event, first, second})
	if !errors.Is(err, ErrInvalidLeaf) {
		t.Fatalf("branch error = %v, want ErrInvalidLeaf", err)
	}
}

func TestMerkleProofsAcrossTreeSizes(t *testing.T) {
	for size := 0; size <= 17; size++ {
		hashes := make([]Hash, size)
		for i := range hashes {
			hashes[i] = HashBytes([]byte{byte(i), byte(size)})
		}
		root := merkleRoot(hashes)
		for pos := 0; pos < size; pos++ {
			proofRoot, path := merkleProof(hashes, pos)
			if proofRoot != root {
				t.Fatalf("size %d pos %d: proof root differs from tree root", size, pos)
			}
			reconstructed, err := merkleRootFromProof(uint64(pos), uint64(size), hashes[pos], path)
			if err != nil {
				t.Fatalf("size %d pos %d: %v", size, pos, err)
			}
			if reconstructed != root {
				t.Fatalf("size %d pos %d: reconstructed root differs", size, pos)
			}
		}
	}
}

func TestDisclosureJSONRoundTrip(t *testing.T) {
	fx := makeFixture(t)
	disclosure, err := BuildDisclosure(fx.leaves, fx.checkpoint, Query{Tenant: "c", From: 11, To: 12})
	if err != nil {
		t.Fatalf("build disclosure: %v", err)
	}
	encoded, err := json.Marshal(disclosure)
	if err != nil {
		t.Fatalf("marshal disclosure: %v", err)
	}
	var decoded Disclosure
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal disclosure: %v", err)
	}
	if _, err := VerifyDisclosure(&decoded, VerifyOptions{}); err != nil {
		t.Fatalf("verify decoded disclosure: %v", err)
	}
}

func assertRoots(t *testing.T, records []Record, roots ...uint64) {
	t.Helper()
	if len(records) != len(roots) {
		t.Fatalf("record count = %d, want %d: %+v", len(records), len(roots), records)
	}
	got := make(map[uint64]bool, len(records))
	for _, record := range records {
		got[record.RootSeq] = true
	}
	for _, root := range roots {
		if !got[root] {
			t.Fatalf("record root %d missing from %+v", root, records)
		}
	}
}
