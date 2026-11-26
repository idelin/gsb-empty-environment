package credentialauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

func credential(rpID, id string) CredentialRef {
	return CredentialRef{RPID: rpID, CredentialID: id}
}

func receipt(device string, generation, counter uint64, refs ...CredentialRef) DeviceReceipt {
	return DeviceReceipt{
		DeviceID:         device,
		BootGeneration:   generation,
		SignatureCounter: counter,
		Credentials:      refs,
	}
}

func TestGlobalMatchingRejectsCrossBatchHallFailure(t *testing.T) {
	refs := []CredentialRef{
		credential("rp", "a"),
		credential("rp", "b"),
		credential("rp", "c"),
	}
	snapshot := Snapshot{
		Devices: []DeviceReceipt{
			receipt("d1", 1, 1, refs[0]),
			receipt("d2", 1, 1, refs[1]),
			receipt("d3", 1, 1, refs[2]),
		},
		Batches: []BatchSummary{
			{BatchID: "b1", RPID: "rp", CandidateCredentials: refs, TransactionCount: 2},
			{BatchID: "b2", RPID: "rp", CandidateCredentials: refs, TransactionCount: 2},
		},
	}
	_, err := BuildProof(snapshot)
	if !errors.Is(err, ErrNoGlobalAssignment) {
		t.Fatalf("BuildProof() error = %v, want ErrNoGlobalAssignment", err)
	}
	oracle, err := Oracle(snapshot)
	if err != nil {
		t.Fatalf("Oracle() error = %v", err)
	}
	if oracle.Satisfiable {
		t.Fatal("Oracle() reported a satisfiable cross-batch Hall failure")
	}
}

func TestGlobalMatchingSucceedsAndReservationsLockCredentials(t *testing.T) {
	a := credential("rp", "a")
	b := credential("rp", "b")
	c := credential("rp", "c")
	snapshot := Snapshot{
		Devices: []DeviceReceipt{
			receipt("d1", 1, 1, a),
			receipt("d2", 1, 1, b),
			receipt("d3", 1, 1, c),
		},
		Batches: []BatchSummary{
			{BatchID: "b1", RPID: "rp", CandidateCredentials: []CredentialRef{a, b}, TransactionCount: 1},
			{BatchID: "b2", RPID: "rp", CandidateCredentials: []CredentialRef{b, c}, TransactionCount: 1},
		},
	}
	proof, err := BuildProof(snapshot)
	if err != nil {
		t.Fatalf("BuildProof() error = %v", err)
	}
	if len(proof.Consumed) != 2 {
		t.Fatalf("Consumed length = %d, want 2", len(proof.Consumed))
	}

	snapshot.Reservations = []Reservation{{RPID: "rp", CredentialID: "b"}}
	proof, err = BuildProof(snapshot)
	if err != nil {
		t.Fatalf("BuildProof() with reservation error = %v", err)
	}
	for _, ref := range proof.Consumed {
		if ref.CredentialIDValue() == "b" {
			t.Fatal("reserved credential was consumed")
		}
	}
}

func TestDeviceCounterRollbackAndForkAreRejected(t *testing.T) {
	base := Snapshot{Devices: []DeviceReceipt{
		{DeviceID: "d1", BootGeneration: 2, SignatureCounter: 8, CredentialID: "a", RPID: "rp", TransactionDigest: "tx1"},
		{DeviceID: "d1", BootGeneration: 3, SignatureCounter: 7, CredentialID: "a", RPID: "rp", TransactionDigest: "tx2"},
	}}
	if _, err := BuildProof(base); !errors.Is(err, ErrCounterRollback) {
		t.Fatalf("rollback error = %v, want ErrCounterRollback", err)
	}

	fork := Snapshot{Devices: []DeviceReceipt{
		{DeviceID: "d1", BootGeneration: 2, SignatureCounter: 8, CredentialID: "a", RPID: "rp", TransactionDigest: "tx1"},
		{DeviceID: "d1", BootGeneration: 2, SignatureCounter: 8, CredentialID: "a", RPID: "rp", TransactionDigest: "tx2"},
	}}
	if _, err := BuildProof(fork); !errors.Is(err, ErrDeviceFork) {
		t.Fatalf("fork error = %v, want ErrDeviceFork", err)
	}
}

func TestRPIsolationForSameCredentialID(t *testing.T) {
	a1 := credential("rp-1", "shared")
	a2 := credential("rp-2", "shared")
	snapshot := Snapshot{
		Devices: []DeviceReceipt{
			receipt("d1", 1, 1, a1),
			receipt("d2", 1, 1, a2),
		},
		Batches: []BatchSummary{
			{BatchID: "b1", RPID: "rp-1", CandidateCredentials: []CredentialRef{a1}, TransactionCount: 1},
		},
		Request: &PermitRequest{
			RPID:       "rp-2",
			Candidates: []CredentialRef{a2},
		},
	}
	proof, err := BuildProof(snapshot)
	if err != nil {
		t.Fatalf("BuildProof() error = %v", err)
	}
	if proof.Permit == nil || proof.Permit.Credential.RPID != "rp-2" {
		t.Fatalf("permit = %#v, want rp-2 credential", proof.Permit)
	}
}

func TestFixedAndUniversalSafetyModes(t *testing.T) {
	a := credential("rp", "a")
	b := credential("rp", "b")
	snapshot := Snapshot{
		Devices: []DeviceReceipt{receipt("d1", 1, 1, a, b)},
		Batches: []BatchSummary{
			{BatchID: "b1", RPID: "rp", CandidateCredentials: []CredentialRef{a, b}, TransactionCount: 1},
		},
		Request: &PermitRequest{
			RPID:       "rp",
			Candidates: []CredentialRef{a},
		},
	}
	fixed, err := BuildProof(snapshot)
	if err != nil {
		t.Fatalf("fixed proof error = %v", err)
	}
	if fixed.Permit == nil || fixed.Permit.Credential.CredentialIDValue() != "a" {
		t.Fatalf("fixed permit = %#v", fixed.Permit)
	}
	for _, ref := range fixed.Consumed {
		if ref.CredentialIDValue() == "a" {
			t.Fatal("fixed proof consumed the locked credential")
		}
	}

	snapshot.Request.Mode = SafetyUniversal
	if _, err := BuildProof(snapshot); !errors.Is(err, ErrCandidatesExhausted) {
		t.Fatalf("universal proof error = %v, want ErrCandidatesExhausted", err)
	}
}

func TestVerifierIsIdempotentAndRejectsASecondLock(t *testing.T) {
	a := credential("rp", "a")
	b := credential("rp", "b")
	snapshot := Snapshot{
		Devices: []DeviceReceipt{receipt("d1", 1, 1, a, b)},
		Batches: []BatchSummary{
			{BatchID: "b1", RPID: "rp", CandidateCredentials: []CredentialRef{a, b}, TransactionCount: 1},
		},
	}
	request := PermitRequest{
		RPID:       "rp",
		Purpose:    "sign",
		Candidates: []CredentialRef{a},
	}
	verifier := NewVerifier()
	first, err := verifier.VerifyAndIssue(snapshot, request)
	if err != nil {
		t.Fatalf("first issuance error = %v", err)
	}
	second, err := verifier.VerifyAndIssue(snapshot, request)
	if err != nil {
		t.Fatalf("second issuance error = %v", err)
	}
	if first.Permit.LockID != second.Permit.LockID {
		t.Fatal("idempotent verification issued a different lock")
	}
	request.Candidates = []CredentialRef{b}
	if _, err := verifier.VerifyAndIssue(snapshot, request); !errors.Is(err, ErrSnapshotConflict) {
		t.Fatalf("second lock error = %v, want ErrSnapshotConflict", err)
	}
}

func TestOracleParityOnRandomSmallSnapshots(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iteration := 0; iteration < 300; iteration++ {
		credentialCount := 4 + rng.Intn(4)
		refs := make([]CredentialRef, 0, credentialCount)
		receipts := make([]DeviceReceipt, 0, MaxDevices)
		for i := 0; i < credentialCount; i++ {
			refs = append(refs, credential("rp", fmt.Sprintf("c%d", i)))
		}
		deviceRefs := make([][]CredentialRef, MaxDevices)
		for index, ref := range refs {
			deviceRefs[index%MaxDevices] = append(deviceRefs[index%MaxDevices], ref)
		}
		for device := 0; device < MaxDevices; device++ {
			receipts = append(receipts, receipt(fmt.Sprintf("d%d", device), 1, 1, deviceRefs[device]...))
		}
		batchCount := 1 + rng.Intn(3)
		batches := make([]BatchSummary, 0, batchCount)
		for batch := 0; batch < batchCount; batch++ {
			size := 2 + rng.Intn(credentialCount-1)
			perm := rng.Perm(credentialCount)
			candidates := make([]CredentialRef, 0, size)
			for _, index := range perm[:size] {
				candidates = append(candidates, refs[index])
			}
			count := 1 + rng.Intn(size)
			batches = append(batches, BatchSummary{
				BatchID:              fmt.Sprintf("b%d", batch),
				RPID:                 "rp",
				CandidateCredentials: candidates,
				TransactionCount:     count,
			})
		}
		snapshot := Snapshot{Devices: receipts, Batches: batches}
		proof, proofErr := BuildProof(snapshot)
		oracle, oracleErr := Oracle(snapshot)
		if oracleErr != nil {
			t.Fatalf("iteration %d: oracle error = %v", iteration, oracleErr)
		}
		if (proofErr == nil) != oracle.Satisfiable {
			t.Fatalf("iteration %d: BuildProof error = %v, oracle satisfiable = %v", iteration, proofErr, oracle.Satisfiable)
		}
		if proofErr != nil {
			continue
		}
		if proof.AssignmentDigest == "" {
			t.Fatalf("iteration %d: empty assignment digest", iteration)
		}
		byKey := make(map[string]CredentialStatus)
		for _, status := range proof.Statuses {
			key, _ := status.Credential.key()
			byKey[key.String()] = status
		}
		for _, expected := range oracle.Statuses {
			key, _ := expected.Credential.key()
			actual, ok := byKey[key.String()]
			if !ok {
				t.Fatalf("iteration %d: missing status for %s", iteration, key.String())
			}
			if actual.Unavoidable != expected.Unavoidable ||
				actual.ConditionallyUsed != expected.ConditionallyUsed ||
				actual.UniversallyFree != expected.UniversallyFree {
				t.Fatalf("iteration %d: status mismatch for %s: got %+v want %+v",
					iteration, key.String(), actual, expected)
			}
		}
	}
}

func TestProofReorderingIsIdempotent(t *testing.T) {
	a := credential("rp", "a")
	b := credential("rp", "b")
	first := Snapshot{
		Devices: []DeviceReceipt{
			receipt("d1", 1, 2, a),
			receipt("d2", 1, 1, b),
		},
		Batches: []BatchSummary{
			{BatchID: "b1", RPID: "rp", CandidateCredentials: []CredentialRef{a, b}, TransactionCount: 1},
			{BatchID: "b2", RPID: "rp", CandidateCredentials: []CredentialRef{a, b}, TransactionCount: 1},
		},
	}
	second := Snapshot{
		Devices: []DeviceReceipt{
			receipt("d2", 1, 1, b),
			receipt("d1", 1, 2, a),
		},
		Batches: []BatchSummary{
			{BatchID: "b2", RPID: "rp", CandidateCredentials: []CredentialRef{b, a}, TransactionCount: 1},
			{BatchID: "b1", RPID: "rp", CandidateCredentials: []CredentialRef{b, a}, TransactionCount: 1},
		},
	}
	left, err := BuildProof(first)
	if err != nil {
		t.Fatalf("first proof error = %v", err)
	}
	right, err := BuildProof(second)
	if err != nil {
		t.Fatalf("second proof error = %v", err)
	}
	if left.SnapshotDigest != right.SnapshotDigest || left.AssignmentDigest != right.AssignmentDigest {
		t.Fatal("semantically reordered snapshot did not produce the same proof")
	}
}

func TestDuplicateBatchAndProofCheck(t *testing.T) {
	a := credential("rp", "a")
	b := credential("rp", "b")
	batch := BatchSummary{
		BatchID:              "b1",
		RPID:                 "rp",
		CandidateCredentials: []CredentialRef{a, b},
		TransactionCount:     1,
	}
	snapshot := Snapshot{
		Devices: []DeviceReceipt{receipt("d1", 1, 1, a, b)},
		Batches: []BatchSummary{batch, batch},
	}
	request := PermitRequest{RPID: "rp", Candidates: []CredentialRef{a}}
	proof, err := VerifyAndIssue(snapshot, request)
	if err != nil {
		t.Fatalf("VerifyAndIssue() error = %v", err)
	}
	if len(proof.Assignment) != 1 {
		t.Fatalf("Assignment length = %d, want 1 after duplicate-batch collapse", len(proof.Assignment))
	}
	if err := CheckProof(snapshot, proof); err != nil {
		t.Fatalf("CheckProof() with embedded proof request error = %v", err)
	}
}

func TestPurposeRestrictionAndReservedCandidate(t *testing.T) {
	a := CredentialRef{RPID: "rp", CredentialID: "a", AllowedPurposes: []string{"sign"}}
	b := CredentialRef{RPID: "rp", CredentialID: "b", AllowedPurposes: []string{"assert"}}
	snapshot := Snapshot{
		Devices: []DeviceReceipt{receipt("d1", 1, 1, a, b)},
		Batches: []BatchSummary{
			{BatchID: "b1", RPID: "rp", CandidateCredentials: []CredentialRef{a, b}, TransactionCount: 1, RequiredPurpose: "sign"},
		},
	}
	proof, err := BuildProof(snapshot)
	if err != nil {
		t.Fatalf("BuildProof() error = %v", err)
	}
	if len(proof.Consumed) != 1 || proof.Consumed[0].CredentialIDValue() != "a" {
		t.Fatalf("consumed = %#v, want credential a", proof.Consumed)
	}

	request := PermitRequest{
		RPID:       "rp",
		Purpose:    "sign",
		Candidates: []CredentialRef{b},
	}
	if _, err := VerifyAndIssue(snapshot, request); !errors.Is(err, ErrCandidatesExhausted) {
		t.Fatalf("purpose mismatch error = %v, want ErrCandidatesExhausted", err)
	}

	snapshot.Request = nil
	snapshot.Batches[0].RequiredPurpose = ""
	snapshot.Reservations = []Reservation{{CredentialID: "a"}}
	if _, err := BuildProof(snapshot); err != nil {
		t.Fatalf("reservation RP inference error = %v", err)
	}
}

func TestRejectsTooManyDevicesAndConflictingBatchIDs(t *testing.T) {
	devices := []DeviceReceipt{
		{DeviceID: "d1", BootGeneration: 1, SignatureCounter: 1, RPID: "rp", CredentialID: "a"},
		{DeviceID: "d2", BootGeneration: 1, SignatureCounter: 1, RPID: "rp", CredentialID: "b"},
		{DeviceID: "d3", BootGeneration: 1, SignatureCounter: 1, RPID: "rp", CredentialID: "c"},
		{DeviceID: "d4", BootGeneration: 1, SignatureCounter: 1, RPID: "rp", CredentialID: "d"},
	}
	if _, err := BuildProof(Snapshot{Devices: devices}); !errors.Is(err, ErrTooManyDevices) {
		t.Fatalf("too-many-devices error = %v, want ErrTooManyDevices", err)
	}

	batch := BatchSummary{BatchID: "same", RPID: "rp", CandidateCredentials: []CredentialRef{credential("rp", "a")}}
	changed := batch
	changed.TransactionCount = 1
	if _, err := BuildProof(Snapshot{Batches: []BatchSummary{batch, changed}}); !errors.Is(err, ErrBatchConflict) {
		t.Fatalf("batch conflict error = %v, want ErrBatchConflict", err)
	}
}

func TestTamperedProofIsRejected(t *testing.T) {
	a := credential("rp", "a")
	b := credential("rp", "b")
	snapshot := Snapshot{
		Devices: []DeviceReceipt{receipt("d1", 1, 1, a, b)},
		Batches: []BatchSummary{
			{BatchID: "b1", RPID: "rp", CandidateCredentials: []CredentialRef{a, b}, TransactionCount: 1},
		},
	}
	proof, err := BuildProof(snapshot)
	if err != nil {
		t.Fatalf("BuildProof() error = %v", err)
	}
	proof.Statuses[0].Available = !proof.Statuses[0].Available
	if err := CheckProof(snapshot, proof); !errors.Is(err, ErrProofMismatch) {
		t.Fatalf("tampered proof error = %v, want ErrProofMismatch", err)
	}
}

func TestProofJSONRoundTrip(t *testing.T) {
	a := credential("rp", "a")
	b := credential("rp", "b")
	snapshot := Snapshot{
		Devices: []DeviceReceipt{receipt("d1", 1, 1, a, b)},
		Batches: []BatchSummary{
			{BatchID: "b1", RPID: "rp", CandidateCredentials: []CredentialRef{a, b}, TransactionCount: 1},
		},
	}
	request := PermitRequest{RPID: "rp", Candidates: []CredentialRef{a}}
	proof, err := VerifyAndIssue(snapshot, request)
	if err != nil {
		t.Fatalf("VerifyAndIssue() error = %v", err)
	}
	encoded, err := json.Marshal(proof)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var decoded Proof
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if err := CheckProof(snapshot, &decoded); err != nil {
		t.Fatalf("CheckProof() after JSON round trip error = %v", err)
	}
}
