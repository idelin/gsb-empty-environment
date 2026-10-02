package ring

import (
	"math"
	"testing"

	"hashring-analyzer/hashfn"
)

func testNodes(n int) []string {
	nodes := make([]string, n)
	for i := range nodes {
		nodes[i] = "node-" + string(rune('a'+i))
	}
	return nodes
}

func TestMapIsDeterministic(t *testing.T) {
	r := New(hashfn.MD5Hash, testNodes(5), 100, 64)
	key := []byte("some-key")
	first := r.Map(key)
	for i := 0; i < 100; i++ {
		if got := r.Map(key); got != first {
			t.Fatalf("Map not deterministic: %d vs %d", got, first)
		}
	}
}

func TestArcFractionsSumToOne(t *testing.T) {
	for _, w := range []uint{12, 16, 32, 64} {
		r := New(hashfn.Murmur3Hash, testNodes(7), 50, w)
		sum := 0.0
		for _, f := range r.ArcFractions() {
			sum += f
		}
		if math.Abs(sum-1) > 1e-9 {
			t.Errorf("width %d: arc fractions sum to %f, want 1", w, sum)
		}
	}
}

func TestSingleNodeOwnsEverything(t *testing.T) {
	r := New(hashfn.FNV1aHash, []string{"only"}, 10, 16)
	for i := 0; i < 1000; i++ {
		if got := r.Map([]byte{byte(i), byte(i >> 8)}); got != 0 {
			t.Fatalf("single-node ring mapped to node %d", got)
		}
	}
	fracs := r.ArcFractions()
	if fracs[0] != 1 {
		t.Fatalf("single node arc fraction = %f, want 1", fracs[0])
	}
}

func TestBitWidthTruncatesPositions(t *testing.T) {
	r := New(hashfn.FNV1aHash, testNodes(4), 200, 12)
	for i := 0; i < 10000; i++ {
		got := r.Map([]byte{byte(i), byte(i >> 8), byte(i >> 16)})
		if got < 0 || got >= 4 {
			t.Fatalf("Map returned out-of-range node %d", got)
		}
	}
}

func TestDistributionIsRoughlyBalanced(t *testing.T) {
	nodes := testNodes(8)
	r := New(hashfn.MD5Hash, nodes, 128, 64)
	counts := make([]int, len(nodes))
	const total = 200000
	for i := 0; i < total; i++ {
		var key [8]byte
		for j := range key {
			key[j] = byte(i >> (uint(j) * 8))
		}
		counts[r.Map(key[:])]++
	}
	min, max := counts[0], counts[0]
	for _, c := range counts {
		if c < min {
			min = c
		}
		if c > max {
			max = c
		}
	}
	if ratio := float64(max) / float64(min); ratio > 2.0 {
		t.Errorf("max/min load ratio = %.2f, want <= 2.0 (min=%d max=%d)", ratio, min, max)
	}
}

func TestInvalidConfigPanics(t *testing.T) {
	cases := []func(){
		func() { New(hashfn.MD5Hash, testNodes(2), 10, 0) },
		func() { New(hashfn.MD5Hash, testNodes(2), 10, 65) },
		func() { New(hashfn.MD5Hash, nil, 10, 32) },
		func() { New(hashfn.MD5Hash, testNodes(2), 0, 32) },
	}
	for i, fn := range cases {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("case %d: expected panic", i)
				}
			}()
			fn()
		}()
	}
}
