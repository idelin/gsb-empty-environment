// Demo of the acceptance scenario: a transfer inside shard 2, proven
// end-to-end to an external verifier via a combined cross-shard proof.
package main

import (
	"encoding/hex"
	"fmt"

	sharded "shardedmerkle"
)

func short(b []byte) string {
	if len(b) == 0 {
		return "<empty>"
	}
	return hex.EncodeToString(b)[:16] + "..."
}

func main() {
	agg := sharded.NewAggregator()

	// Seed accounts across the 4 shards.
	agg.Shard(0).Set("alice", 100)
	agg.Shard(1).Set("carol", 200)
	agg.Shard(2).Set("dave", 500)
	agg.Shard(2).Set("erin", 300)
	agg.Shard(3).Set("grace", 1000)
	agg.Refresh()

	fmt.Println("=== Initial state ===")
	for i := 0; i < sharded.NumShards; i++ {
		fmt.Printf("shard %d local root: %s\n", i, short(agg.Shard(i).Root()))
	}
	fmt.Printf("meta root:           %s\n\n", short(agg.MetaRoot()))

	// Acceptance scenario: account in shard 2 initiates a transfer.
	fmt.Println("=== Transfer: dave --120--> erin (inside shard 2) ===")
	if err := agg.Shard(2).Transfer("dave", "erin", 120); err != nil {
		panic(err)
	}
	agg.Refresh()
	dave, _ := agg.Shard(2).Balance("dave")
	erin, _ := agg.Shard(2).Balance("erin")
	fmt.Printf("dave=%d erin=%d\n", dave, erin)
	fmt.Printf("shard 2 local root:  %s\n", short(agg.Shard(2).Root()))
	fmt.Printf("meta root:           %s\n\n", short(agg.MetaRoot()))

	// Shard 2 produces the internal inclusion proof; the master aggregator
	// attaches shard 2's position proof inside the Meta-Tree.
	cp, err := agg.ProveCrossShard(2, "dave")
	if err != nil {
		panic(err)
	}
	fmt.Println("=== Combined cross-shard proof for dave@shard2 ===")
	fmt.Printf("shard-local proof steps: %d\n", len(cp.ShardProof.Steps))
	fmt.Printf("meta-tree proof steps:   %d (shard index %d of %d)\n\n",
		len(cp.MetaProof.Steps), cp.MetaProof.Index, cp.MetaProof.Size)

	// Any external verifier checks it with only the meta root.
	metaRoot := agg.MetaRoot()
	ok := sharded.VerifyCombinedProof(metaRoot, cp)
	fmt.Printf("external verification against meta root: %v\n", ok)

	// Negative control: a forged balance must fail.
	forged := *cp
	forged.Balance = 999999
	fmt.Printf("forged balance verification:             %v (want false)\n",
		sharded.VerifyCombinedProof(metaRoot, &forged))
}
