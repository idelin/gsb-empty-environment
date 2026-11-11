package proofcache_test

import (
	"fmt"

	"example.org/netsec/q0496/proofcache"
)

// Example shows the host lifecycle: build fingerprints, publish a mutually
// recursive fixed point as one batch, then hit the cache and observe a
// dependency change invalidating a dependent proof.
func Example() {
	rule := proofcache.RuleVersion{Name: "taint", Version: "v1.2.0"}
	cache := proofcache.New(rule)

	irF := digest("ir-f")
	irG := digest("ir-g")
	contractF := digest("contract-f")
	contractG := digest("contract-g")

	fFP, err := proofcache.NewFingerprint(irF,
		[]proofcache.DepClaim{{Label: "g", Contract: contractG}}, rule)
	if err != nil {
		panic(err)
	}
	gFP, err := proofcache.NewFingerprint(irG,
		[]proofcache.DepClaim{{Label: "f", Contract: contractF}}, rule)
	if err != nil {
		panic(err)
	}

	batch, err := cache.BeginBatch([]proofcache.Label{"f", "g"})
	if err != nil {
		panic(err)
	}
	if err := batch.Put(proofcache.Draft{
		Label:       "f",
		Fingerprint: fFP,
		Contract:    contractF,
		Proof:       proofcache.Proof("proof-f"),
		Generation:  batch.Generation("f"),
	}); err != nil {
		panic(err)
	}
	if err := batch.Put(proofcache.Draft{
		Label:       "g",
		Fingerprint: gFP,
		Contract:    contractG,
		Proof:       proofcache.Proof("proof-g"),
		Generation:  batch.Generation("g"),
	}); err != nil {
		panic(err)
	}
	if _, err := batch.Commit(); err != nil {
		panic(err)
	}

	if _, err := cache.Lookup("f", fFP); err != nil {
		panic(err)
	}
	fmt.Println("f cached")

	// g's contract changes, so every proof depending on the old contract
	// stops validating until re-analyzed.
	newContractG := digest("contract-g-v2")
	newGFP, err := proofcache.NewFingerprint(digest("ir-g-v2"), nil, rule)
	if err != nil {
		panic(err)
	}
	replacement, err := cache.BeginBatch([]proofcache.Label{"g"})
	if err != nil {
		panic(err)
	}
	if err := replacement.Put(proofcache.Draft{
		Label:       "g",
		Fingerprint: newGFP,
		Contract:    newContractG,
		Proof:       proofcache.Proof("proof-g-v2"),
		Generation:  replacement.Generation("g"),
	}); err != nil {
		panic(err)
	}
	if _, err := replacement.Commit(); err != nil {
		panic(err)
	}

	if _, err := cache.Lookup("f", fFP); err != nil {
		fmt.Println("f invalidated")
	}

	// Output:
	// f cached
	// f invalidated
}

func digest(tag string) proofcache.Digest {
	h := proofcache.NewHasher()
	h.WriteString("example", tag)
	return h.Sum()
}
