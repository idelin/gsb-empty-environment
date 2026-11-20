package admission

import (
	"errors"
	"testing"
)

// TestVerifyRootOnlyAfterRestore 验证刚恢复、尚无收据的根快照也能通过独立校验。
func TestVerifyRootOnlyAfterRestore(t *testing.T) {
	host, policy := newTestHost(t, 1, 100)
	if _, err := host.Restore(bootFor(policy), policy, "root-wl", 200); err != nil {
		t.Fatalf("restore: %v", err)
	}
	snap := buildSnapshot(t, host)
	if len(snap.Receipts) != 0 || len(snap.Instances) != 1 {
		t.Fatalf("want root-only snapshot, got %d instances %d receipts", len(snap.Instances), len(snap.Receipts))
	}
	if err := VerifySnapshot(snap, host.PublicKey()); err != nil {
		t.Fatalf("root-only snapshot should verify: %v", err)
	}
}

// TestFailedBatchKeepsNoSideEffects 验证被拒绝的批次不发布实例、不消费票据，
// 预登记实例仍可由调用方显式回收后重新使用其随机数。
func TestFailedBatchKeepsNoSideEffects(t *testing.T) {
	host, _ := newTestHost(t, 1, 100)
	nonce := mustNonce(t)
	if _, err := host.PreregisterInstance("doomed", nonce, "worker", 1, sharedRW()); err != nil {
		t.Fatalf("preregister: %v", err)
	}
	claimDigest := workerClaimDigest(t)
	ticket := Ticket{Body: TicketBody{
		ParentClaimDigest: rootDigestOf(t, host),
		ChildClaimDigest:  claimDigest,
		ParentNonce:       mustNonce(t), // 不存在的父
		ChildNonce:        nonce,
		Generation:        100,
		PolicyVersion:     1,
		Purpose:           PurposeSpawn,
		Volumes:           sharedRW(),
		RemainingDepth:    0,
	}}
	signTickets(t, host, &ticket)

	if _, err := host.Submit(Batch{
		PolicyVersion: 1,
		Generation:    100,
		Requests:      []SpawnRequest{{Name: "doomed", Ticket: ticket}},
	}); !errors.Is(err, ErrParentNotFound) {
		t.Fatalf("want ErrParentNotFound, got %v", err)
	}

	state := host.CurrentState()
	if _, published := state.Instance(nonce); published {
		t.Fatal("rejected batch must not publish an instance")
	}
	ticketDigest, _ := ticket.Digest()
	if state.consumed[ticketDigest] {
		t.Fatal("rejected batch must not consume a ticket")
	}
	if _, reserved := state.Inflight(nonce); !reserved {
		t.Fatal("preregistration survives a failed batch for explicit retry/release")
	}
	if err := host.ReleaseInstance(nonce); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, reserved := host.CurrentState().Inflight(nonce); reserved {
		t.Fatal("released preregistration must disappear")
	}
}
