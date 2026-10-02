// Command demo runs the acceptance scenario end to end: an account in
// shard 2 transfers funds, the system emits an aggregate cross-shard
// proof, and an independent external verifier checks it against the
// published meta root.
package main

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"

	"shardmerkle/merkle"
)

func balance(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}

func amount(b []byte) uint64 { return binary.BigEndian.Uint64(b) }

func short(d merkle.Digest) string { return hex.EncodeToString(d[:])[:16] + "..." }

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func main() {
	fmt.Println("=== Sharded Merkle Tree: cross-shard proof demo ===")

	// 1. Bootstrap: 4 independent shard trees + main aggregator.
	sys := merkle.NewSystem(merkle.DefaultShardCount)
	for shard := 0; shard < merkle.DefaultShardCount; shard++ {
		for i := 0; i < 6; i++ {
			key := []byte(fmt.Sprintf("shard%d/account%03d", shard, i))
			must(sys.Update(shard, key, balance(uint64(1000+shard*100+i))))
		}
	}
	fmt.Printf("bootstrapped %d shards, meta root = %s\n", merkle.DefaultShardCount, short(sys.MetaRoot()))

	// 2. Transfer inside shard 2: account003 -> account004, 125 units.
	sender := []byte("shard2/account003")
	receiver := []byte("shard2/account004")
	shard2, err := sys.Shard(2)
	must(err)
	senderBal, _ := shard2.Get(sender)
	receiverBal, _ := shard2.Get(receiver)
	fmt.Printf("\ntransfer in shard 2: %s (%d) -> %s (%d), amount 125\n",
		sender, amount(senderBal), receiver, amount(receiverBal))

	must(sys.Update(2, sender, balance(amount(senderBal)-125)))
	must(sys.Update(2, receiver, balance(amount(receiverBal)+125)))

	shard2Root, err := sys.ShardRoot(2)
	must(err)
	metaRoot := sys.MetaRoot()
	fmt.Printf("shard 2 local root = %s\n", short(shard2Root))
	fmt.Printf("meta root          = %s\n", short(metaRoot))

	// 3. Shard 2 emits the inclusion proof; the aggregator attaches
	//    shard 2's position proof in the meta tree.
	proof, err := sys.ProveCrossShard(2, sender)
	must(err)
	fmt.Printf("\naggregate proof for %s:\n", sender)
	fmt.Printf("  shard-local path : %d step(s)\n", len(proof.ShardPath))
	fmt.Printf("  meta-tree path   : %d step(s)\n", len(proof.MetaPath))
	fmt.Printf("  proven balance   : %d\n", amount(proof.Value))

	// 4. Independent external verifier: knows only the meta root.
	if !merkle.VerifyCrossShardProof(metaRoot, proof) {
		fmt.Println("\nEXTERNAL VERIFY: FAIL")
		os.Exit(1)
	}
	fmt.Println("\nEXTERNAL VERIFY: PASS (proof checked against meta root only)")

	// 5. Sanity: a tampered proof must be rejected.
	forged := *proof
	forged.Value = balance(999999)
	if merkle.VerifyCrossShardProof(metaRoot, &forged) {
		fmt.Println("tampered proof accepted: FAIL")
		os.Exit(1)
	}
	fmt.Println("tampered proof rejected: PASS")
}
