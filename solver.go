package taskpkg

import (
	"math"
	"math/big"
)

// candidate 是单个像素由某个满秩三灯组解出的候选 g 及其评分。
type candidate struct {
	g            [3]*big.Rat
	triple       [3]int
	consistent   int
	squaredError *big.Rat
}

// pixelEval 是单个像素在一个曝光向量下的评估结果。
type pixelEval struct {
	result   PixelResult
	rejected []RejectedObservation
}

// exposureEval 是整个曝光向量在全图上的评估结果。
type exposureEval struct {
	exposure        []*big.Rat
	exposureIndex   int
	pixels          []pixelEval
	totalConsistent int
	totalError      *big.Rat
}

// Solve 执行联合校准，详见包文档。预算耗尽时返回当前最优可行解、
// ErrBudgetExhausted 且 Result.NotOptimal 为 true。
func Solve(p *Problem) (*Result, error) {
	cands, err := validateProblem(p)
	if err != nil {
		return nil, err
	}
	n := len(p.Lights)
	np := len(p.Observations)
	threshold := cloneRat(p.ResidualThreshold)

	// 每个像素的可用（非饱和）灯 ID 与满秩三灯组，与曝光无关：
	// 正曝光只对矩阵行做非零缩放，不改变秩，只计算一次。
	usable := make([][]int, np)
	rankedTriples := make([][][3]int, np)
	var totalTriplesPerExposure int64
	for px := 0; px < np; px++ {
		for k := 0; k < n; k++ {
			if !p.Observations[px][k].Saturated {
				usable[px] = append(usable[px], k)
			}
		}
		rankedTriples[px] = fullRankTriples(p.Lights, usable[px])
		totalTriplesPerExposure += int64(len(rankedTriples[px]))
	}

	enumerator := newExposureEnumerator(cands)
	totalEvals := totalTriplesPerExposure * int64(enumerator.total())

	var (
		best        *exposureEval
		evaluations int64
		budgetHit   bool
	)

	for {
		exposure := enumerator.current()
		expIndex := enumerator.index()
		ev := &exposureEval{
			exposure:      exposure,
			exposureIndex: expIndex,
			pixels:        make([]pixelEval, np),
			totalError:    new(big.Rat),
		}

		for px := 0; px < np; px++ {
			pe, evalCount, stop := evaluatePixel(p, px, rankedTriples[px], exposure, threshold, p.Budget, evaluations)
			evaluations = evalCount
			if stop {
				budgetHit = true
				break
			}
			ev.pixels[px] = pe
			if pe.result.Solved {
				ev.totalConsistent += pe.result.Consistent
				ev.totalError.Add(ev.totalError, pe.result.SquaredError)
			}
		}

		if !budgetHit {
			// 仅把完整评估且至少解出一个像素的曝光向量纳入全图比较。
			if anyPixelSolved(ev) {
				if best == nil || betterExposure(ev, best) {
					best = ev
				}
			}
		}

		if budgetHit || !enumerator.next() {
			break
		}
	}

	if best == nil {
		return nil, ErrNoFeasibleSolution
	}

	result := buildResult(best, evaluations, totalEvals, budgetHit)
	if budgetHit {
		return result, ErrBudgetExhausted
	}
	return result, nil
}

// anyPixelSolved 报告该曝光向量下是否至少有一个像素可解。
func anyPixelSolved(ev *exposureEval) bool {
	for i := range ev.pixels {
		if ev.pixels[i].result.Solved {
			return true
		}
	}
	return false
}

// fullRankTriples 枚举可用灯的全部三灯组，返回其中在原始光向量矩阵
// 下满秩（行列式非零）的组合，按灯 ID 升序排列。
func fullRankTriples(lights [][3]*big.Rat, usableLights []int) [][3]int {
	var out [][3]int
	m := len(usableLights)
	for a := 0; a < m; a++ {
		for b := a + 1; b < m; b++ {
			for c := b + 1; c < m; c++ {
				triple := [3]int{usableLights[a], usableLights[b], usableLights[c]}
				matrix := [3][3]*big.Rat{
					lights[triple[0]],
					lights[triple[1]],
					lights[triple[2]],
				}
				if det3(matrix).Sign() != 0 {
					out = append(out, triple)
				}
			}
		}
	}
	return out
}

// evaluatePixel 评估单个像素在给定曝光向量下的所有满秩三灯组，返回
// 该像素结果、累计评估数，以及 stop=true 表示预算在本像素内耗尽。
func evaluatePixel(
	p *Problem,
	px int,
	triples [][3]int,
	exposure []*big.Rat,
	threshold *big.Rat,
	budget int64,
	evaluations int64,
) (pixelEval, int64, bool) {
	n := len(p.Lights)
	pe := pixelEval{
		result: PixelResult{
			Pixel:        px,
			SquaredError: new(big.Rat),
		},
	}

	// 饱和观测在任何候选评估之前显式拒绝。
	for k := 0; k < n; k++ {
		if p.Observations[px][k].Saturated {
			pe.rejected = append(pe.rejected, RejectedObservation{
				Pixel:      px,
				Light:      k,
				Reason:     RejectSaturated,
				Brightness: cloneRat(p.Observations[px][k].Brightness),
				Threshold:  cloneRat(threshold),
			})
		}
	}

	var bestCandidate *candidate
	for _, triple := range triples {
		if budget > 0 && evaluations > 0 && evaluations >= budget {
			return pe, evaluations, true
		}
		matrix := [3][3]*big.Rat{
			scaledRow(exposure[triple[0]], p.Lights[triple[0]]),
			scaledRow(exposure[triple[1]], p.Lights[triple[1]]),
			scaledRow(exposure[triple[2]], p.Lights[triple[2]]),
		}
		det := det3(matrix)
		evaluations++

		rhs := [3]*big.Rat{
			cloneRat(p.Observations[px][triple[0]].Brightness),
			cloneRat(p.Observations[px][triple[1]].Brightness),
			cloneRat(p.Observations[px][triple[2]].Brightness),
		}
		g := solveCramer(matrix, det, rhs)
		cand := scoreCandidate(p, px, g, triple, exposure, threshold)
		if bestCandidate == nil || betterCandidate(cand, bestCandidate) {
			bestCandidate = cand
		}
	}

	if bestCandidate == nil {
		return pe, evaluations, false
	}
	pe.result = finalizePixelResult(px, bestCandidate)
	pe.rejected = appendResidualRejects(p, px, bestCandidate, exposure, threshold, pe.rejected)
	return pe, evaluations, false
}

// scoreCandidate 在全部非饱和观测上评估候选 g，统计一致数与平方误差。
func scoreCandidate(
	p *Problem,
	px int,
	g [3]*big.Rat,
	triple [3]int,
	exposure []*big.Rat,
	threshold *big.Rat,
) *candidate {
	cand := &candidate{
		g:            g,
		triple:       triple,
		squaredError: new(big.Rat),
	}
	for k := range p.Lights {
		if p.Observations[px][k].Saturated {
			continue
		}
		pred := prediction(exposure[k], p.Lights[k], g)
		residual := new(big.Rat).Sub(pred, p.Observations[px][k].Brightness)
		cand.squaredError.Add(cand.squaredError, ratSquare(residual))
		if ratAbs(residual).Cmp(threshold) <= 0 {
			cand.consistent++
		}
	}
	return cand
}

// prediction 返回 max(0, e * L · g)。
func prediction(e *big.Rat, light [3]*big.Rat, g [3]*big.Rat) *big.Rat {
	raw := new(big.Rat).Mul(e, dot3(light, g))
	return ratMax(raw, ratZero)
}

// betterCandidate 实现像素内候选决胜：一致数降序、平方误差升序、
// 三灯 ID 字典序升序。
func betterCandidate(a, b *candidate) bool {
	if a.consistent != b.consistent {
		return a.consistent > b.consistent
	}
	if cmp := a.squaredError.Cmp(b.squaredError); cmp != 0 {
		return cmp < 0
	}
	for i := 0; i < RequiredTripleLen; i++ {
		if a.triple[i] != b.triple[i] {
			return a.triple[i] < b.triple[i]
		}
	}
	return false
}

// betterExposure 实现全图曝光决胜：一致总数降序、平方误差总和升序、
// 曝光索引升序、各像素三灯 ID 元组字典序升序。
func betterExposure(a, b *exposureEval) bool {
	if a.totalConsistent != b.totalConsistent {
		return a.totalConsistent > b.totalConsistent
	}
	if cmp := a.totalError.Cmp(b.totalError); cmp != 0 {
		return cmp < 0
	}
	if a.exposureIndex != b.exposureIndex {
		return a.exposureIndex < b.exposureIndex
	}
	for px := range a.pixels {
		ta, tb := a.pixels[px].result, b.pixels[px].result
		if !ta.TripleValid || !tb.TripleValid {
			continue
		}
		for i := 0; i < RequiredTripleLen; i++ {
			if ta.Triple[i] != tb.Triple[i] {
				return ta.Triple[i] < tb.Triple[i]
			}
		}
	}
	return false
}

// finalizePixelResult 把胜出候选固化为像素结果，并分解法线与反照率。
func finalizePixelResult(px int, cand *candidate) PixelResult {
	res := PixelResult{
		Pixel:        px,
		G:            cand.g,
		Triple:       cand.triple,
		TripleValid:  true,
		Consistent:   cand.consistent,
		SquaredError: cloneRat(cand.squaredError),
		Solved:       true,
	}

	var normSq float64
	for i := 0; i < 3; i++ {
		f := ratToFloat(cand.g[i])
		res.G[i] = cloneRat(cand.g[i])
		res.Normal[i] = f
		normSq += f * f
	}
	norm := math.Sqrt(normSq)
	res.Albedo = norm
	if norm > 0 {
		for i := 0; i < 3; i++ {
			res.Normal[i] /= norm
		}
	}
	return res
}

// appendResidualRejects 把最终模型下残差超阈的非饱和观测加入拒绝列表，
// 同时为已收集的饱和观测补齐预测值。
func appendResidualRejects(
	p *Problem,
	px int,
	cand *candidate,
	exposure []*big.Rat,
	threshold *big.Rat,
	rejected []RejectedObservation,
) []RejectedObservation {
	for i := range rejected {
		if rejected[i].Reason == RejectSaturated {
			rejected[i].Prediction = prediction(
				exposure[rejected[i].Light],
				p.Lights[rejected[i].Light],
				cand.g,
			)
		}
	}
	for k := range p.Lights {
		obs := p.Observations[px][k]
		if obs.Saturated {
			continue
		}
		pred := prediction(exposure[k], p.Lights[k], cand.g)
		residual := new(big.Rat).Sub(pred, obs.Brightness)
		if ratAbs(residual).Cmp(threshold) > 0 {
			rejected = append(rejected, RejectedObservation{
				Pixel:      px,
				Light:      k,
				Reason:     RejectResidual,
				Brightness: cloneRat(obs.Brightness),
				Prediction: pred,
				Residual:   residual,
				Threshold:  cloneRat(threshold),
			})
		}
	}
	return rejected
}

// buildResult 汇总胜出曝光向量的全图结果。
func buildResult(
	best *exposureEval,
	evaluations int64,
	totalEvals int64,
	budgetHit bool,
) *Result {
	out := &Result{
		Exposure:          make([]*big.Rat, len(best.exposure)),
		ExposureIndex:     best.exposureIndex,
		Pixels:            make([]PixelResult, len(best.pixels)),
		TotalConsistent:   best.totalConsistent,
		TotalSquaredError: cloneRat(best.totalError),
		Evaluations:       evaluations,
		TotalEvaluations:  totalEvals,
		NotOptimal:        budgetHit,
	}
	for k := range best.exposure {
		out.Exposure[k] = cloneRat(best.exposure[k])
	}
	for px := range best.pixels {
		out.Pixels[px] = best.pixels[px].result
		out.Rejected = append(out.Rejected, best.pixels[px].rejected...)
	}
	return out
}
