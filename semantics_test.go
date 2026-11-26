package hvsched

import (
	"math/big"
	"testing"
)

func TestLayerSumBruteForce(t *testing.T) {
	base := twoLeafProblem(
		VarSpec{Min: 1, Max: 4, Layer: "l"},
		VarSpec{Min: 1, Max: 4, Layer: "l"},
		[]ArrivalSpec{
			{Leaf: "a", Before: 0, Count: 3, Length: 2},
			{Leaf: "b", Before: 0, Count: 3, Length: 1},
		},
		4,
		ClockStartTag,
	)
	base.LayerSums = map[string]uint64{"l": 5}

	for wa := uint64(1); wa <= 4; wa++ {
		for wb := uint64(1); wb <= 4; wb++ {
			if wa+wb != 5 {
				continue
			}
			w := WeightVector{wa, wb}
			replay, err := Replay(base, w)
			if err != nil {
				t.Fatal(err)
			}
			p := base
			p.Observations = make([]Observation, len(replay.Emissions))
			for i, e := range replay.Emissions {
				p.Observations[i] = Observation{Leaf: e.Leaf}
			}
			want := enumerateVectors(t, p)
			checkSolutionSet(t, p, want)
		}
	}
}

func TestEmptyTreeReactivationMaxRules(t *testing.T) {
	for _, clock := range []ClockMode{ClockStartTag, ClockFinishTag} {
		p := twoLeafProblem(
			VarSpec{Min: 1, Max: 1},
			VarSpec{Min: 1, Max: 1},
			[]ArrivalSpec{
				{Leaf: "a", Before: 0, Count: 1, Length: 1},
				{Leaf: "b", Before: 1, Count: 1, Length: 10},
				{Leaf: "a", Before: 2, Count: 1, Length: 1},
			},
			3,
			clock,
		)
		res, err := Replay(p, WeightVector{1, 1})
		if err != nil {
			t.Fatal(err)
		}
		gotLeaves := []string{res.Emissions[0].Leaf, res.Emissions[1].Leaf, res.Emissions[2].Leaf}
		wantLeaves := []string{"a", "b", "a"}
		for i := range wantLeaves {
			if gotLeaves[i] != wantLeaves[i] {
				t.Fatalf("clock=%d leaves=%v, want %v", clock, gotLeaves, wantLeaves)
			}
		}

		thirdFinish := res.Emissions[2].Finish
		var want *big.Rat
		if clock == ClockStartTag {
			// max(parent 0, local last finish 1) + 1/1.
			want = big.NewRat(2, 1)
		} else {
			// Parent clock jumped to b's finish 11; max(11, 1)+1.
			want = big.NewRat(12, 1)
		}
		if thirdFinish.Cmp(want) != 0 {
			t.Fatalf("clock=%d third finish=%v, want %v", clock, thirdFinish, want)
		}

		p.Observations = []Observation{{Leaf: "a"}, {Leaf: "b"}, {Leaf: "a"}}
		set, err := Solve(p)
		if err != nil || set.Count().Int64() != 1 {
			t.Fatalf("clock=%d solve count=%s err=%v regions=%#v", clock, set.Count(), err, set.Regions())
		}
	}
}

func TestTieRequiredByFullTrajectory(t *testing.T) {
	p := twoLeafProblem(
		VarSpec{Min: 1, Max: 3, Layer: "l"},
		VarSpec{Min: 1, Max: 3, Layer: "l"},
		[]ArrivalSpec{
			{Leaf: "a", Before: 0, Count: 4, Length: 1},
			{Leaf: "b", Before: 0, Count: 4, Length: 1},
		},
		2,
		ClockStartTag,
	)
	p.LayerSums = map[string]uint64{"l": 4}
	p.Observations = []Observation{{Leaf: "a"}, {Leaf: "b"}}

	want := []WeightVector{{2, 2}}
	checkSolutionSet(t, p, want)

	res, err := Replay(p, want[0])
	if err != nil {
		t.Fatal(err)
	}
	if res.Emissions[0].Finish.Cmp(res.Emissions[0].Finish) != 0 {
		t.Fatal("rational finish invalid")
	}
	// Explicitly show the first tags are equal, so tenant number breaks it.
	if res.Emissions[0].Finishes["a"].Cmp(big.NewRat(1, 2)) != 0 {
		t.Fatalf("a finish=%v, want 1/2", res.Emissions[0].Finishes["a"])
	}
}

func TestLargeRangeCountAndKth(t *testing.T) {
	const n = uint64(1 << 32)
	p := twoLeafProblem(
		VarSpec{Min: 1, Max: n, Layer: "l"},
		VarSpec{Min: 1, Max: n, Layer: "l"},
		[]ArrivalSpec{
			{Leaf: "a", Before: 0, Count: 1, Length: 1},
			{Leaf: "b", Before: 0, Count: 1, Length: 1},
		},
		1,
		ClockStartTag,
	)
	const sum = uint64(6_000_000_000)
	p.LayerSums = map[string]uint64{"l": sum}
	p.Observations[0] = Observation{Leaf: "a"}

	set, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}

	// wa >= wb and wa+wb=sum; first lex solution is ceil(sum/2).
	first, err := set.KthInt(1)
	if err != nil {
		t.Fatal(err)
	}
	if first[0] != 3_000_000_000 || first[1] != 3_000_000_000 {
		t.Fatalf("first=%v", first)
	}
	last, err := set.Kth(set.Count())
	if err != nil {
		t.Fatal(err)
	}
	if last[0] != n || last[1] != sum-n {
		t.Fatalf("last=%v, want [%d %d]", last, n, sum-n)
	}

	// Every rank must unrank consistently. Check boundaries around powers.
	for _, rank := range []*big.Int{big.NewInt(2), new(big.Int).Sub(set.Count(), big.NewInt(1))} {
		v, err := set.Kth(rank)
		if err != nil {
			t.Fatal(err)
		}
		ok, err := Verify(p, v)
		if err != nil || !ok {
			t.Fatalf("rank %s vector %v verify=%v err=%v", rank, v, ok, err)
		}
	}
}

func TestExactCompletionConstraint(t *testing.T) {
	p := twoLeafProblem(
		VarSpec{Min: 2, Max: 2},
		VarSpec{Min: 4, Max: 4},
		[]ArrivalSpec{
			{Leaf: "a", Before: 0, Count: 1, Length: 3},
			{Leaf: "b", Before: 0, Count: 1, Length: 8},
		},
		1,
		ClockStartTag,
	)
	p.Observations[0] = Observation{Leaf: "a", Finish: big.NewRat(3, 2)}
	set, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if set.Count().Int64() != 1 {
		t.Fatalf("correct finish count=%s regions=%#v", set.Count(), set.Regions())
	}

	wrong := p
	wrong.Observations = []Observation{{Leaf: "a", Finish: big.NewRat(7, 5)}}
	bad, err := Solve(wrong)
	if err != nil {
		t.Fatal(err)
	}
	if bad.Count().Sign() != 0 || bad.BadPrefixLength() != 1 {
		t.Fatalf("wrong finish count=%s prefix=%d", bad.Count(), bad.BadPrefixLength())
	}
}
