package netsec

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
)

func testDigest(i int) Digest {
	return sha256.Sum256([]byte(fmt.Sprintf("leaf-%d", i)))
}

func TestInclusionProofsForManyTreeShapes(t *testing.T) {
	for size := 1; size <= 17; size++ {
		leaves := make([]Digest, size)
		for i := range leaves {
			leaves[i] = testDigest(i)
		}
		checkpoint := NewCheckpoint("log", leaves)
		for index := 0; index < size; index++ {
			proof, err := BuildInclusionProof(leaves, uint64(index), checkpoint)
			if err != nil {
				t.Fatalf("size %d index %d: %v", size, index, err)
			}
			if err := VerifyInclusionProof(proof); err != nil {
				t.Fatalf("size %d index %d did not verify: %v", size, index, err)
			}

			tamperedLeaf := proof
			tamperedLeaf.LeafDigest = testDigest(index + 100)
			if err := VerifyInclusionProof(tamperedLeaf); !errors.Is(err, ErrProofInvalid) {
				t.Fatalf("size %d index %d tampered leaf: %v", size, index, err)
			}
			if len(proof.AuditPath) > 0 {
				tamperedPath := proof
				tamperedPath.AuditPath = cloneProof(&proof).AuditPath
				tamperedPath.AuditPath[0] = testDigest(index + 200)
				if err := VerifyInclusionProof(tamperedPath); !errors.Is(err, ErrProofInvalid) {
					t.Fatalf("size %d index %d tampered path: %v", size, index, err)
				}
			}
		}
	}
}

func TestInclusionProofBoundsAndCheckpointDigest(t *testing.T) {
	leaves := []Digest{testDigest(1)}
	checkpoint := NewCheckpoint("log-a", leaves)
	if checkpoint.Digest() == NewCheckpoint("log-b", leaves).Digest() {
		t.Fatal("different log IDs produced the same checkpoint digest")
	}
	if _, err := BuildInclusionProof(leaves, 1, checkpoint); !errors.Is(err, ErrProofInvalid) {
		t.Fatalf("out-of-range index: %v", err)
	}
	if _, err := BuildInclusionProof(nil, 0, LogCheckpoint{}); !errors.Is(err, ErrProofInvalid) {
		t.Fatalf("empty log: %v", err)
	}
	proof, _ := BuildInclusionProof(leaves, 0, checkpoint)
	proof.AuditPath = append(proof.AuditPath, testDigest(9))
	if err := VerifyInclusionProof(proof); !errors.Is(err, ErrProofInvalid) {
		t.Fatalf("extra audit path node: %v", err)
	}
}

func TestSnapshotRestoreAndNextVersion(t *testing.T) {
	f := newFixture(t)
	log := newFakeLog(f.logID)
	keyA, _ := newTestKey(t)
	client, err := NewClient(f.account, f.masterPub)
	if err != nil {
		t.Fatal(err)
	}
	v1 := f.list(t, 1, ZeroDigest, NewDevice("device-a", keyA, 1))
	signed1 := f.sign(t, v1)
	applyCommitted(t, client, signed1, log.publish(t, signed1))
	v2 := addDevice(t, v1, NewDevice("device-b", keyA, 2), 2)
	signed2 := f.sign(t, v2)
	applyCommitted(t, client, signed2, log.publish(t, signed2))

	snapshot, err := client.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewClientFromSnapshot(f.account, f.masterPub, snapshot)
	if err != nil {
		t.Fatalf("restore snapshot: %v", err)
	}
	if restored.Version() != 2 || restored.Digest() != client.Digest() {
		t.Fatalf("restored version/digest = %d/%s", restored.Version(), restored.Digest())
	}

	v3 := addDevice(t, v2, NewDevice("device-c", keyA, 3), 3)
	signed3 := f.sign(t, v3)
	applyCommitted(t, restored, signed3, log.publish(t, signed3))
	if restored.Version() != 3 {
		t.Fatalf("restored client version = %d", restored.Version())
	}
}

func TestSameSizeCheckpointForkIsRejected(t *testing.T) {
	f := newFixture(t)
	log := newFakeLog(f.logID)
	keyA, _ := newTestKey(t)
	client, err := NewClient(f.account, f.masterPub)
	if err != nil {
		t.Fatal(err)
	}
	v1 := f.list(t, 1, ZeroDigest, NewDevice("device-a", keyA, 1))
	signed1 := f.sign(t, v1)
	applyCommitted(t, client, signed1, log.publish(t, signed1))
	v2 := addDevice(t, v1, NewDevice("device-b", keyA, 2), 2)
	signed2 := f.sign(t, v2)
	applyCommitted(t, client, signed2, log.publish(t, signed2))

	v3 := addDevice(t, v2, NewDevice("device-c", keyA, 3), 3)
	signed3 := f.sign(t, v3)
	alternateTree := newFakeLog(f.logID, testDigest(77))
	proof3 := alternateTree.publish(t, signed3)
	if _, err := client.Apply(signed3, &proof3); !errors.Is(err, ErrCheckpointConflict) {
		t.Fatalf("same-size checkpoint fork: %v", err)
	}
}

type rejectingVerifier struct{}

func (rejectingVerifier) VerifyProof(LogCheckpoint, LogInclusionProof, Digest) error {
	return errors.New("unknown witness")
}

func TestHostVerifierRejectionIsNotCommitted(t *testing.T) {
	f := newFixture(t)
	log := newFakeLog(f.logID)
	keyA, _ := newTestKey(t)
	client, err := NewClient(f.account, f.masterPub, rejectingVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	v1 := f.list(t, 1, ZeroDigest, NewDevice("device-a", keyA, 1))
	signed1 := f.sign(t, v1)
	result, err := client.Apply(signed1, func() *LogInclusionProof {
		proof := log.publish(t, signed1)
		return &proof
	}())
	if !errors.Is(err, ErrProofInvalid) {
		t.Fatalf("host rejection: result=%+v err=%v", result, err)
	}
	if client.Version() != 0 || client.CanReceiveFrom("device-a") {
		t.Fatal("rejected proof authorized state")
	}
}
