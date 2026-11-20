package admission

import (
	"errors"
	"testing"
)

// TestVerifySnapshotRejectsTampering 验证独立校验函数能识别伪造与篡改。
func TestVerifySnapshotRejectsTampering(t *testing.T) {
	host, _ := newTestHost(t, 1, 100)
	ticket := spawnTicket(t, host, rootNonce(t, host), "worker-verify", "worker", sharedRW(), 1)
	if _, err := host.Submit(Batch{
		PolicyVersion: 1,
		Generation:    100,
		Requests:      []SpawnRequest{{Name: "worker-verify", Ticket: ticket}},
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}
	snap := buildSnapshot(t, host)
	if err := VerifySnapshot(snap, host.PublicKey()); err != nil {
		t.Fatalf("valid snapshot should verify: %v", err)
	}

	// 用另一把证明公钥校验必须失败。
	otherPub, _ := testKey(t)
	if err := VerifySnapshot(snap, otherPub); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature with foreign key, got %v", err)
	}

	// 篡改已发布实例的卷权限：收据重建出的权限与快照不再一致。
	tampered := snap
	tampered.Instances = append([]Instance(nil), snap.Instances...)
	for i, inst := range tampered.Instances {
		if inst.Name == "worker-verify" {
			inst.Volumes = []VolumePerm{{Volume: volSecret, Read: true, Write: true}}
			tampered.Instances[i] = inst
		}
	}
	if err := VerifySnapshot(tampered, host.PublicKey()); !errors.Is(err, ErrSnapshotCoverage) {
		t.Fatalf("want ErrSnapshotCoverage for tampered volumes, got %v", err)
	}

	// 空快照被拒绝。
	if err := VerifySnapshot(Snapshot{}, host.PublicKey()); !errors.Is(err, ErrSnapshotEmpty) {
		t.Fatalf("want ErrSnapshotEmpty, got %v", err)
	}

	// 同一票据在两张收据中重复出现必须被识别为重放。
	replaySnap := snap
	replaySnap.Receipts = append([]Receipt(nil), snap.Receipts...)
	replaySnap.Receipts = append(replaySnap.Receipts, snap.Receipts...)
	if err := VerifySnapshot(replaySnap, host.PublicKey()); !errors.Is(err, ErrReceiptReplay) {
		t.Fatalf("want ErrReceiptReplay, got %v", err)
	}
}
