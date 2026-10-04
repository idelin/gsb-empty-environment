package inventory

import (
	"math/rand"
	"testing"
)

// 基本扣减：可用量随扣减减少，额度不足时报错且状态不变。
func TestDeductBasic(t *testing.T) {
	r := NewReplica("A", 10)
	if err := r.Deduct(4); err != nil {
		t.Fatalf("deduct: %v", err)
	}
	if got := r.Available(); got != 6 {
		t.Fatalf("available = %d, want 6", got)
	}
	if err := r.Deduct(7); err == nil {
		t.Fatal("expected insufficient error")
	}
	if got := r.Available(); got != 6 {
		t.Fatalf("failed deduct mutated state: available = %d", got)
	}
	if err := r.Deduct(0); err == nil {
		t.Fatal("expected non-positive error")
	}
}

// 总库存永不为负：各副本 spent 之和不超过总额。
func TestNeverNegative(t *testing.T) {
	a, b := NewReplica("A", 3), NewReplica("B", 2)
	for i := 0; i < 10; i++ {
		a.Deduct(1)
		b.Deduct(1)
	}
	if total := a.Spent() + b.Spent(); total != 5 {
		t.Fatalf("total spent = %d, want 5", total)
	}
	if a.Available() != 0 || b.Available() != 0 {
		t.Fatalf("available should be drained: %d, %d", a.Available(), b.Available())
	}
}

// 转让：转出方立即失去额度，接收方收到消息后才获得额度。
func TestTransfer(t *testing.T) {
	a, b := NewReplica("A", 10), NewReplica("B", 0)
	msg, err := a.Grant("B", 6)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if got := a.Available(); got != 4 {
		t.Fatalf("A available = %d, want 4", got)
	}
	// 消息未到达前 B 不得预支。
	if err := b.Deduct(1); err == nil {
		t.Fatal("B must not spend in-flight quota")
	}
	b.Receive(msg)
	if got := b.Available(); got != 6 {
		t.Fatalf("B available = %d, want 6", got)
	}
	if err := b.Deduct(6); err != nil {
		t.Fatalf("B deduct: %v", err)
	}
}

// 转让消息重复投递不会重复授予。
func TestTransferIdempotent(t *testing.T) {
	a, b := NewReplica("A", 10), NewReplica("B", 0)
	msg, _ := a.Grant("B", 6)
	for i := 0; i < 5; i++ {
		b.Receive(msg) // 网络重传/重复投递
	}
	if got := b.Available(); got != 6 {
		t.Fatalf("duplicate delivery double-granted: B available = %d, want 6", got)
	}
}

// 转出方不能转让超出自己可用量的额度。
func TestGrantInsufficient(t *testing.T) {
	a := NewReplica("A", 5)
	a.Deduct(3)
	if _, err := a.Grant("B", 4); err == nil {
		t.Fatal("grant of spent quota must fail")
	}
	if got := a.Available(); got != 2 {
		t.Fatalf("failed grant mutated state: available = %d", got)
	}
}

// 合并幂等且可交换：反复合并、乱序合并后状态一致。
func TestMergeIdempotentAndCommutative(t *testing.T) {
	a, b, c := NewReplica("A", 10), NewReplica("B", 0), NewReplica("C", 0)
	m1, _ := a.Grant("B", 4)
	m2, _ := a.Grant("C", 3)

	// 乱序、重复投递。
	c.Receive(m2)
	b.Receive(m1)
	c.Receive(m1) // 转发获悉，不属于自己的不授予
	b.Receive(m2)
	b.MergeFrom(c)
	c.MergeFrom(b)
	a.MergeFrom(b)
	a.MergeFrom(c)
	b.MergeFrom(a)
	c.MergeFrom(a)

	if a.Available() != 3 || b.Available() != 4 || c.Available() != 3 {
		t.Fatalf("available = %d,%d,%d, want 3,4,4...3", a.Available(), b.Available(), c.Available())
	}
	for _, r := range []*Replica{a, b, c} {
		if len(r.Ledger()) != 2 {
			t.Fatalf("replica %s ledger size = %d, want 2", r.ID, len(r.Ledger()))
		}
	}
}

// 分区重连测试：分区期间各组独立扣减与转让，
// 重连合并后总额度守恒，总库存不为负。
func TestPartitionReconnectConservation(t *testing.T) {
	const total = 100
	a, b, c := NewReplica("A", 40), NewReplica("B", 30), NewReplica("C", 30)
	net := NewNetwork(a, b, c)

	// 分区：{A,B} 与 {C} 隔离。
	net.Partition([]string{"A", "B"})

	// {A,B} 组内：A 额度不足，向 B 求援。
	a.Deduct(38)
	msg, err := b.Grant("A", 20)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	a.Receive(msg) // 同组，可达
	a.Deduct(20)   // A 共扣 58，超过其初始 40，靠转让完成

	// C 离线独立扣减。
	c.Deduct(25)

	// 分区期间不变量：rights + 在途 == total（此处无跨组在途消息）。
	if got := net.TotalRights() + net.InFlight(); got != total {
		t.Fatalf("during partition: rights+inflight = %d, want %d", got, total)
	}
	if net.TotalSpent() > total {
		t.Fatalf("overspent: %d > %d", net.TotalSpent(), total)
	}

	// 重连并合并。
	net.Heal()
	net.Sync()

	if got := net.TotalRights(); got != total {
		t.Fatalf("after heal: total rights = %d, want %d", got, total)
	}
	if got := net.TotalSpent(); got != 83 {
		t.Fatalf("total spent = %d, want 83", got)
	}
	if got := net.TotalRights() - net.TotalSpent(); got != 17 {
		t.Fatalf("remaining stock = %d, want 17", got)
	}
	// 所有副本流水收敛一致。
	if len(a.Ledger()) != 1 || len(b.Ledger()) != 1 || len(c.Ledger()) != 1 {
		t.Fatal("ledgers did not converge")
	}
}

// 跨分区转让：消息在分区期间发出但无法送达，接收方不得预支；
// 重连后送达且只授予一次。
func TestCrossPartitionTransfer(t *testing.T) {
	const total = 50
	a, b := NewReplica("A", 50), NewReplica("B", 0)
	net := NewNetwork(a, b)
	net.Partition([]string{"A"}) // A 与 B 隔离

	msg, _ := a.Grant("B", 20)
	net.Sync() // 分区中：消息送不到 B
	if got := b.Available(); got != 0 {
		t.Fatalf("B spent unreceived quota: %d", got)
	}
	if err := b.Deduct(1); err == nil {
		t.Fatal("B must not deduct in-flight quota")
	}
	// 守恒：20 额度在途。
	if got := net.TotalRights() + net.InFlight(); got != total {
		t.Fatalf("rights+inflight = %d, want %d", got, total)
	}

	net.Heal()
	b.Receive(msg)
	b.Receive(msg) // 重连后重传
	net.Sync()
	if got := b.Available(); got != 20 {
		t.Fatalf("B available = %d, want 20", got)
	}
	if got := net.TotalRights(); got != total {
		t.Fatalf("total rights = %d, want %d", got, total)
	}
}

// 随机压力测试：随机分区、扣减、转让、合并，
// 每一步都校验守恒不变量，最终收敛后总额度等于总库存。
func TestRandomizedConservation(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 1999, 20261005} {
		runRandomized(t, seed)
	}
}

func runRandomized(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	const total = 300
	ids := []string{"A", "B", "C", "D"}
	reps := make([]*Replica, len(ids))
	for i, id := range ids {
		reps[i] = NewReplica(id, total/int64(len(ids)))
	}
	net := NewNetwork(reps...)

	check := func(phase string) {
		t.Helper()
		if got := net.TotalRights() + net.InFlight(); got != total {
			t.Fatalf("%s: rights+inflight = %d, want %d", phase, got, total)
		}
		if net.TotalSpent() > total {
			t.Fatalf("%s: overspent %d > %d", phase, net.TotalSpent(), total)
		}
		for _, r := range reps {
			if r.Available() < 0 {
				t.Fatalf("%s: replica %s available = %d < 0", phase, r.ID, r.Available())
			}
		}
	}

	for round := 0; round < 200; round++ {
		// 随机重新分区（1~3 组）。
		ng := 1 + rng.Intn(3)
		groups := make([][]string, ng)
		for _, id := range ids {
			g := rng.Intn(ng)
			groups[g] = append(groups[g], id)
		}
		var nonEmpty [][]string
		for _, g := range groups {
			if len(g) > 0 {
				nonEmpty = append(nonEmpty, g)
			}
		}
		net.Partition(nonEmpty...)

		// 随机操作：扣减或转让（可能失败，失败即跳过）。
		for op := 0; op < 10; op++ {
			r := reps[rng.Intn(len(reps))]
			if rng.Intn(2) == 0 {
				r.Deduct(int64(1 + rng.Intn(10)))
			} else {
				to := ids[rng.Intn(len(ids))]
				if msg, err := r.Grant(to, int64(1+rng.Intn(10))); err == nil {
					// 仅当同组时消息可达；重复投递若干次。
					if net.groups[r.ID] == net.groups[to] {
						for i := 0; i <= rng.Intn(3); i++ {
							for _, rr := range reps {
								if rr.ID == to {
									rr.Receive(msg)
								}
							}
						}
					}
				}
			}
		}
		net.Sync() // 组内 gossip
		check("partition round")
	}

	net.Heal()
	net.Sync()
	check("after heal")
	if got := net.TotalRights(); got != total {
		t.Fatalf("final total rights = %d, want %d", got, total)
	}
	// 流水收敛：所有副本看到相同的消息集合。
	base := reps[0].Ledger()
	for _, r := range reps[1:] {
		if len(r.Ledger()) != len(base) {
			t.Fatalf("ledger divergence: %s has %d, want %d", r.ID, len(r.Ledger()), len(base))
		}
	}
}
