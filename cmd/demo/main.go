// Command demo walks through the full compliance-erasure lifecycle:
// records are committed to a Merkle tree, a customer exercises the right
// to be forgotten, and every remaining customer's proof still verifies.
package main

import (
	"bytes"
	"fmt"

	"erasureproof"
)

func short(d erasureproof.Digest) string { return fmt.Sprintf("%x…", d[:8]) }

func main() {
	prover, err := erasureproof.NewProverFromSeed(bytes.Repeat([]byte{0x42}, 32))
	must(err)

	// 1. Onboard customers: personal data is committed as tree leaves.
	customers := map[string][]byte{
		"cust-alice": []byte(`{"name":"Alice","id_no":"A-1001","phone":"138-0000-0001"}`),
		"cust-bob":   []byte(`{"name":"Bob","id_no":"B-1002","phone":"138-0000-0002"}`),
		"cust-carol": []byte(`{"name":"Carol","id_no":"C-1003","phone":"138-0000-0003"}`),
		"cust-dave":  []byte(`{"name":"Dave","id_no":"D-1004","phone":"138-0000-0004"}`),
	}
	for _, id := range []string{"cust-alice", "cust-bob", "cust-carol", "cust-dave"} {
		must(prover.Add(id, customers[id]))
	}
	root1 := prover.Root()
	fmt.Printf("[1] 4 customers committed, root = %s\n", short(root1))

	// 2. Routine audit: every customer's inclusion proof verifies.
	for id := range customers {
		ip, err := prover.Prove(id)
		must(err)
		fmt.Printf("[2] inclusion proof %-11s valid=%v\n", id, erasureproof.VerifyInclusion(root1, ip))
	}

	// 3. Alice requests account deletion (right to be forgotten).
	tr, err := prover.Erase("cust-alice")
	must(err)
	fmt.Printf("[3] cust-alice erased: root %s -> %s (epoch %d)\n",
		short(tr.OldRoot), short(tr.NewRoot), tr.Epoch)

	// 4. The original personal data is physically wiped.
	_, err = prover.Record("cust-alice")
	fmt.Printf("[4] alice's data after erasure: %v; original buffer wiped=%v\n",
		err, bytes.Equal(customers["cust-alice"], make([]byte, len(customers["cust-alice"]))))

	// 5. The erasure itself is publicly verifiable (Erase_Marker + signature).
	ep, err := prover.ErasureProof("cust-alice")
	must(err)
	fmt.Printf("[5] erasure proof for cust-alice valid=%v\n",
		erasureproof.VerifyErasure(prover.PublicKey(), prover.Root(), ep))

	// 6. The root transition is auditable: exactly one leaf was replaced.
	fmt.Printf("[6] root transition (old root -> new root) valid=%v\n",
		erasureproof.VerifyTransition(prover.PublicKey(), tr))

	// 7. Acceptance boundary: every remaining customer's proof still
	//    verifies against the evolved root.
	root2 := prover.Root()
	for _, id := range []string{"cust-bob", "cust-carol", "cust-dave"} {
		ip, err := prover.Prove(id)
		must(err)
		fmt.Printf("[7] post-erasure proof %-11s valid=%v\n", id, erasureproof.VerifyInclusion(root2, ip))
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
