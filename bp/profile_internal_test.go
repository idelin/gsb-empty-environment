package bp

import (
	"math/rand"
	"testing"

	"bptree/naive"
)

func TestProfileLCAPhases(t *testing.T) {
	for i := 1; i <= blocksPerSB; i++ {
		want := 0
		for 1<<(want+1) <= i {
			want++
		}
		if blockLog[i] != want {
			t.Fatalf("blockLog[%d]=%d want %d", i, blockLog[i], want)
		}
	}
	rng := rand.New(rand.NewSource(11))
	root := naive.Random(1<<16, rng)
	ix, err := Build(naive.Encode(root))
	if err != nil {
		t.Fatal(err)
	}
	var sel, minr, back, rnk int
	const q = 5000
	for i := 0; i < q; i++ {
		u := rng.Intn(ix.nodes)
		v := rng.Intn(ix.nodes)
		if u == v {
			continue
		}
		acc := 0
		ou := ix.selectAcc(u, &acc)
		ov := ix.selectAcc(v, &acc)
		sel += acc
		if ou > ov {
			ou, ov = ov, ou
		}
		acc = 0
		m := ix.minExcessRange(ou+1, ov+1, &acc)
		minr += acc
		acc = 0
		qq := ix.searchBackward(ou, m-1, &acc)
		back += acc
		acc = 0
		ix.rankAcc(qq, &acc)
		rnk += acc
	}
	t.Logf("select avg %.1f, minRange avg %.1f, backward avg %.1f, rank avg %.1f",
		float64(sel)/q, float64(minr)/q, float64(back)/q, float64(rnk)/q)
}
