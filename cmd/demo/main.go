// Demo of the erasure package: encode a message into 4+2 shards,
// lose two shards, corrupt nothing, and recover. Then show rejection
// of duplicates, corrupt shards, and insufficient shards.
package main

import (
	"fmt"

	"erasure"
)

func main() {
	msg := []byte("GF(256) systematic erasure coding: any 4 of 6 shards recover this message.")
	k, m := 4, 2

	shards, err := erasure.Encode(msg, k, m)
	if err != nil {
		panic(err)
	}
	fmt.Printf("encoded %d bytes into %d data + %d parity shards (%d bytes each)\n",
		len(msg), k, m, len(shards[0])-erasure.HeaderSize)
	for i, s := range shards {
		fmt.Printf("  shard %d: %d bytes, head=% x\n", i, len(s), s[:8])
	}

	// Lose shard 1 and shard 4 (one data, one parity): exactly k remain.
	keep := [][]byte{shards[0], shards[2], shards[3], shards[5]}
	got, err := erasure.Decode(keep, 1<<20)
	if err != nil {
		panic(err)
	}
	fmt.Printf("recovered from shards {0,2,3,5}: %q\n", string(got))

	// Duplicate shard ID is rejected.
	if _, err := erasure.Decode([][]byte{shards[0], shards[0], shards[2], shards[3]}, -1); err != nil {
		fmt.Printf("duplicate shard rejected: %v\n", err)
	}

	// A corrupted shard is excluded and never used; recovery still works.
	bad := append([]byte(nil), shards[1]...)
	bad[erasure.HeaderSize+3] ^= 0xFF
	got, err = erasure.Decode([][]byte{shards[0], bad, shards[2], shards[3], shards[4]}, -1)
	if err != nil {
		panic(err)
	}
	fmt.Printf("corrupt shard excluded, recovered: %q\n", string(got))

	// Beyond tolerance: only k-1 valid shards -> insufficient.
	if _, err := erasure.Decode([][]byte{shards[0], shards[2], shards[3]}, -1); err != nil {
		fmt.Printf("only %d shards: %v\n", k-1, err)
	}

	// Output budget is enforced.
	if _, err := erasure.Decode(shards, 10); err != nil {
		fmt.Printf("output limit enforced: %v\n", err)
	}
}
