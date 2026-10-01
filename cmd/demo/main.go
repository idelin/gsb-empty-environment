// 运行样例：模拟分库1 与 分库2 之间的转账业务，
// 展示对齐器在时间戳未闭合前阻止查询视图，闭合后才输出一致性快照。
package main

import (
	"fmt"
	"os"

	"globalreadview/aligner"
)

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func show(a *aligner.Aligner, note string) {
	snap := a.Snapshot()
	fmt.Printf("  [T_Safe=%3d] %-22s 余额: A=%-4d B=%-4d 总额=%d\n",
		a.SafeTS(), note, snap.Balances["A"], snap.Balances["B"], snap.Total())
}

func tryQuery(a *aligner.Aligner, ts uint64) {
	snap, err := a.SnapshotAt(ts)
	if err == aligner.ErrNotClosed {
		fmt.Printf("  查询 ts=%d 的视图 => 被阻止（%v）\n", ts, err)
		return
	}
	fmt.Printf("  查询 ts=%d 的视图 => A=%d B=%d 总额=%d\n", ts, snap.Balances["A"], snap.Balances["B"], snap.Total())
}

func main() {
	a := aligner.New("shard-1", "shard-2")

	fmt.Println("== 1. 开户充值（各分片独立事务） ==")
	must(a.Ingest(aligner.Event{ShardID: "shard-1", TxnID: "seed-a", TS: 1, Kind: aligner.OpCredit, Account: "A", Amount: 1000, Parts: 1}))
	must(a.Ingest(aligner.Event{ShardID: "shard-2", TxnID: "seed-b", TS: 1, Kind: aligner.OpCredit, Account: "B", Amount: 500, Parts: 1}))
	must(a.Heartbeat("shard-1", 5))
	must(a.Heartbeat("shard-2", 5))
	show(a, "充值完成")

	fmt.Println("\n== 2. 跨库转账：A(分库1) 转 100 给 B(分库2) ==")
	fmt.Println("-- 分库1 转出事件先到（A -100 @ ts=10），分库2 转入事件尚未到达 --")
	must(a.Ingest(aligner.Event{ShardID: "shard-1", TxnID: "tx-1", TS: 10, Kind: aligner.OpDebit, Account: "A", Amount: 100, Parts: 2}))
	must(a.Heartbeat("shard-1", 15))
	must(a.Heartbeat("shard-2", 15))
	show(a, "转出未闭合，快照不可见")
	tryQuery(a, 10)
	tryQuery(a, 15)

	fmt.Println("\n-- 分库2 转入事件到达（B +100 @ ts=12），事务因果闭合 --")
	must(a.Ingest(aligner.Event{ShardID: "shard-2", TxnID: "tx-1", TS: 12, Kind: aligner.OpCredit, Account: "B", Amount: 100, Parts: 2}))
	show(a, "闭合后快照生效")
	tryQuery(a, 15)

	fmt.Println("\n== 3. 资金守恒校验 ==")
	snap := a.Snapshot()
	if snap.Total() != 1500 {
		fmt.Fprintf(os.Stderr, "FAIL: 总额 %d != 1500，钱凭空消失！\n", snap.Total())
		os.Exit(1)
	}
	fmt.Printf("  总额 = %d，与初始一致，无任何中间态快照出现半笔转账。\n", snap.Total())
}
