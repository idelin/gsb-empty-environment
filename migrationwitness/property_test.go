package migrationwitness

import (
	"math/rand"
	"testing"
)

// TestRandomSmallGraphs builds random small closed manifests with blob
// collisions, shared concat nodes and occasional self references, then asserts
// the budgeted evaluator and the independent brute-force enumerator agree on
// the distinct root identity set.
func TestRandomSmallGraphs(t *testing.T) {
	for seed := int64(0); seed < 60; seed++ {
		rng := rand.New(rand.NewSource(seed))
		env := newTestEnv(t)

		// Three collision groups of legacy refs, each with equal-length blobs.
		groups := []string{"g0", "g1", "g2"}
		blobIndex := 0
		for gi, g := range groups {
			length := int64(gi + 1)
			n := 1 + rng.Intn(3)
			for k := 0; k < n; k++ {
				payload := make([]byte, length)
				for i := range payload {
					payload[i] = byte(seed*131 + int64(blobIndex*7) + int64(i))
				}
				env.blob(nodeName(blobIndex), g, payload)
				blobIndex++
			}
		}

		// Build concat nodes over random edges; occasionally self reference to
		// introduce cyclic candidates alongside legal ones.
		const nodeCount = 6
		var roots []NodeID
		for c := 0; c < nodeCount; c++ {
			name := NodeID("c" + itoa(c))
			edgeCount := 1 + rng.Intn(2)
			edges := make([]Ref, edgeCount)
			var total int64
			for e := range edges {
				g := groups[rng.Intn(len(groups))]
				length := int64(groupsIndex(g) + 1)
				if c == 1 && e == 0 && rng.Intn(2) == 0 {
					// Possible self edge; node ref digest is reused below.
					edges[e] = ref("self"+itoa(c), length)
				} else {
					edges[e] = ref(g, length)
				}
				total += edges[e].Length
			}
			digest := "d" + itoa(c)
			if c == 1 {
				digest = "self" + itoa(c)
			}
			env.concat(string(name), digest, total, edges)
			roots = append(roots, name)
		}
		env.close()

		snap := env.log.Current()
		for _, root := range roots {
			v, err := Evaluate(snap, root, 0)
			if err != nil {
				t.Fatalf("seed %d root %s: %v", seed, root, err)
			}
			ids, _ := bruteEnumerate(snap, root)
			bfCount := len(ids)
			if bfCount > 2 {
				bfCount = 2
			}
			want := StatusNoResolution
			switch len(ids) {
			case 1:
				want = StatusUnique
			default:
				if len(ids) >= 2 {
					want = StatusMultiple
				}
			}
			if v.Status != want || len(v.DistinctIDs) != bfCount {
				t.Fatalf("seed %d root %s mismatch: evaluator=%d(%d ids) brute=%d",
					seed, root, v.Status, len(v.DistinctIDs), len(ids))
			}
			for _, id := range v.DistinctIDs {
				if _, ok := ids[id.String()]; !ok {
					t.Fatalf("seed %d root %s: id %s not in brute force set", seed, root, id)
				}
			}
			for _, cert := range v.Certificates {
				if err := VerifyCertificate(env.log, cert); err != nil {
					t.Fatalf("seed %d root %s cert: %v", seed, root, err)
				}
			}
		}
	}
}

func nodeName(i int) string { return "b" + itoa(i) }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func groupsIndex(g string) int {
	switch g {
	case "g0":
		return 0
	case "g1":
		return 1
	default:
		return 2
	}
}
