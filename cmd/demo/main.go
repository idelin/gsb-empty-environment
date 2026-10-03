// Demo: two shards, one cross-shard transfer. The aligner blocks external
// query views until both the debit and the credit leg have arrived.
package main

import (
	"fmt"

	"github.com/example/globalreadview/aligner"
)

func report(a *aligner.Aligner, label string) {
	snap, err := a.Snapshot()
	if err != nil {
		fmt.Printf("[%s] view BLOCKED: %v\n", label, err)
		return
	}
	fmt.Printf("[%s] consistent view @ T_Safe=%d, balanced=%v\n", label, snap.TS, snap.Balanced())
	for _, e := range snap.Events {
		fmt.Printf("    shard=%s txn=%s lts=%d %s %s %d\n",
			e.ShardID, e.TxnID, e.LocalTS, e.Kind, e.Account, e.Amount)
	}
}

func main() {
	a := aligner.NewAligner("shard1", "shard2")
	a.RegisterTxn("transfer-42", "shard1", "shard2")

	fmt.Println("== shard1 commits the debit leg (alice -100 @ lts 10) ==")
	must(a.Ingest(aligner.Event{ShardID: "shard1", TxnID: "transfer-42",
		LocalTS: 10, Kind: aligner.Debit, Account: "alice", Amount: 100}))
	report(a, "debit only, shard2 silent")

	fmt.Println("== shard2 heartbeats on its own clock, credit still in flight ==")
	must(a.Heartbeat("shard2", 20))
	report(a, "shard2 alive, txn open")

	fmt.Println("== shard2 commits the credit leg (bob +100 @ lts 25) ==")
	must(a.Ingest(aligner.Event{ShardID: "shard2", TxnID: "transfer-42",
		LocalTS: 25, Kind: aligner.Credit, Account: "bob", Amount: 100}))
	report(a, "both legs arrived")

	fmt.Println("== done: money never vanished ==")
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
