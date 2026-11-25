package recov

import "testing"

func TestInclusionAllIndices(t *testing.T) {
	for size := 1; size <= 17; size++ {
		leaves := make([][]byte, size)
		for i := range leaves {
			leaves[i] = []byte{byte(i), byte(i >> 8)}
		}
		root := MTH(leaves)
		for idx := 0; idx < size; idx++ {
			proof := InclusionProof(leaves, uint64(idx))
			if !VerifyInclusion(proof, leaves[idx], root) {
				t.Fatalf("inclusion failed size=%d idx=%d", size, idx)
			}
		}
	}
}

func TestConsistencyProofs(t *testing.T) {
	for n := 1; n <= 20; n++ {
		leaves := make([][]byte, n)
		for i := range leaves {
			leaves[i] = []byte{byte(i)}
		}
		for m := 1; m <= n; m++ {
			proof := ConsistencyProof(leaves, uint64(m))
			if !VerifyConsistency(leaves, uint64(m), proof) {
				t.Fatalf("consistency failed n=%d m=%d", n, m)
			}
		}
	}
}

func TestConsistencyRejectsTamper(t *testing.T) {
	leaves := [][]byte{{1}, {2}, {3}, {4}, {5}}
	proof := ConsistencyProof(leaves, 3)
	wrong := MTH([][]byte{{1}, {2}, {9}})
	tampered := append(append([][]byte{}, proof...), wrong)
	if VerifyConsistency(leaves, 3, tampered) {
		t.Fatal("tampered consistency proof must be rejected")
	}
}
