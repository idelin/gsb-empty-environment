package acceptance_test

import (
	"math"
	"testing"

	crush "prescreen.local/ps-7261f1735c"
)

func TestReweightedDistributionWithinTolerance(t *testing.T) {
	root := crush.NewNode(0, "root", 8)
	reweights := []float64{1, 1, 0.5, 0.5, 0.25, 0.25, 1, 1}
	for i := range reweights {
		osd := crush.NewOSD(20+i, 1)
		osd.Reweight = reweights[i]
		root.Children = append(root.Children, osd)
	}
	rule := crush.NewRule("rw", crush.TakeStep(0), crush.ChooseStep(crush.FirstN, 1, "osd", 16), crush.EmitStep())
	mapper := crush.NewMapper(root, crush.SplitMix64, rule)

	const n = 300000
	counts := make(map[int]int, len(reweights))
	total := 0.0
	for _, rw := range reweights {
		total += rw
	}
	for pg := uint64(0); pg < n; pg++ {
		result, err := mapper.MapPG(pg, 1, "rw")
		if err != nil || result.HasHole() {
			t.Fatalf("pg %d: result=%v err=%v", pg, result, err)
		}
		counts[result.OSDs[0]]++
	}

	for i, rw := range reweights {
		id := 20 + i
		expected := rw / total
		actual := float64(counts[id]) / n
		sigma := math.Sqrt(expected * (1 - expected) / n)
		if math.Abs(actual-expected) > 5*sigma+0.002 {
			t.Fatalf("osd %d reweight %.2f: actual %.5f want %.5f sigma %.5f", id, rw, actual, expected, sigma)
		}
	}
}
