// Command demo shows encode -> lose/corrupt blocks -> decode.
package main

import (
	"fmt"

	"erasure"
)

func main() {
	message := []byte("The quick brown fox jumps over the lazy dog. 0123456789!")
	k, m := 4, 2

	blocks, err := erasure.Encode(message, k, m)
	if err != nil {
		panic(err)
	}
	fmt.Printf("encoded %d bytes into %d data + %d parity blocks\n", len(message), k, m)
	for _, b := range blocks {
		fmt.Printf("  block id=%d len=%d\n", b.ID, len(b.Data))
	}

	raw := make([][]byte, len(blocks))
	for i, b := range blocks {
		raw[i] = b.Marshal()
	}

	// Lose blocks 1 and 4 (m=2 losses, exactly at tolerance).
	survivors := [][]byte{raw[0], raw[2], raw[3], raw[5]}
	got, err := erasure.Decode(survivors, 1<<20)
	fmt.Printf("\nlost blocks 1,4 -> decode err=%v\n", err)
	fmt.Printf("recovered: %q\n", got)

	// Corrupt block 0 (bit flip) and lose block 5; still 4 valid blocks.
	corrupt := make([]byte, len(raw[0]))
	copy(corrupt, raw[0])
	corrupt[erasure.HeaderSize] ^= 0x5a
	mixed := [][]byte{corrupt, raw[1], raw[2], raw[3], raw[4]}
	got, err = erasure.Decode(mixed, 1<<20)
	fmt.Printf("\ncorrupted block 0, lost block 5 -> decode err=%v\n", err)
	fmt.Printf("recovered: %q\n", got)

	// Lose m+1 = 3 blocks: beyond tolerance.
	got, err = erasure.Decode([][]byte{raw[0], raw[1], raw[2]}, 1<<20)
	fmt.Printf("\nlost 3 blocks (beyond tolerance) -> decode err=%v, output=%v\n", err, got)

	// Duplicate block id is rejected.
	_, err = erasure.Decode([][]byte{raw[0], raw[0], raw[1], raw[2]}, 1<<20)
	fmt.Printf("duplicate block id -> decode err=%v\n", err)

	// Output limit smaller than the original length is enforced.
	_, err = erasure.Decode(raw, len(message)-1)
	fmt.Printf("output limit %d -> decode err=%v\n", len(message)-1, err)
}
