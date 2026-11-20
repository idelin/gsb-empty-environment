package admission

import (
	"errors"
	"testing"
)

func workerClaimDigest(t *testing.T) Digest {
	t.Helper()
	digest, err := testPolicy(1).ClaimDigest("worker")
	if err != nil {
		t.Fatalf("worker claim digest: %v", err)
	}
	return digest
}

// TestCyclicForwardReference 验证环形前向引用被无环图校验拒绝，整批不发布。
func TestCyclicForwardReference(t *testing.T) {
	host, _ := newTestHost(t, 1, 100)

	alphaNonce := mustNonce(t)
	betaNonce := mustNonce(t)
	claimDigest := workerClaimDigest(t)
	if _, err := host.PreregisterInstance("alpha", alphaNonce, "worker", 1, sharedRW()); err != nil {
		t.Fatalf("preregister alpha: %v", err)
	}
	if _, err := host.PreregisterInstance("beta", betaNonce, "worker", 1, sharedRW()); err != nil {
		t.Fatalf("preregister beta: %v", err)
	}

	// 手工构造环：alpha 的父是 beta，beta 的父是 alpha。两个随机数互不相同，
	// 因而绕过重复随机数检查并直达环形图检测。
	alphaTicket := Ticket{Body: TicketBody{
		ParentClaimDigest: rootDigestOf(t, host),
		ChildClaimDigest:  claimDigest,
		ParentNonce:       betaNonce,
		ChildNonce:        alphaNonce,
		Generation:        100,
		PolicyVersion:     1,
		Purpose:           PurposeSpawn,
		Volumes:           sharedRW(),
		RemainingDepth:    0,
	}}
	betaTicket := Ticket{Body: TicketBody{
		ParentClaimDigest: rootDigestOf(t, host),
		ChildClaimDigest:  claimDigest,
		ParentNonce:       alphaNonce,
		ChildNonce:        betaNonce,
		Generation:        100,
		PolicyVersion:     1,
		Purpose:           PurposeSpawn,
		Volumes:           sharedRW(),
		RemainingDepth:    0,
	}}
	signTickets(t, host, &alphaTicket, &betaTicket)

	batch := Batch{
		PolicyVersion: 1,
		Generation:    100,
		Requests: []SpawnRequest{
			{Name: "alpha", Ticket: alphaTicket},
			{Name: "beta", Ticket: betaTicket},
		},
	}
	if _, err := host.Submit(batch); !errors.Is(err, ErrCyclic) {
		t.Fatalf("want ErrCyclic, got %v", err)
	}
	if _, ok := host.CurrentState().InstanceByName("alpha"); ok {
		t.Fatal("no instance may be published after rejected batch")
	}

	// 自环同样必须被识别为环。
	selfNonce := mustNonce(t)
	if _, err := host.PreregisterInstance("self", selfNonce, "worker", 1, sharedRW()); err != nil {
		t.Fatalf("preregister self: %v", err)
	}
	selfTicket := alphaTicket
	selfTicket.Body.ChildNonce = selfNonce
	selfTicket.Body.ParentNonce = selfNonce
	signTickets(t, host, &selfTicket)
	selfBatch := Batch{
		PolicyVersion: 1,
		Generation:    100,
		Requests:      []SpawnRequest{{Name: "self", Ticket: selfTicket}},
	}
	if _, err := host.Submit(selfBatch); !errors.Is(err, ErrCyclic) {
		t.Fatalf("want ErrCyclic for self loop, got %v", err)
	}
}

// TestDisconnectedSubtree 验证无法从已认证根到达的子树被拒绝（跨树拼接的本质）。
func TestDisconnectedSubtree(t *testing.T) {
	host, _ := newTestHost(t, 1, 100)
	orphanParent := mustNonce(t) // 宿主视图中不存在的父随机数
	orphanNonce := mustNonce(t)
	if _, err := host.PreregisterInstance("orphan", orphanNonce, "worker", 1, sharedRW()); err != nil {
		t.Fatalf("preregister orphan: %v", err)
	}
	claimDigest := workerClaimDigest(t)
	ticket := Ticket{Body: TicketBody{
		ParentClaimDigest: rootDigestOf(t, host),
		ChildClaimDigest:  claimDigest,
		ParentNonce:       orphanParent,
		ChildNonce:        orphanNonce,
		Generation:        100,
		PolicyVersion:     1,
		Purpose:           PurposeSpawn,
		Volumes:           sharedRW(),
		RemainingDepth:    0,
	}}
	signTickets(t, host, &ticket)

	_, err := host.Submit(Batch{
		PolicyVersion: 1,
		Generation:    100,
		Requests:      []SpawnRequest{{Name: "orphan", Ticket: ticket}},
	})
	if !errors.Is(err, ErrParentNotFound) {
		t.Fatalf("want ErrParentNotFound, got %v", err)
	}
}

// TestCrossTreeBinding 验证不能从两棵合法树分别取父身份和卷权限拼接。
func TestCrossTreeBinding(t *testing.T) {
	pub, priv := testKey(t)
	policy := testPolicy(1)

	// 两棵共享同一证明密钥但根实例不同的合法树。
	hostA := NewHost(NewMemoryStore(), pub, priv)
	if _, err := hostA.Bootstrap(bootFor(policy), policy, "root-a", 100); err != nil {
		t.Fatalf("bootstrap A: %v", err)
	}
	hostB := NewHost(NewMemoryStore(), pub, priv)
	if _, err := hostB.Bootstrap(bootFor(policy), policy, "root-b", 100); err != nil {
		t.Fatalf("bootstrap B: %v", err)
	}

	// 树 B 根签发的合法票据拿到树 A 提交：父随机数在 A 的视图中不存在，
	// 父身份无法嫁接到 A 的实例树。
	foreignTicket := spawnTicket(t, hostB, hostB.CurrentState().Root().Nonce, "intruder", "worker", sharedRW(), 1)
	if _, err := hostA.PreregisterInstance("intruder", foreignTicket.Body.ChildNonce, "worker", 1, sharedRW()); err != nil {
		t.Fatalf("preregister intruder in tree A: %v", err)
	}
	_, err := hostA.Submit(Batch{
		PolicyVersion: 1,
		Generation:    100,
		Requests:      []SpawnRequest{{Name: "intruder", Ticket: foreignTicket}},
	})
	if !errors.Is(err, ErrParentNotFound) {
		t.Fatalf("want ErrParentNotFound for foreign parent, got %v", err)
	}

	// 若外树根实例被注入宿主 A 的已发布视图，父链能解析却终止于外树根：
	// 此时必须识别为跨树拼接而不是普通的父缺失。
	foreignRoot := hostB.CurrentState().Root()
	injected := hostA.CurrentState().clone()
	injected.instances[foreignRoot.Nonce] = foreignRoot
	if err := hostA.store.Replace(injected); err != nil {
		t.Fatalf("inject foreign root: %v", err)
	}
	crossNonce := mustNonce(t)
	if _, err := hostA.PreregisterInstance("cross", crossNonce, "worker", 1, sharedRW()); err != nil {
		t.Fatalf("preregister cross: %v", err)
	}
	crossTicket := Ticket{Body: TicketBody{
		ParentClaimDigest: foreignRoot.ClaimDigest,
		ChildClaimDigest:  workerClaimDigest(t),
		ParentNonce:       foreignRoot.Nonce,
		ChildNonce:        crossNonce,
		Generation:        100,
		PolicyVersion:     1,
		Purpose:           PurposeSpawn,
		Volumes:           sharedRW(),
		RemainingDepth:    0,
	}}
	signTickets(t, hostA, &crossTicket)
	if _, err := hostA.Submit(Batch{
		PolicyVersion: 1,
		Generation:    100,
		Requests:      []SpawnRequest{{Name: "cross", Ticket: crossTicket}},
	}); !errors.Is(err, ErrCrossTree) {
		t.Fatalf("want ErrCrossTree, got %v", err)
	}

	// 卷权限拼接：auditor 仅持 vol-audit 只读，夹带 vol-secret 的票据即使
	// 签名合法也必须在提交期被权限子集校验拒绝。
	claimDigest := workerClaimDigest(t)
	escalationNonce := mustNonce(t)
	if _, err := hostA.PreregisterInstance("auditor-escalation", escalationNonce, "worker", 1, sharedRW()); err != nil {
		t.Fatalf("preregister escalation: %v", err)
	}
	auditorTicket := Ticket{Body: TicketBody{
		ParentClaimDigest: rootDigestOf(t, hostA),
		ChildClaimDigest:  claimDigest,
		ParentNonce:       hostA.CurrentState().Root().Nonce,
		ChildNonce:        escalationNonce,
		Generation:        100,
		PolicyVersion:     1,
		Purpose:           PurposeSpawn,
		Volumes: []VolumePerm{
			{Volume: volAudit, Read: true, Write: false},
			{Volume: volSecret, Read: true, Write: true},
		},
		RemainingDepth: 0,
	}}
	signTickets(t, hostA, &auditorTicket)
	_, err = hostA.Submit(Batch{
		PolicyVersion: 1,
		Generation:    100,
		Requests:      []SpawnRequest{{Name: "auditor-escalation", Ticket: auditorTicket}},
	})
	if !errors.Is(err, ErrPermission) {
		t.Fatalf("want ErrPermission for volume splicing, got %v", err)
	}

	// 正常签出阶段同样拦截越权转授与超层委托。
	if _, err := hostA.CheckoutTicket(CheckoutRequest{
		ParentNonce:    hostA.CurrentState().Root().Nonce,
		ChildClaimID:   "worker",
		ChildNonce:     mustNonce(t),
		Purpose:        PurposeSpawn,
		Volumes:        []VolumePerm{{Volume: volSecret, Read: true, Write: false}},
		RemainingDepth: 0,
	}); !errors.Is(err, ErrPermission) {
		t.Fatalf("worker claim cannot carry vol-secret, got %v", err)
	}
	if _, err := hostA.CheckoutTicket(CheckoutRequest{
		ParentNonce:    hostA.CurrentState().Root().Nonce,
		ChildClaimID:   "worker",
		ChildNonce:     mustNonce(t),
		Purpose:        PurposeSpawn,
		Volumes:        sharedRW(),
		RemainingDepth: 9,
	}); !errors.Is(err, ErrPermission) {
		t.Fatalf("delegation depth above limit must fail, got %v", err)
	}
}
