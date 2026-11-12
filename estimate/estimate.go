// Package estimate 提供覆盖采样去偏后的路径签名加权频次估计。
//
// 覆盖采样会让长执行记录被重复计数：同一次测试运行中同一路径签名即使
// 出现多次也只应计一次。本包按运行（run）去重，并以含抽样概率的有理
// 权重（Horvitz–Thompson 权重 w = 1/p）维护精确（math/big.Rat）的加权
// 计数与平方和，支持按运行 ID 幂等写入、冲突拒绝与精确撤回。
package estimate

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"
)

// Signature 是被观测路径的统计单元（例如调用栈或边序列的哈希签名）。
type Signature string

// Run 描述一次测试运行中去重后的路径签名观测。
type Run struct {
	// ID 是运行的唯一标识，重复提交据此做幂等判定。
	ID string

	// Probability 是本次运行中路径签名的入样概率 p，要求 0 < p <= 1。
	// 权重取精确有理值 1/p。
	Probability *big.Rat

	// Signatures 为本次运行出现过的路径签名；重复元素会被去重，只计一次。
	Signatures []Signature
}

var (
	// ErrInvalidRun 表示运行字段不合法（空 ID、概率越界、参数为空等）。
	ErrInvalidRun = errors.New("estimate: invalid run")

	// ErrRunConflict 表示同一运行 ID 已存在，且本次提交内容与其不一致。
	ErrRunConflict = errors.New("estimate: run id conflict")

	// ErrRunNotFound 表示撤回时找不到对应运行 ID。
	ErrRunNotFound = errors.New("estimate: run not found")
)

// recordedRun 保存一次运行的规范化内容，用于幂等比较与精确撤回。
type recordedRun struct {
	probability *big.Rat
	weight      *big.Rat
	signatures  []Signature // 已排序、去重
}

// tally 是单个路径签名的精确累计统计。
type tally struct {
	sumWeight  *big.Rat // S1 = Σ w_r
	sumSquared *big.Rat // S2 = Σ w_r²，置信区间/方差所需平方和
	contribs   int      // 贡献该签名的运行数
}

// Aggregator 并发安全地汇总多次运行的精确加权统计。
type Aggregator struct {
	mu   sync.Mutex
	runs map[string]*recordedRun
	rows map[Signature]*tally
}

// New 创建一个空的聚合器。
func New() *Aggregator {
	return &Aggregator{
		runs: make(map[string]*recordedRun),
		rows: make(map[Signature]*tally),
	}
}

// Add 幂等录入一次运行。
//
// 相同 ID 且内容（概率与签名集合）完全一致时为空操作；相同 ID 但内容
// 不一致时返回包装了 ErrRunConflict 的错误且保持原状态不变。
func (a *Aggregator) Add(run Run) error {
	if run.ID == "" {
		return fmt.Errorf("%w: empty run id", ErrInvalidRun)
	}
	if run.Probability == nil {
		return fmt.Errorf("%w: nil inclusion probability", ErrInvalidRun)
	}
	if run.Probability.Sign() <= 0 || run.Probability.Cmp(big.NewRat(1, 1)) > 0 {
		return fmt.Errorf("%w: inclusion probability must satisfy 0 < p <= 1, got %s",
			ErrInvalidRun, run.Probability.RatString())
	}
	sigs := normalizeSignatures(run.Signatures)

	a.mu.Lock()
	defer a.mu.Unlock()

	if existing, ok := a.runs[run.ID]; ok {
		if existing.probability.Cmp(run.Probability) == 0 &&
			equalSignatureSet(existing.signatures, sigs) {
			return nil // 幂等：重复提交同一运行
		}
		return fmt.Errorf("%w: run %q already recorded with different content",
			ErrRunConflict, run.ID)
	}

	prob := new(big.Rat).Set(run.Probability)
	rec := &recordedRun{
		probability: prob,
		weight:      new(big.Rat).Inv(prob),
		signatures:  sigs,
	}
	squaredWeight := new(big.Rat).Mul(rec.weight, rec.weight)

	for _, sig := range sigs {
		row := a.rows[sig]
		if row == nil {
			row = &tally{sumWeight: new(big.Rat), sumSquared: new(big.Rat)}
			a.rows[sig] = row
		}
		row.sumWeight.Add(row.sumWeight, rec.weight)
		row.sumSquared.Add(row.sumSquared, squaredWeight)
		row.contribs++
	}
	a.runs[run.ID] = rec
	return nil
}

// Retract 精确撤回一次运行：从所有相关签名的计数与平方和中减去其贡献。
// 撤回不存在的运行 ID 返回包装了 ErrRunNotFound 的错误。
func (a *Aggregator) Retract(runID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	rec, ok := a.runs[runID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrRunNotFound, runID)
	}
	squaredWeight := new(big.Rat).Mul(rec.weight, rec.weight)

	for _, sig := range rec.signatures {
		row := a.rows[sig]
		row.sumWeight.Sub(row.sumWeight, rec.weight)
		row.sumSquared.Sub(row.sumSquared, squaredWeight)
		row.contribs--
		if row.contribs == 0 {
			delete(a.rows, sig)
		}
	}
	delete(a.runs, runID)
	return nil
}

// Estimate 返回签名的精确加权计数 Σ 1/p_r；未见过的签名返回 0。
func (a *Aggregator) Estimate(sig Signature) *big.Rat {
	a.mu.Lock()
	defer a.mu.Unlock()
	if row, ok := a.rows[sig]; ok {
		return new(big.Rat).Set(row.sumWeight)
	}
	return new(big.Rat)
}

// SumSquared 返回置信区间所需的精确平方和 Σ (1/p_r)²；未见过的签名返回 0。
func (a *Aggregator) SumSquared(sig Signature) *big.Rat {
	a.mu.Lock()
	defer a.mu.Unlock()
	if row, ok := a.rows[sig]; ok {
		return new(big.Rat).Set(row.sumSquared)
	}
	return new(big.Rat)
}

// Variance 在各运行独立入样（Poisson / Horvitz–Thompson）假设下返回
// 估计量方差，即平方和本身。
func (a *Aggregator) Variance(sig Signature) *big.Rat {
	return a.SumSquared(sig)
}

// ContributingRuns 返回对该签名有贡献的运行数量；未见过的签名返回 0。
func (a *Aggregator) ContributingRuns(sig Signature) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if row, ok := a.rows[sig]; ok {
		return row.contribs
	}
	return 0
}

// Has 报告运行 ID 是否已录入。
func (a *Aggregator) Has(runID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.runs[runID]
	return ok
}

// RunCount 返回当前保留的运行数量。
func (a *Aggregator) RunCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.runs)
}

// Signatures 返回当前有非零贡献的全部签名，按字典序排列。
func (a *Aggregator) Signatures() []Signature {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Signature, 0, len(a.rows))
	for sig := range a.rows {
		out = append(out, sig)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ConfidenceInterval 返回加权计数的近似正态置信区间 [lo, hi]：
// Estimate ± z·sqrt(Variance)，z 为调用方给定的正态临界值（如 1.96）。
//
// 区间是 big.Float 浮点近似；精确值请使用 Estimate 与 SumSquared。
// 未见过的签名返回 (0, 0)。z 为 nil 或负数时返回错误。
func (a *Aggregator) ConfidenceInterval(sig Signature, z *big.Float) (lo, hi *big.Float, err error) {
	if z == nil {
		return nil, nil, errors.New("estimate: nil critical value z")
	}
	if z.Sign() < 0 {
		return nil, nil, errors.New("estimate: critical value z must be non-negative")
	}

	precision := uint(64)
	if z.Prec() > precision {
		precision = z.Prec()
	}

	a.mu.Lock()
	estimate := new(big.Rat)
	variance := new(big.Rat)
	if row, ok := a.rows[sig]; ok {
		estimate.Set(row.sumWeight)
		variance.Set(row.sumSquared)
	}
	a.mu.Unlock()

	est := new(big.Float).SetPrec(precision).SetRat(estimate)
	se := new(big.Float).SetPrec(precision).Sqrt(
		new(big.Float).SetPrec(precision).SetRat(variance))
	delta := new(big.Float).SetPrec(precision).Mul(z, se)

	lo = new(big.Float).SetPrec(precision).Sub(est, delta)
	hi = new(big.Float).SetPrec(precision).Add(est, delta)
	return lo, hi, nil
}

// normalizeSignatures 去重并排序，保证同一次运行中每个签名只计一次，
// 同时让幂等比较与撤回遍历具有确定性。
func normalizeSignatures(in []Signature) []Signature {
	if len(in) == 0 {
		return nil
	}
	set := make(map[Signature]struct{}, len(in))
	for _, sig := range in {
		set[sig] = struct{}{}
	}
	out := make([]Signature, 0, len(set))
	for sig := range set {
		out = append(out, sig)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func equalSignatureSet(a, b []Signature) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
