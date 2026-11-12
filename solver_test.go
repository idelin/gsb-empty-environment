package taskpkg

import (
	"errors"
	"math/big"
	"math/rand"
	"testing"
)

func ratInt(i int64) *big.Rat { return big.NewRat(i, 1) }

// syntheticInstance 描述一个由已知真值生成的扫描实例。
type syntheticInstance struct {
	problem   *Problem
	truthG    [][3]*big.Rat
	truthExp  []*big.Rat
	saturated [][]bool
}

// buildSynthetic 用精确有理真值构造实例：真值曝光从每灯候选中随机挑
// 选（首灯恒为 1），并按 saturated 掩码标记饱和观测。
func buildSynthetic(t *testing.T, rng *rand.Rand, nLights, nPixels int, saturated [][]bool) syntheticInstance {
	t.Helper()

	lights := make([][3]*big.Rat, nLights)
	lights[0] = [3]*big.Rat{ratInt(1), ratInt(0), ratInt(0)}
	lights[1] = [3]*big.Rat{ratInt(0), ratInt(1), ratInt(0)}
	lights[2] = [3]*big.Rat{ratInt(0), ratInt(0), ratInt(1)}
	for k := 3; k < nLights; k++ {
		lights[k] = [3]*big.Rat{
			big.NewRat(int64(rng.Intn(3)-1), 1),
			big.NewRat(int64(rng.Intn(3)-1), 1),
			big.NewRat(int64(rng.Intn(3)+1), 1),
		}
	}

	expCands := make([][]*big.Rat, nLights)
	expCands[0] = []*big.Rat{ratInt(1)}
	truthExp := make([]*big.Rat, nLights)
	truthExp[0] = ratInt(1)
	for k := 1; k < nLights; k++ {
		expCands[k] = []*big.Rat{
			big.NewRat(1, 2), ratInt(1), big.NewRat(3, 2),
		}
		truthExp[k] = expCands[k][rng.Intn(3)]
	}

	truthG := make([][3]*big.Rat, nPixels)
	for px := 0; px < nPixels; px++ {
		albedo := big.NewRat(int64(rng.Intn(3)+2), 1)
		nx := big.NewRat(int64(rng.Intn(3)-1), 2)
		ny := big.NewRat(int64(rng.Intn(3)-1), 2)
		nz := big.NewRat(1, 1)
		truthG[px] = [3]*big.Rat{
			new(big.Rat).Mul(albedo, nx),
			new(big.Rat).Mul(albedo, ny),
			new(big.Rat).Mul(albedo, nz),
		}
	}

	obs := make([][]Observation, nPixels)
	for px := 0; px < nPixels; px++ {
		obs[px] = make([]Observation, nLights)
		for k := 0; k < nLights; k++ {
			raw := new(big.Rat).Mul(truthExp[k], dot3(lights[k], truthG[px]))
			b := raw
			if raw.Sign() < 0 {
				b = ratInt(0)
			}
			obs[px][k] = Observation{Brightness: b, Saturated: saturated[px][k]}
		}
	}

	return syntheticInstance{
		problem: &Problem{
			Lights:             lights,
			ExposureCandidates: expCands,
			Observations:       obs,
			ResidualThreshold:  ratInt(0),
		},
		truthG:   truthG,
		truthExp: truthExp,
	}
}

func cloneObservations(in [][]Observation) [][]Observation {
	out := make([][]Observation, len(in))
	for px := range in {
		out[px] = make([]Observation, len(in[px]))
		for k := range in[px] {
			out[px][k] = Observation{
				Brightness: cloneRat(in[px][k].Brightness),
				Saturated:  in[px][k].Saturated,
			}
		}
	}
	return out
}

func makeSat(nPixels, nLights int) [][]bool {
	sat := make([][]bool, nPixels)
	for px := range sat {
		sat[px] = make([]bool, nLights)
	}
	return sat
}

func TestSolvesCleanSynthetic(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const nLights, nPixels = 4, 3
	inst := buildSynthetic(t, rng, nLights, nPixels, makeSat(nPixels, nLights))

	res, err := Solve(inst.problem)
	if err != nil {
		t.Fatalf("Solve returned error: %v", err)
	}
	if res.NotOptimal {
		t.Fatal("expected optimal result")
	}
	for k := 0; k < nLights; k++ {
		if !ratEq(res.Exposure[k], inst.truthExp[k]) {
			t.Fatalf("exposure[%d] = %v, want %v", k, res.Exposure[k], inst.truthExp[k])
		}
	}
	for px := 0; px < nPixels; px++ {
		pr := res.Pixels[px]
		if !pr.Solved {
			t.Fatalf("pixel %d unsolved", px)
		}
		for c := 0; c < 3; c++ {
			if !ratEq(pr.G[c], inst.truthG[px][c]) {
				t.Fatalf("pixel %d g[%d] = %v, want %v", px, c, pr.G[c], inst.truthG[px][c])
			}
		}
		var normSq float64
		for c := 0; c < 3; c++ {
			normSq += pr.Normal[c] * pr.Normal[c]
		}
		if d := normSq - 1; d > 1e-12 || d < -1e-12 {
			t.Fatalf("pixel %d normal not unit: norm^2=%v", px, normSq)
		}
		if pr.Albedo <= 0 {
			t.Fatalf("pixel %d non-positive albedo %v", px, pr.Albedo)
		}
	}
}

func TestReferenceCrossValidation(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 24; iter++ {
		nLights := 3 + rng.Intn(3)
		nPixels := 1 + rng.Intn(4)
		sat := makeSat(nPixels, nLights)
		for px := range sat {
			for k := range sat[px] {
				if rng.Intn(5) == 0 {
					sat[px][k] = true
				}
			}
			used := 0
			for k := range sat[px] {
				if !sat[px][k] {
					used++
				}
			}
			for used < 3 {
				k := rng.Intn(nLights)
				if sat[px][k] {
					sat[px][k] = false
					used++
				}
			}
		}
		inst := buildSynthetic(t, rng, nLights, nPixels, sat)
		inst.problem.ResidualThreshold = big.NewRat(1, 10)

		got, err := Solve(inst.problem)
		ref, refErr := refSolve(inst.problem)
		if errors.Is(err, ErrNoFeasibleSolution) || errors.Is(refErr, ErrNoFeasibleSolution) {
			if !errors.Is(err, refErr) {
				t.Fatalf("iter %d feasibility divergence: Solve=%v ref=%v", iter, err, refErr)
			}
			continue
		}
		if err != nil {
			t.Fatalf("iter %d Solve error: %v", iter, err)
		}
		if refErr != nil {
			t.Fatalf("iter %d refSolve error: %v", iter, refErr)
		}
		if got.ExposureIndex != ref.exposureIndex {
			t.Fatalf("iter %d exposure index %d != ref %d", iter, got.ExposureIndex, ref.exposureIndex)
		}
		if got.TotalConsistent != ref.totalConsistent {
			t.Fatalf("iter %d consistent %d != ref %d", iter, got.TotalConsistent, ref.totalConsistent)
		}
		if !ratEq(got.TotalSquaredError, ref.totalError) {
			t.Fatalf("iter %d total error %v != ref %v", iter, got.TotalSquaredError, ref.totalError)
		}
		for px := range ref.pixels {
			gt, rp := got.Pixels[px], ref.pixels[px]
			if gt.Triple != rp.triple {
				t.Fatalf("iter %d pixel %d triple %v != ref %v", iter, px, gt.Triple, rp.triple)
			}
			for c := 0; c < 3; c++ {
				if !ratEq(gt.G[c], rp.g[c]) {
					t.Fatalf("iter %d pixel %d g mismatch", iter, px)
				}
			}
		}
	}
}

func TestSaturatedObservationsExcludedAndReported(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	const nLights, nPixels = 4, 2
	sat := makeSat(nPixels, nLights)
	sat[0][3] = true
	inst := buildSynthetic(t, rng, nLights, nPixels, sat)

	res, err := Solve(inst.problem)
	if err != nil {
		t.Fatalf("Solve error: %v", err)
	}
	var sawReject bool
	for _, rj := range res.Rejected {
		if rj.Pixel == 0 && rj.Light == 3 && rj.Reason == RejectSaturated {
			sawReject = true
			if rj.Prediction == nil {
				t.Fatal("saturated rejection missing prediction")
			}
		}
	}
	if !sawReject {
		t.Fatal("saturated observation not reported as rejected")
	}

	corrupted := *inst.problem
	corrupted.Observations = cloneObservations(inst.problem.Observations)
	corrupted.Observations[0][3].Brightness = big.NewRat(9999, 1)
	res2, err := Solve(&corrupted)
	if err != nil {
		t.Fatalf("Solve corrupted error: %v", err)
	}
	if res2.ExposureIndex != res.ExposureIndex {
		t.Fatal("saturated observation leaked into fitting")
	}
}

func TestSharedExposureMatchesReference(t *testing.T) {
	lights := [][3]*big.Rat{
		{ratInt(1), ratInt(0), ratInt(0)},
		{ratInt(0), ratInt(1), ratInt(0)},
		{ratInt(0), ratInt(0), ratInt(1)},
	}
	expCands := [][]*big.Rat{
		{ratInt(1)},
		{ratInt(1), big.NewRat(2, 1)},
		{ratInt(1), big.NewRat(2, 1)},
	}
	obs := [][]Observation{
		{{Brightness: ratInt(1)}, {Brightness: ratInt(1)}, {Brightness: ratInt(1)}},
		{{Brightness: ratInt(2)}, {Brightness: ratInt(0)}, {Brightness: ratInt(1)}},
	}
	p := &Problem{
		Lights:             lights,
		ExposureCandidates: expCands,
		Observations:       obs,
		ResidualThreshold:  ratInt(0),
	}
	res, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve error: %v", err)
	}
	ref, err := refSolve(p)
	if err != nil {
		t.Fatalf("refSolve error: %v", err)
	}
	if res.ExposureIndex != ref.exposureIndex {
		t.Fatalf("shared exposure index %d != ref %d", res.ExposureIndex, ref.exposureIndex)
	}
	if len(res.Exposure) != len(lights) {
		t.Fatal("exposure vector length mismatch")
	}
}

func TestBudgetPrefixIsFeasibleAndFlagged(t *testing.T) {
	rng := rand.New(rand.NewSource(123))
	const nLights, nPixels = 4, 2
	inst := buildSynthetic(t, rng, nLights, nPixels, makeSat(nPixels, nLights))

	full, err := Solve(inst.problem)
	if err != nil {
		t.Fatalf("full Solve error: %v", err)
	}
	if full.TotalEvaluations == 0 || full.Evaluations != full.TotalEvaluations {
		t.Fatalf("bad eval counts: %d/%d", full.Evaluations, full.TotalEvaluations)
	}

	exposureCount := 1
	for k := 1; k < nLights; k++ {
		exposureCount *= len(inst.problem.ExposureCandidates[k])
	}
	firstExposureCost := full.TotalEvaluations / int64(exposureCount)

	budgeted := *inst.problem
	budgeted.Budget = firstExposureCost
	res, err := Solve(&budgeted)
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("want ErrBudgetExhausted, got %v", err)
	}
	if !res.NotOptimal {
		t.Fatal("expected NotOptimal flag")
	}
	if res.Evaluations != firstExposureCost {
		t.Fatalf("evaluations %d, want %d", res.Evaluations, firstExposureCost)
	}
	if res.ExposureIndex != 0 {
		t.Fatalf("budget prefix should keep first exposure, got index %d", res.ExposureIndex)
	}
	for px := range res.Pixels {
		if !res.Pixels[px].Solved {
			t.Fatalf("pixel %d unsolved under feasible prefix", px)
		}
	}
}

func TestBudgetLargerThanTotalIsOptimal(t *testing.T) {
	rng := rand.New(rand.NewSource(55))
	inst := buildSynthetic(t, rng, 4, 2, makeSat(2, 4))
	inst.problem.Budget = 1 << 40
	res, err := Solve(inst.problem)
	if err != nil {
		t.Fatalf("Solve error: %v", err)
	}
	if res.NotOptimal {
		t.Fatal("large budget must yield optimal result")
	}
}

func TestBudgetExactlyTotalIsOptimal(t *testing.T) {
	rng := rand.New(rand.NewSource(56))
	inst := buildSynthetic(t, rng, 4, 2, makeSat(2, 4))
	full, err := Solve(inst.problem)
	if err != nil {
		t.Fatalf("full Solve error: %v", err)
	}
	inst.problem.Budget = full.TotalEvaluations
	res, err := Solve(inst.problem)
	if err != nil {
		t.Fatalf("exact-budget Solve error: %v", err)
	}
	if res.NotOptimal {
		t.Fatal("budget equal to total work must still be optimal")
	}
	if res.Evaluations != full.TotalEvaluations {
		t.Fatalf("evaluations %d, want %d", res.Evaluations, full.TotalEvaluations)
	}
	if res.ExposureIndex != full.ExposureIndex {
		t.Fatalf("exposure index %d, want %d", res.ExposureIndex, full.ExposureIndex)
	}
}

func TestBudgetStopsInsideExposure(t *testing.T) {
	rng := rand.New(rand.NewSource(57))
	inst := buildSynthetic(t, rng, 4, 3, makeSat(3, 4))
	full, err := Solve(inst.problem)
	if err != nil {
		t.Fatalf("full Solve error: %v", err)
	}
	exposureCount := int64(len(inst.problem.ExposureCandidates[1]) *
		len(inst.problem.ExposureCandidates[2]) *
		len(inst.problem.ExposureCandidates[3]))
	perExposure := full.TotalEvaluations / exposureCount
	// 恰好完整评估第一个曝光向量，再在第二个里耗尽：可行解来自索引 0。
	inst.problem.Budget = perExposure + 1
	res, err := Solve(inst.problem)
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("want ErrBudgetExhausted, got %v", err)
	}
	if !res.NotOptimal || res.Evaluations != perExposure+1 {
		t.Fatalf("got notOptimal=%v evals=%d, want true and %d",
			res.NotOptimal, res.Evaluations, perExposure+1)
	}
	if res.ExposureIndex != 0 {
		t.Fatalf("exposure index %d, want 0", res.ExposureIndex)
	}
}

func TestTinyBudgetYieldsNoFeasible(t *testing.T) {
	rng := rand.New(rand.NewSource(58))
	inst := buildSynthetic(t, rng, 4, 3, makeSat(3, 4))
	inst.problem.Budget = 1
	_, err := Solve(inst.problem)
	if !errors.Is(err, ErrNoFeasibleSolution) {
		t.Fatalf("want ErrNoFeasibleSolution, got %v", err)
	}
}

func TestRankDeficientAndUnsolvedPixel(t *testing.T) {
	// 三灯全部落在 z=0 平面，3x3 光矩阵秩亏；取一个无法被张成的
	// 亮度向量，使任何三灯组都无法得到一致候选。
	lights := [][3]*big.Rat{
		{ratInt(1), ratInt(0), ratInt(0)},
		{ratInt(0), ratInt(1), ratInt(0)},
		{ratInt(1), ratInt(1), ratInt(0)},
		{ratInt(2), ratInt(-1), ratInt(0)},
	}
	p := &Problem{
		Lights: lights,
		ExposureCandidates: [][]*big.Rat{
			{ratInt(1)}, {ratInt(1)}, {ratInt(1)}, {ratInt(1)},
		},
		Observations: [][]Observation{
			{
				{Brightness: ratInt(1)}, {Brightness: ratInt(1)},
				{Brightness: ratInt(1)}, {Brightness: ratInt(3)},
			},
		},
		ResidualThreshold: ratInt(0),
	}
	_, err := Solve(p)
	if !errors.Is(err, ErrNoFeasibleSolution) {
		t.Fatalf("want ErrNoFeasibleSolution, got %v", err)
	}
}

func TestFirstExposureFixedToOne(t *testing.T) {
	rng := rand.New(rand.NewSource(321))
	inst := buildSynthetic(t, rng, 3, 1, makeSat(1, 3))
	inst.problem.ExposureCandidates[0] = []*big.Rat{ratInt(7)}
	res, err := Solve(inst.problem)
	if err != nil {
		t.Fatalf("Solve error: %v", err)
	}
	if !ratEq(res.Exposure[0], ratInt(1)) {
		t.Fatalf("first exposure = %v, want 1", res.Exposure[0])
	}
}

func TestExposureEnumeratorOrderAndTotal(t *testing.T) {
	cands := [][]*big.Rat{
		{ratInt(1)},
		{ratInt(1), ratInt(2)},
		{ratInt(1), ratInt(2), ratInt(3)},
	}
	en := newExposureEnumerator(cands)
	if en.total() != 6 {
		t.Fatalf("total = %d, want 6", en.total())
	}
	want := [][]int64{
		{1, 1, 1},
		{1, 1, 2},
		{1, 1, 3},
		{1, 2, 1},
		{1, 2, 2},
		{1, 2, 3},
	}
	for i, w := range want {
		cur := en.current()
		if en.index() != i {
			t.Fatalf("step %d index = %d", i, en.index())
		}
		for k := range w {
			if cur[k].Num().Int64() != w[k] {
				t.Fatalf("step %d light %d = %v, want %d", i, k, cur[k], w[k])
			}
		}
		if i < len(want)-1 && !en.next() {
			t.Fatalf("enumerator ended early at step %d", i)
		}
	}
	if en.next() {
		t.Fatal("enumerator should be exhausted")
	}
}

func TestInvalidInputs(t *testing.T) {
	base := &Problem{
		Lights: [][3]*big.Rat{
			{ratInt(1), ratInt(0), ratInt(0)},
			{ratInt(0), ratInt(1), ratInt(0)},
			{ratInt(0), ratInt(0), ratInt(1)},
		},
		ExposureCandidates: [][]*big.Rat{
			{ratInt(1)}, {ratInt(1)}, {ratInt(1)},
		},
		Observations: [][]Observation{
			{{Brightness: ratInt(1)}, {Brightness: ratInt(1)}, {Brightness: ratInt(1)}},
		},
		ResidualThreshold: ratInt(0),
	}

	cases := map[string]func(*Problem){
		"too few lights": func(p *Problem) {
			p.Lights = p.Lights[:2]
		},
		"too many exposure candidates": func(p *Problem) {
			p.ExposureCandidates[1] = []*big.Rat{ratInt(1), ratInt(2), ratInt(3), ratInt(4)}
		},
		"non-positive exposure": func(p *Problem) {
			p.ExposureCandidates[1] = []*big.Rat{ratInt(0)}
		},
		"negative brightness": func(p *Problem) {
			p.Observations[0][0].Brightness = big.NewRat(-1, 1)
		},
		"negative threshold": func(p *Problem) {
			p.ResidualThreshold = big.NewRat(-1, 1)
		},
		"negative budget": func(p *Problem) {
			p.Budget = -1
		},
		"too many pixels": func(p *Problem) {
			p.Observations = make([][]Observation, MaxPixels+1)
			for i := range p.Observations {
				p.Observations[i] = base.Observations[0]
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := *base
			mutate(&p)
			if _, err := Solve(&p); !errors.Is(err, ErrInvalidProblem) {
				t.Fatalf("want ErrInvalidProblem, got %v", err)
			}
		})
	}
}

func TestDoesNotMutateInput(t *testing.T) {
	rng := rand.New(rand.NewSource(888))
	inst := buildSynthetic(t, rng, 4, 2, makeSat(2, 4))
	snapshot := snapshotProblem(inst.problem)
	if _, err := Solve(inst.problem); err != nil {
		t.Fatalf("Solve error: %v", err)
	}
	if !snapshotEqual(snapshot, inst.problem) {
		t.Fatal("Solve mutated its input")
	}
}

type problemSnapshot struct {
	exposures [][]string
	bright    [][]string
}

func snapshotProblem(p *Problem) problemSnapshot {
	s := problemSnapshot{
		exposures: make([][]string, len(p.ExposureCandidates)),
		bright:    make([][]string, len(p.Observations)),
	}
	for k := range p.ExposureCandidates {
		for _, e := range p.ExposureCandidates[k] {
			s.exposures[k] = append(s.exposures[k], e.RatString())
		}
	}
	for px := range p.Observations {
		for k := range p.Observations[px] {
			s.bright[px] = append(s.bright[px], p.Observations[px][k].Brightness.RatString())
		}
	}
	return s
}

func snapshotEqual(s problemSnapshot, p *Problem) bool {
	for k := range s.exposures {
		if len(s.exposures[k]) != len(p.ExposureCandidates[k]) {
			return false
		}
		for i, v := range s.exposures[k] {
			if v != p.ExposureCandidates[k][i].RatString() {
				return false
			}
		}
	}
	for px := range s.bright {
		for k, v := range s.bright[px] {
			if v != p.Observations[px][k].Brightness.RatString() {
				return false
			}
		}
	}
	return true
}
