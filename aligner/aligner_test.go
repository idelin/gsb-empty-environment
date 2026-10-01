package aligner

import (
	"testing"
)

const (
	shard1 = "shard-1"
	shard2 = "shard-2"
)

// seed 通过两个单分片事务给账户充值，并推进水位线使其生效。
func seed(t *testing.T, a *Aligner) {
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	must(a.Ingest(Event{ShardID: shard1, TxnID: "seed-a", TS: 1, Kind: OpCredit, Account: "A", Amount: 1000, Parts: 1}))
	must(a.Ingest(Event{ShardID: shard2, TxnID: "seed-b", TS: 1, Kind: OpCredit, Account: "B", Amount: 500, Parts: 1}))
	must(a.Heartbeat(shard1, 5))
	must(a.Heartbeat(shard2, 5))

	snap := a.Snapshot()
	if snap.Balances["A"] != 1000 || snap.Balances["B"] != 500 {
		t.Fatalf("seed failed: %+v", snap.Balances)
	}
}

// TestTransferBlockedUntilClosed 验证验收边界：
// 转账事务在转入与转出事件全部就绪之前，外部无法生成包含它的查询视图。
func TestTransferBlockedUntilClosed(t *testing.T) {
	a := New(shard1, shard2)
	seed(t, a)

	// 分库1 的转出事件先到（A -100 @ ts=10）。
	if err := a.Ingest(Event{ShardID: shard1, TxnID: "tx-1", TS: 10, Kind: OpDebit, Account: "A", Amount: 100, Parts: 2}); err != nil {
		t.Fatal(err)
	}
	// 两个分片水位线都已推进到 15，但事务未闭合。
	if err := a.Heartbeat(shard1, 15); err != nil {
		t.Fatal(err)
	}
	if err := a.Heartbeat(shard2, 15); err != nil {
		t.Fatal(err)
	}

	// T_Safe 必须被未闭合事务阻挡在 9。
	if got := a.SafeTS(); got != 9 {
		t.Fatalf("SafeTS = %d, want 9 (blocked by unclosed txn)", got)
	}
	// 请求 ts>=10 的视图必须被拒绝。
	if _, err := a.SnapshotAt(10); err != ErrNotClosed {
		t.Fatalf("SnapshotAt(10) err = %v, want ErrNotClosed", err)
	}
	// 当前快照不得包含转出（A 仍为 1000）。
	snap := a.Snapshot()
	if snap.Balances["A"] != 1000 {
		t.Fatalf("debit leaked into snapshot before closure: A=%d", snap.Balances["A"])
	}

	// 分库2 的转入事件到达（B +100 @ ts=12），事务闭合。
	if err := a.Ingest(Event{ShardID: shard2, TxnID: "tx-1", TS: 12, Kind: OpCredit, Account: "B", Amount: 100, Parts: 2}); err != nil {
		t.Fatal(err)
	}
	// 提交时间戳取 max(10,12)=12，水位线 15 已覆盖，T_Safe 推进到 15。
	if got := a.SafeTS(); got != 15 {
		t.Fatalf("SafeTS = %d, want 15 after closure", got)
	}
	snap = a.Snapshot()
	if snap.Balances["A"] != 900 || snap.Balances["B"] != 600 {
		t.Fatalf("closed transfer not applied: %+v", snap.Balances)
	}
	// 现在可以查询 ts=10 的视图（不包含该事务，因其 commitTS=12）。
	snap10, err := a.SnapshotAt(10)
	if err != nil {
		t.Fatal(err)
	}
	if snap10.Balances["A"] != 1000 {
		t.Fatalf("snapshot@10 should exclude txn committed at 12: %+v", snap10.Balances)
	}
}

// TestMoneyNeverDisappears 资金守恒不变式：任意时刻生成的快照，
// 总余额必须等于初始总余额，且不存在"已扣款但未入账"的中间态视图。
func TestMoneyNeverDisappears(t *testing.T) {
	a := New(shard1, shard2)
	seed(t, a)
	const wantTotal = 1500

	checkInvariant := func(step string) {
		t.Helper()
		snap := a.Snapshot()
		if got := snap.Total(); got != wantTotal {
			t.Fatalf("%s: money vanished! total=%d want=%d (balances=%+v)", step, got, wantTotal, snap.Balances)
		}
		// 转账中间态检测：A 少了 100 时 B 必须同时多了 100。
		aBal, bBal := snap.Balances["A"], snap.Balances["B"]
		if (aBal < 1000) != (bBal > 500) && aBal >= 900 && bBal <= 600 {
			t.Fatalf("%s: partial transfer visible! A=%d B=%d", step, aBal, bBal)
		}
	}

	// 交错送达 3 笔转账的事件，每步都校验不变式。
	events := []Event{
		{ShardID: shard1, TxnID: "tx-1", TS: 10, Kind: OpDebit, Account: "A", Amount: 100, Parts: 2},
		{ShardID: shard1, TxnID: "tx-2", TS: 11, Kind: OpDebit, Account: "A", Amount: 50, Parts: 2},
		{ShardID: shard2, TxnID: "tx-1", TS: 12, Kind: OpCredit, Account: "B", Amount: 100, Parts: 2},
		{ShardID: shard2, TxnID: "tx-3", TS: 13, Kind: OpCredit, Account: "B", Amount: 200, Parts: 2},
		{ShardID: shard1, TxnID: "tx-3", TS: 14, Kind: OpDebit, Account: "A", Amount: 200, Parts: 2},
		{ShardID: shard2, TxnID: "tx-2", TS: 15, Kind: OpCredit, Account: "B", Amount: 50, Parts: 2},
	}
	for i, ev := range events {
		if err := a.Ingest(ev); err != nil {
			t.Fatal(err)
		}
		checkInvariant(ev.TxnID)
		// 水位线逐步推进，每推进一步也校验。
		wm := uint64(10 + i)
		if err := a.Heartbeat(shard1, wm); err != nil {
			t.Fatal(err)
		}
		if err := a.Heartbeat(shard2, wm); err != nil {
			t.Fatal(err)
		}
		checkInvariant(ev.TxnID + " after heartbeat")
	}

	final := a.Snapshot()
	if final.Balances["A"] != 650 || final.Balances["B"] != 850 {
		t.Fatalf("final balances = %+v, want A=650 B=850", final.Balances)
	}
}

// TestSafeTSIsMinOfWatermarks 验证 T_Safe 受最慢分片水位线约束。
func TestSafeTSIsMinOfWatermarks(t *testing.T) {
	a := New(shard1, shard2)
	seed(t, a)

	if err := a.Heartbeat(shard1, 100); err != nil {
		t.Fatal(err)
	}
	if err := a.Heartbeat(shard2, 30); err != nil {
		t.Fatal(err)
	}
	if got := a.SafeTS(); got != 30 {
		t.Fatalf("SafeTS = %d, want 30 (limited by slowest shard)", got)
	}
	if _, err := a.SnapshotAt(31); err != ErrNotClosed {
		t.Fatalf("SnapshotAt(31) err = %v, want ErrNotClosed", err)
	}
}

// TestOutOfOrderDelivery 验证转入事件先于转出事件到达时仍能正确闭合。
func TestOutOfOrderDelivery(t *testing.T) {
	a := New(shard1, shard2)
	seed(t, a)

	// 转入先到。
	if err := a.Ingest(Event{ShardID: shard2, TxnID: "tx-1", TS: 12, Kind: OpCredit, Account: "B", Amount: 100, Parts: 2}); err != nil {
		t.Fatal(err)
	}
	if err := a.Heartbeat(shard1, 20); err != nil {
		t.Fatal(err)
	}
	if err := a.Heartbeat(shard2, 20); err != nil {
		t.Fatal(err)
	}
	// 未闭合：T_Safe 被挡在 11，B 不得提前入账。
	if got := a.SafeTS(); got != 11 {
		t.Fatalf("SafeTS = %d, want 11", got)
	}
	if a.Snapshot().Balances["B"] != 500 {
		t.Fatal("credit leaked before closure")
	}
	// 转出到达，闭合，commitTS=max(10,12)=12。
	if err := a.Ingest(Event{ShardID: shard1, TxnID: "tx-1", TS: 10, Kind: OpDebit, Account: "A", Amount: 100, Parts: 2}); err != nil {
		t.Fatal(err)
	}
	snap := a.Snapshot()
	if snap.Balances["A"] != 900 || snap.Balances["B"] != 600 {
		t.Fatalf("out-of-order transfer not applied correctly: %+v", snap.Balances)
	}
}

// TestErrors 验证非法输入被拒绝。
func TestErrors(t *testing.T) {
	a := New(shard1, shard2)

	if err := a.Ingest(Event{ShardID: "shard-9", TxnID: "x", TS: 1, Kind: OpDebit, Account: "A", Amount: 1, Parts: 1}); err == nil {
		t.Fatal("expected error for unknown shard")
	}
	if err := a.Heartbeat("shard-9", 1); err == nil {
		t.Fatal("expected error for unknown shard heartbeat")
	}
	ev := Event{ShardID: shard1, TxnID: "tx-1", TS: 1, Kind: OpDebit, Account: "A", Amount: 1, Parts: 2}
	if err := a.Ingest(ev); err != nil {
		t.Fatal(err)
	}
	if err := a.Ingest(ev); err == nil {
		t.Fatal("expected error for duplicate shard event")
	}
}
