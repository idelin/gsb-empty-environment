// Command demo builds a small block package and exercises the streaming
// verifier, range proofs, the version-isolated cache, and tamper detection.
package main

import (
	"fmt"

	"blockpack"
)

func main() {
	objs := []blockpack.Object{
		{Type: "meta", Data: []byte("root object"), Refs: []uint32{1, 2}},
		{Type: "blob", Data: []byte("hello"), Refs: []uint32{3}},
		{Type: "blob", Data: []byte("world")},
		{Type: "tree", Data: []byte("some tree payload"), Refs: []uint32{0}},
		{Type: "blob", Data: []byte("tail"), Refs: []uint32{2}},
	}

	pkg, err := blockpack.Build(1, 2, objs)
	if err != nil {
		panic(err)
	}
	fmt.Printf("built package: version=%d objects=%d blocks=%d\n",
		pkg.Header.Version, pkg.Header.ObjectCount, pkg.Header.BlockCount)
	fmt.Printf("root digest: %x\n", pkg.Header.Root[:16])

	// Serialize and parse back (strict length checks, no host code run).
	wire := blockpack.Serialize(pkg)
	parsed, err := blockpack.Parse(wire)
	if err != nil {
		panic(err)
	}
	fmt.Printf("wire format: %d bytes, parsed back OK\n", len(wire))

	// Streaming verification: blocks are verified as they are read; only
	// Finish confirms completeness of the whole package.
	v := blockpack.NewVerifier(parsed.Header, parsed.Header.Root)
	for _, b := range parsed.Blocks {
		if err := v.AddBlock(b); err != nil {
			panic(err)
		}
		fmt.Printf("block %d locally verified (complete=%v)\n", b.Index, v.Complete())
	}
	if err := v.Finish(); err != nil {
		panic(err)
	}
	fmt.Printf("finish: package complete=%v\n", v.Complete())

	// Range read with an integrity proof.
	proof, err := parsed.ProveRange(1, 3)
	if err != nil {
		panic(err)
	}
	fmt.Printf("range proof blocks [%d,%d): verify=%v\n",
		proof.Start, proof.End, proof.Verify(parsed.Header.Root) == nil)

	// Deleting the last block must fail even though all earlier blocks pass.
	v2 := blockpack.NewVerifier(parsed.Header, parsed.Header.Root)
	for _, b := range parsed.Blocks[:len(parsed.Blocks)-1] {
		if err := v2.AddBlock(b); err != nil {
			panic(err)
		}
	}
	fmt.Printf("last block deleted: finish err=%v\n", v2.Finish())

	// Reordered blocks are rejected at read time.
	v3 := blockpack.NewVerifier(parsed.Header, parsed.Header.Root)
	fmt.Printf("block swap: add err=%v\n", v3.AddBlock(parsed.Blocks[1]))

	// Version-isolated cache: entries verified under v1 miss under v2.
	cache := blockpack.NewCache()
	for _, b := range parsed.Blocks {
		cache.Store(1, b.Index, b.Digest)
	}
	objs[4].Data = []byte("tail v2")
	pkgV2, err := blockpack.Build(2, 2, objs)
	if err != nil {
		panic(err)
	}
	fmt.Printf("cache: v1 block hit=%v, v2 block hit=%v (must re-verify)\n",
		cache.Lookup(1, 0, parsed.Blocks[0].Digest),
		cache.Lookup(2, 0, pkgV2.Blocks[0].Digest))

	// Old proof cannot be spliced into the new package.
	oldProof, err := parsed.ProveRange(0, 2)
	if err != nil {
		panic(err)
	}
	fmt.Printf("old proof spliced into v2: verify=%v\n",
		oldProof.Verify(pkgV2.Header.Root) == nil)

	// Independent verifier over a subset, matched against a tamper manifest.
	tampered := append([]blockpack.Block(nil), parsed.Blocks...)
	tampered[1].Objects[1].Data = []byte("forged")
	failed := blockpack.VerifySubset(parsed.Header.Root, tampered)
	fmt.Printf("tamper check: failed blocks=%v, matches manifest=%v\n",
		failed, blockpack.MatchTamperManifest(failed, []int{1}))
}
