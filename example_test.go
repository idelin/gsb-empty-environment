package kt_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"

	kt "formal.local/w3-38e8b6c020"
)

// Example shows the minimal honest flow: build a tree, sign an epoch root and
// verify it through a Verifier.
func Example() {
	dirPub, dirPriv, _ := ed25519.GenerateKey(rand.Reader)
	issPub, _, _ := ed25519.GenerateKey(rand.Reader)

	verifier, err := kt.NewVerifier(dirPub, []ed25519.PublicKey{issPub}, 6)
	if err != nil {
		panic(err)
	}

	label, _ := kt.PrefixFromString("010001")
	tree, err := kt.BuildTree(kt.FirstEpoch, 6, []kt.Entry{{
		Label: label,
		Value: kt.LeafValue{PublicKey: []byte("pk-A")},
	}})
	if err != nil {
		panic(err)
	}
	log := &kt.MigrationLog{Epoch: kt.FirstEpoch}
	root, err := kt.SignSignedRoot(kt.EpochRoot{
		Epoch:         kt.FirstEpoch,
		TreeRoot:      tree.RootHash(),
		MigrationHash: log.Hash(),
	}, dirPriv)
	if err != nil {
		panic(err)
	}
	if err := verifier.IngestEpoch(root, log, nil, tree); err != nil {
		panic(err)
	}
	fmt.Println(verifier.Locked())
	// Output: false
}
