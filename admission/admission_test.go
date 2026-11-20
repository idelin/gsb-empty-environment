package admission

import (
	"crypto/ed25519"
	"testing"
)

const (
	volShared = VolumeID("vol-shared")
	volAudit  = VolumeID("vol-audit")
	volSecret = VolumeID("vol-secret")
)

func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv
}

func testPolicy(version uint64) Policy {
	return Policy{
		Version: version,
		Tree: ClaimTree{
			Root: "root",
			Claims: map[string]Claim{
				"root": {
					ID:                 "root",
					AllowedChildren:    []string{"worker", "auditor"},
					Volumes:            []VolumePerm{{Volume: volShared, Read: true, Write: true}, {Volume: volAudit, Read: true, Write: false}, {Volume: volSecret, Read: true, Write: true}},
					MaxDelegationDepth: 2,
				},
				"worker": {
					ID:                 "worker",
					AllowedChildren:    []string{"worker"},
					Volumes:            []VolumePerm{{Volume: volShared, Read: true, Write: true}},
					MaxDelegationDepth: 2,
				},
				"auditor": {
					ID:                 "auditor",
					AllowedChildren:    nil,
					Volumes:            []VolumePerm{{Volume: volAudit, Read: true, Write: false}},
					MaxDelegationDepth: 0,
				},
			},
		},
	}
}

func bootFor(policy Policy) BootMeasurement {
	rootDigest, _ := policy.ClaimDigest(policy.Tree.Root)
	return BootMeasurement{
		Measurement:     Digest{0xab, 0xcd},
		PolicyVersion:   policy.Version,
		RootClaimDigest: rootDigest,
	}
}

func newTestHost(t *testing.T, version uint64, generation uint64) (*Host, Policy) {
	t.Helper()
	pub, priv := testKey(t)
	host := NewHost(NewMemoryStore(), pub, priv)
	policy := testPolicy(version)
	if _, err := host.Bootstrap(bootFor(policy), policy, "root-wl", generation); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return host, policy
}

func mustNonce(t *testing.T) Nonce {
	t.Helper()
	nonce, err := NewNonce()
	if err != nil {
		t.Fatalf("nonce: %v", err)
	}
	return nonce
}

func rootNonce(t *testing.T, host *Host) Nonce {
	t.Helper()
	return host.CurrentState().Root().Nonce
}

func rootDigestOf(t *testing.T, host *Host) Digest {
	t.Helper()
	return host.CurrentState().Root().ClaimDigest
}

// spawnTicket 从指定父实例签出一张合法 spawn 票据。
func spawnTicket(t *testing.T, host *Host, parent Nonce, name, childClaim string, volumes []VolumePerm, depth int) Ticket {
	t.Helper()
	childNonce := mustNonce(t)
	parentInst, ok := host.CurrentState().Instance(parent)
	if !ok {
		parentInst, ok = host.CurrentState().inflight[parent]
	}
	if !ok {
		t.Fatalf("parent %x not found for preregister", parent[:4])
	}
	if _, err := host.PreregisterInstance(name, childNonce, childClaim, parentInst.Depth+1, volumes); err != nil {
		t.Fatalf("preregister %s: %v", name, err)
	}
	ticket, err := host.CheckoutTicket(CheckoutRequest{
		ParentNonce:    parent,
		ChildClaimID:   childClaim,
		ChildNonce:     childNonce,
		Purpose:        PurposeSpawn,
		Volumes:        volumes,
		RemainingDepth: depth,
	})
	if err != nil {
		t.Fatalf("checkout ticket: %v", err)
	}
	return ticket
}

func signTickets(t *testing.T, host *Host, tickets ...*Ticket) {
	t.Helper()
	for _, ticket := range tickets {
		ticket.Signature = nil
		if err := ticket.Sign(host.priv); err != nil {
			t.Fatalf("sign ticket: %v", err)
		}
	}
}

func sharedRW() []VolumePerm {
	return []VolumePerm{{Volume: volShared, Read: true, Write: true}}
}

// buildSnapshot 从宿主当前发布状态收集独立校验所需的全部材料。
func buildSnapshot(t *testing.T, host *Host) Snapshot {
	t.Helper()
	state := host.CurrentState()
	instances := make([]Instance, 0, len(state.instances))
	for _, inst := range state.instances {
		instances = append(instances, inst)
	}
	receipts := make([]Receipt, 0, len(state.batchReceipts))
	for _, receipt := range state.batchReceipts {
		receipts = append(receipts, receipt)
	}
	return Snapshot{
		Root:      state.rootEvidence,
		Policy:    state.policy,
		Instances: instances,
		Receipts:  receipts,
	}
}

// TestForwardReferenceAndHappyPath 验证批请求可先引用尚未提交的子节点，
// 整体提交时形成一棵从根出发的无环树。
func TestForwardReferenceAndHappyPath(t *testing.T) {
	host, _ := newTestHost(t, 1, 100)
	root := rootNonce(t, host)

	workerTicket := spawnTicket(t, host, root, "worker-a", "worker", sharedRW(), 1)
	firstReceipt, err := host.Submit(Batch{
		PolicyVersion: 1,
		Generation:    100,
		Requests:      []SpawnRequest{{Name: "worker-a", Ticket: workerTicket}},
	})
	if err != nil {
		t.Fatalf("submit worker: %v", err)
	}
	workerInst := firstReceipt.Body.Instances[0]

	grandTicket := spawnTicket(t, host, workerInst.Nonce, "worker-grand", "worker", sharedRW(), 0)

	// 同批内的前向引用：worker-b 尚未提交，worker-c 先引用它作为父。
	forwardTicket := spawnTicket(t, host, root, "worker-b", "worker", sharedRW(), 1)
	forwardChild := forwardTicket.Body.ChildNonce
	childOfForward := spawnTicket(t, host, forwardChild, "worker-c", "worker", sharedRW(), 0)

	batch := Batch{
		PolicyVersion: 1,
		Generation:    100,
		Requests: []SpawnRequest{
			{Name: "worker-c", Ticket: childOfForward},
			{Name: "worker-b", Ticket: forwardTicket},
			{Name: "worker-grand", Ticket: grandTicket},
		},
	}
	receipt, err := host.Submit(batch)
	if err != nil {
		t.Fatalf("submit forward-reference batch: %v", err)
	}
	if len(receipt.Body.Instances) != 3 {
		t.Fatalf("want 3 instances, got %d", len(receipt.Body.Instances))
	}
	if err := VerifySnapshot(buildSnapshot(t, host), host.PublicKey()); err != nil {
		t.Fatalf("snapshot verify: %v", err)
	}
}
