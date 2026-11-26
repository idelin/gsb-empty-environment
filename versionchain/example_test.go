package versionchain_test

import (
	"fmt"

	"prescreen.local/ps-c2642cc2ca/versionchain"
)

func ExamplePrune() {
	chain := versionchain.Chain{
		// Newest-first version chain of one key.
		Versions: []versionchain.Version{
			{ID: 3, Txn: 3, TxnSeq: 1, Scopes: []uint64{3}, CommitSeq: 30},
			{ID: 2, Txn: 2, TxnSeq: 1, Scopes: []uint64{2}, CommitSeq: 20, Delete: true},
			{ID: 1, Txn: 1, TxnSeq: 1, Scopes: []uint64{1}, CommitSeq: 10},
		},
		Snapshots: []versionchain.Snapshot{
			{ID: 1, VisibilityUpperBound: 20},
		},
		Slot: &versionchain.ReplicationSlot{RetainAtLeast: 30},
	}

	reclaim := versionchain.Prune(chain)

	fmt.Print("retained:")
	for _, ver := range reclaim.Retained {
		fmt.Printf(" %d", ver.ID)
	}
	fmt.Println()
	fmt.Print("reclaimed:")
	for _, id := range reclaim.Reclaimed {
		fmt.Printf(" %d", id)
	}
	fmt.Println()
	fmt.Printf("predecessor[3]=%d predecessor[2]=%d\n",
		reclaim.Predecessor[3], reclaim.Predecessor[2])

	// Output:
	// retained: 3 2
	// reclaimed: 1
	// predecessor[3]=2 predecessor[2]=0
}
