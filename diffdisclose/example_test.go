package diffdisclose_test

import (
	"crypto/rand"
	"fmt"

	"prescreen.local/ddd-10491-v2/diffdisclose"
)

// ExampleBuildProof 展示构造、编码、解码并验证一份差分披露证明的完整流程。
func ExampleBuildProof() {
	signer, err := diffdisclose.NewEd25519Signer()
	if err != nil {
		panic(err)
	}
	const height = 6
	sharedSalt := randomSalt()

	oldTree, err := diffdisclose.NewTree(height, []diffdisclose.Leaf{
		{Key: 2, Value: []byte("alpha"), Salt: sharedSalt},
		{Key: 5, Value: []byte("beta"), Salt: randomSalt()},
	})
	if err != nil {
		panic(err)
	}
	newTree, err := diffdisclose.NewTree(height, []diffdisclose.Leaf{
		{Key: 2, Value: []byte("alpha"), Salt: sharedSalt},
		{Key: 6, Value: []byte("gamma"), Salt: randomSalt()},
	})
	if err != nil {
		panic(err)
	}

	old, err := diffdisclose.SignManifest(signer, "tenant", 1, diffdisclose.Digest{}, oldTree.Root())
	if err != nil {
		panic(err)
	}
	next, err := diffdisclose.SignManifest(signer, "tenant", 2, diffdisclose.ManifestDigest(old), newTree.Root())
	if err != nil {
		panic(err)
	}

	authorized, err := diffdisclose.NewIntervalSet([]diffdisclose.Interval{{Lo: 0, Hi: 64}})
	if err != nil {
		panic(err)
	}
	proof, _, err := diffdisclose.BuildProof(diffdisclose.BuildRequest{
		OldTree:    oldTree,
		NewTree:    newTree,
		Old:        old,
		New:        next,
		Authorized: authorized,
	})
	if err != nil {
		panic(err)
	}

	encoded := proof.Encode()
	decoded, err := diffdisclose.DecodeProof(encoded)
	if err != nil {
		panic(err)
	}
	result, err := decoded.Verify(diffdisclose.VerifyOptions{Verifier: diffdisclose.Ed25519Verifier{}})
	if err != nil {
		panic(err)
	}

	for _, event := range result.Events {
		fmt.Printf("key %d: %s\n", event.Key, eventName(event.Kind))
	}
	// Unordered output:
	// key 5: removed
	// key 6: added
}

func eventName(k diffdisclose.EventKind) string {
	switch k {
	case diffdisclose.EventAdded:
		return "added"
	case diffdisclose.EventRemoved:
		return "removed"
	case diffdisclose.EventChanged:
		return "changed"
	default:
		return "unknown"
	}
}

func randomSalt() []byte {
	salt := make([]byte, diffdisclose.SaltSize)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	return salt
}
