package audioalign

import (
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestAlignKnownDrift(t *testing.T) {
	sc := synthConfig{
		n:             16384,
		driftStart:    40,
		driftSlope:    8.0 / 16384.0,
		noiseAmp:      0.35,
		beatPeriod:    307,
		beatAmp:       1.0,
		silencePeriod: 4096,
		silenceGap:    768,
		silenceBoth:   true,
		seed:          42,
	}
	ref, delayed := synthPCM(sc)
	cfg := Config{
		WindowSize: 512, HopSize: 128,
		MinLag: 16, MaxLag: 80,
		SmoothWeight: 0.2, MaxDrift: 2,
		SilenceRMS: 1e-6, MaxPaths: 32,
		CorrEpsilon: 1e-9, CostEpsilon: 1e-12,
	}
	res, err := Align(ref, delayed, cfg)
	if err != nil {
		t.Fatalf("Align failed: %v", err)
	}
	if len(res.Windows) == 0 {
		t.Fatal("expected active windows")
	}
	if len(res.Skipped) == 0 {
		t.Fatal("expected silence windows to be skipped")
	}
	if len(res.Paths) == 0 {
		t.Fatal("expected at least one optimal path")
	}

	for j, w := range res.Windows {
		center := float64(w.Center.Num)
		want := math.Round(sc.driftStart + sc.driftSlope*center)
		if w.BestCorr < 0.65 {
			t.Fatalf("window %d weak NCC %.4f", j, w.BestCorr)
		}
		if w.Confidence <= 0 {
			t.Fatalf("window %d non-positive confidence difference %g", j, w.Confidence)
		}
		for pi, p := range res.Paths {
			got := p.Lags[j].Float()
			if math.Abs(got-want) > 1.0 {
				t.Fatalf("path %d window %d lag %s, want ~%g", pi, j, p.Lags[j], want)
			}
			if p.Lags[j].Den <= 0 {
				t.Fatalf("non-positive denominator: %s", p.Lags[j])
			}
		}
	}

	for pi, p := range res.Paths {
		for j := 1; j < len(p.Lags); j++ {
			hopDist := math.Round((res.Windows[j].Center.Float() - res.Windows[j-1].Center.Float()) / float64(cfg.HopSize))
			bound := cfg.MaxDrift * hopDist
			delta := math.Abs(p.Lags[j].Float() - p.Lags[j-1].Float())
			if delta > bound+1e-12 {
				t.Fatalf("path %d transition %d drifts %g > bound %g", pi, j, delta, bound)
			}
		}
	}
}

func TestDoesNotModifyInput(t *testing.T) {
	sc := synthConfig{n: 4096, driftStart: 24, driftSlope: 0.001, noiseAmp: 0.4, seed: 7}
	ref, delayed := synthPCM(sc)
	refCopy := append([]float64(nil), ref...)
	delCopy := append([]float64(nil), delayed...)

	if _, err := Align(ref, delayed, Config{WindowSize: 512, HopSize: 256, MinLag: 0, MaxLag: 48}); err != nil {
		t.Fatalf("Align failed: %v", err)
	}
	if !reflect.DeepEqual(ref, refCopy) || !reflect.DeepEqual(delayed, delCopy) {
		t.Fatal("Align modified the input PCM slices")
	}
}

func TestBruteForceEquivalence(t *testing.T) {
	sc := synthConfig{
		n: 1600, driftStart: 30, driftSlope: 0.004,
		noiseAmp: 0.5, beatPeriod: 173, beatAmp: 0.8, seed: 99,
	}
	ref, delayed := synthPCM(sc)
	cfg := Config{
		WindowSize: 256, HopSize: 128,
		MinLag: 16, MaxLag: 56,
		SmoothWeight: 0.3, MaxDrift: 4,
		MaxPaths: 100000, CostEpsilon: 1e-15,
	}
	res, err := Align(ref, delayed, cfg)
	if err != nil {
		t.Fatalf("Align failed: %v", err)
	}

	lagSets := make([][]int, len(res.Windows))
	costSets := make([][]float64, len(res.Windows))
	centers := make([]int, len(res.Windows))
	for j, w := range res.Windows {
		type cand struct {
			lag  int
			corr float64
			cost float64
		}
		top := make([]cand, 0, 3)
		for _, c := range w.Candidates {
			top = append(top, cand{int(c.Lag.Num), c.Corr, c.Cost})
		}
		sort.Slice(top, func(i, k int) bool { return top[i].corr > top[k].corr })
		if len(top) > 3 {
			top = top[:3]
		}
		sort.Slice(top, func(i, k int) bool { return top[i].lag < top[k].lag })
		centers[j] = int(w.Center.Num)
		for _, c := range top {
			lagSets[j] = append(lagSets[j], c.lag)
			costSets[j] = append(costSets[j], c.cost)
		}
	}

	dpPaths, truncated, err := solveDP(lagSets, costSets, centers, cfg)
	if err != nil {
		t.Fatalf("solveDP failed: %v", err)
	}
	if truncated {
		t.Fatal("test MaxPaths too small for the restricted space")
	}
	bfPaths := bruteForcePaths(lagSets, costSets, centers, cfg)

	if len(dpPaths) != len(bfPaths) {
		t.Fatalf("optimal path count: DP %d, brute force %d", len(dpPaths), len(bfPaths))
	}
	dpSet := make(map[string]float64, len(dpPaths))
	for _, p := range dpPaths {
		dpSet[lagKey(p.lags)] = p.cost
	}
	for _, p := range bfPaths {
		dpCost, ok := dpSet[lagKey(p.lags)]
		if !ok {
			t.Fatalf("brute-force path missing from DP: %v", p.lags)
		}
		if math.Abs(dpCost-p.cost) > 1e-10 {
			t.Fatalf("cost mismatch for %v: DP %g brute %g", p.lags, dpCost, p.cost)
		}
	}

	independent := 0.0
	for j := range lagSets {
		minLocal := math.Inf(1)
		for _, c := range costSets[j] {
			if c < minLocal {
				minLocal = c
			}
		}
		independent += minLocal
	}
	if independent > dpPaths[0].cost+1e-9 {
		t.Fatalf("DP cost %g below independent local minimum %g", dpPaths[0].cost, independent)
	}
}

func lagKey(lags []int) string {
	parts := make([]string, len(lags))
	for i, l := range lags {
		parts[i] = strconv.Itoa(l)
	}
	return strings.Join(parts, ",")
}

func rationalLags(rs []Rational) []int {
	out := make([]int, len(rs))
	for i, r := range rs {
		if r.Den != 1 {
			out[i] = int(math.Round(r.Float()))
		} else {
			out[i] = int(r.Num)
		}
	}
	return out
}

// bruteForcePaths enumerates every full path in a small candidate space and
// keeps all paths attaining the minimum total cost.
func bruteForcePaths(lagSets [][]int, costSets [][]float64, centers []int, cfg Config) []rawPath {
	var best []rawPath
	minCost := math.Inf(1)

	var walk func(j int, seq []int, total float64)
	walk = func(j int, seq []int, total float64) {
		if j == len(lagSets) {
			if total < minCost-1e-12 {
				minCost = total
				best = []rawPath{{lags: append([]int(nil), seq...), cost: total}}
			} else if math.Abs(total-minCost) <= 1e-12 {
				best = append(best, rawPath{lags: append([]int(nil), seq...), cost: total})
			}
			return
		}
		for i, lag := range lagSets[j] {
			next := total + costSets[j][i]
			if j > 0 {
				hopDist := math.Round(float64(centers[j]-centers[j-1]) / float64(cfg.HopSize))
				bound := cfg.MaxDrift * hopDist
				delta := math.Abs(float64(lag - seq[j-1]))
				if delta > bound {
					continue
				}
				next += cfg.SmoothWeight * delta
			}
			seq = append(seq, lag)
			walk(j+1, seq, next)
			seq = seq[:len(seq)-1]
		}
	}
	walk(0, nil, 0)
	return best
}
