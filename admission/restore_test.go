package admission

import (
	"errors"
	"testing"
)

// TestStaleTicketAfterSnapshotRestore 验证快照恢复后旧代次票据立即失效，
// 同名工作负载拿到新随机数，旧请求无法借同名重建再次生效。
func TestStaleTicketAfterSnapshotRestore(t *testing.T) {
	host, policyV1 := newTestHost(t, 1, 100)
	root := rootNonce(t, host)

	// 旧代次签出的票据，准备使用与恢复后相同的工作负载名称。
	staleTicket := spawnTicket(t, host, root, "worker-same-name", "worker", sharedRW(), 1)

	// 快照恢复：代次严格递增，根实例重建且随机数轮换。
	oldRoot := root
	evidence, err := host.Restore(bootFor(policyV1), policyV1, "root-wl", 200)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	newRoot := host.CurrentState().Root()
	if newRoot.Nonce == oldRoot {
		t.Fatal("root nonce must rotate after restore")
	}
	if evidence.Body.RootNonce != newRoot.Nonce {
		t.Fatal("restored evidence nonce mismatch")
	}

	// 延迟到达的旧批次绑定代次 100，必须被拒绝。
	delayed := Batch{
		PolicyVersion: 1,
		Generation:    100,
		Requests:      []SpawnRequest{{Name: "worker-same-name", Ticket: staleTicket}},
	}
	if _, err := host.Submit(delayed); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("want ErrStaleGeneration, got %v", err)
	}

	// 恢复代次必须单调，回滚被拒绝。
	if _, err := host.Restore(bootFor(policyV1), policyV1, "root-wl", 150); !errors.Is(err, ErrGenerationRollback) {
		t.Fatalf("want ErrGenerationRollback, got %v", err)
	}

	// 同名工作负载以新实例随机数重新创建并成功，旧票仍不可复用。
	freshTicket := spawnTicket(t, host, newRoot.Nonce, "worker-same-name", "worker", sharedRW(), 1)
	receipt, err := host.Submit(Batch{
		PolicyVersion: 1,
		Generation:    200,
		Requests:      []SpawnRequest{{Name: "worker-same-name", Ticket: freshTicket}},
	})
	if err != nil {
		t.Fatalf("fresh same-name submit: %v", err)
	}
	if receipt.Body.Instances[0].Nonce == staleTicket.Body.ChildNonce {
		t.Fatal("rebuilt instance must not reuse the old instance nonce")
	}
	if _, err := host.Submit(delayed); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale ticket must stay invalid after rebuild, got %v", err)
	}

	if err := VerifySnapshot(buildSnapshot(t, host), host.PublicKey()); err != nil {
		t.Fatalf("snapshot verify after restore: %v", err)
	}
}

// TestOldPolicyTicketRejected 验证策略版本升级后旧策略票据不能再生效。
func TestOldPolicyTicketRejected(t *testing.T) {
	host, _ := newTestHost(t, 1, 100)
	oldTicket := spawnTicket(t, host, rootNonce(t, host), "worker-old-policy", "worker", sharedRW(), 1)

	policyV2 := testPolicy(2)
	if _, err := host.Restore(bootFor(policyV2), policyV2, "root-wl", 200); err != nil {
		t.Fatalf("restore v2: %v", err)
	}
	batch := Batch{
		PolicyVersion: 1,
		Generation:    200,
		Requests:      []SpawnRequest{{Name: "worker-old-policy", Ticket: oldTicket}},
	}
	if _, err := host.Submit(batch); !errors.Is(err, ErrPolicyVersion) {
		t.Fatalf("want ErrPolicyVersion, got %v", err)
	}
}

// TestReceiptLossIdempotent 验证回执丢失后用相同批次摘要取回原结果，
// 票据只消费一次，不会重复发布实例。
func TestReceiptLossIdempotent(t *testing.T) {
	host, _ := newTestHost(t, 1, 100)
	ticket := spawnTicket(t, host, rootNonce(t, host), "worker-once", "worker", sharedRW(), 1)
	batch := Batch{
		PolicyVersion: 1,
		Generation:    100,
		Requests:      []SpawnRequest{{Name: "worker-once", Ticket: ticket}},
	}

	first, err := host.Submit(batch)
	if err != nil {
		t.Fatalf("first submit: %v", err)
	}
	// 模拟回执丢失：调用方只保留批次，重新提交同一摘要。
	second, err := host.Submit(batch)
	if err != nil {
		t.Fatalf("idempotent resubmit: %v", err)
	}
	if first.Signature == nil || second.Signature == nil {
		t.Fatal("receipts must be signed")
	}
	if first.Body.BatchDigest != second.Body.BatchDigest {
		t.Fatal("replayed receipt must carry the same batch digest")
	}

	state := host.CurrentState()
	if len(state.instances) != 2 { // 根 + 单个 worker
		t.Fatalf("ticket consumed twice: instance count = %d", len(state.instances))
	}
	ticketDigest, _ := ticket.Digest()
	if !state.consumed[ticketDigest] {
		t.Fatal("ticket should be marked consumed")
	}

	// 已消费票据不能换个批次名称再次提交。
	replay := Batch{
		PolicyVersion: 1,
		Generation:    100,
		Requests:      []SpawnRequest{{Name: "worker-twice", Ticket: ticket}},
	}
	if _, err := host.Submit(replay); !errors.Is(err, ErrTicketConsumed) {
		t.Fatalf("want ErrTicketConsumed, got %v", err)
	}
}
