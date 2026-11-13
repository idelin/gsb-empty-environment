package netsec

import (
	"errors"
	"math/big"
)

var zero = big.NewInt(0)
var one = big.NewInt(1)

// Sample 是一份待分析的签名样本：消息代表 m 与签名值 s，均为非负且小于 N。
type Sample struct {
	M *big.Int
	S *big.Int
}

// Status 描述单份样本的分析结论。
type Status int

const (
	// StatusInvalid 表示输入不满足前置条件（区间、gcd(m,N)=1 等），样本被拒绝。
	StatusInvalid Status = iota
	// StatusValidSignature 表示 s^e mod N == m，样本是正常签名，不含故障信息。
	StatusValidSignature
	// StatusFactored 表示 gcd(v-m, N) 给出了 N 的非平凡因子。
	StatusFactored
	// StatusUnfactorable 表示 gcd 为 1 或 N，无法由该样本分解 N。
	StatusUnfactorable
)

// String 返回状态的可读描述。
func (s Status) String() string {
	switch s {
	case StatusInvalid:
		return "invalid"
	case StatusValidSignature:
		return "valid-signature"
	case StatusFactored:
		return "factored"
	case StatusUnfactorable:
		return "unfactorable"
	default:
		return "unknown"
	}
}

// Factor 是 N 的一个非平凡因子及其结构说明。
type Factor struct {
	// Value 为因子值，满足 1 < Value < N。
	Value *big.Int
	// Primality 是该因子的素性结论；证据不足时为 PrimalityUnknown，绝不臆断为素数。
	Primality Primality
}

// FactorStructure 说明一次成功分解得到的两个因子及其乘积校验结果。
type FactorStructure struct {
	// G 是直接由 gcd 计算得到的因子。
	G *big.Int
	// Cofactor 是 N/G。
	Cofactor *big.Int
	// ProductIsN 表示 G*Cofactor == N。
	ProductIsN bool
	// GPrimality 是 G 的素性结论。
	GPrimality Primality
	// CofactorPrimality 是 Cofactor 的素性结论。
	CofactorPrimality Primality
	// Deterministic 为 true 表示两个因子的素性结论都来自 64 位确定性
	// Miller-Rabin 基集合（无论结论是素数还是合数）；为 false 表示结论
	// 依赖宿主回调。
	Deterministic bool
}

// SampleResult 是 AnalyzeSample 对单份样本的完整结论。
type SampleResult struct {
	Status Status
	// Reason 在 StatusInvalid / StatusUnfactorable 时给出原因，便于保留证据。
	Reason string
	// V 是重算值 s^e mod N。
	V *big.Int
	// GCD 是 gcd(v-m mod N, N)，所有非拒绝样本都会记录。
	GCD *big.Int
	// Factors 仅在 StatusFactored 时非空。
	Factors *FactorStructure
}

// Evidence 保留无法分解或被拒绝的故障样本证据，供回归台复盘。
type Evidence struct {
	Index  int
	Sample Sample
	Status Status
	Reason string
	V      *big.Int
	GCD    *big.Int
}

// Report 是批处理结论。
type Report struct {
	Total      int
	Duplicates int
	Invalid    int
	Valid      int
	Factored   int
	Unfactor   int
	// Success 在至少一份样本真实分解 N 时为 true。
	Success bool
	// Structure 是首个成功分解的结构说明；未成功时为 nil。
	Structure *FactorStructure
	// Failures 保留全部无法分解与被拒绝样本的证据。
	Failures []Evidence
}

// Analyzer 绑定一组 RSA 公开参数与可选素性回调，可重复并发使用。
type Analyzer struct {
	n  *big.Int
	e  *big.Int
	cb PrimalityCallback
}

// NewAnalyzer 校验公开参数并构造分析器。
// N 必须是奇合数；e 必须满足 1 < e < N。N 超出 64 位且无法借助回调确认其为
// 合数时返回错误，因为此时无法对“N 为合数”与因子结构给出负责任的结论。
func NewAnalyzer(n, e *big.Int, cb PrimalityCallback) (*Analyzer, error) {
	if n == nil || e == nil {
		return nil, errors.New("netsec: N and e must be non-nil")
	}
	if n.Sign() <= 0 || e.Sign() <= 0 {
		return nil, errors.New("netsec: N and e must be positive")
	}
	if n.Bit(0) == 0 {
		return nil, errors.New("netsec: N must be odd")
	}
	if n.Cmp(big.NewInt(9)) < 0 {
		return nil, errors.New("netsec: N must be an odd composite at least 9")
	}
	if e.Cmp(big.NewInt(1)) <= 0 || e.Cmp(n) >= 0 {
		return nil, errors.New("netsec: require 1 < e < N")
	}

	switch classifyPrimality(n, cb) {
	case PrimalityPrime:
		return nil, errors.New("netsec: N must be composite but is reported prime")
	case PrimalityUnknown:
		return nil, errors.New("netsec: N exceeds 64 bits and no primality callback was provided")
	}

	return &Analyzer{
		n:  new(big.Int).Set(n),
		e:  new(big.Int).Set(e),
		cb: cb,
	}, nil
}

// N 返回分析器绑定的模数副本。
func (a *Analyzer) N() *big.Int { return new(big.Int).Set(a.n) }

// E 返回分析器绑定的公开指数副本。
func (a *Analyzer) E() *big.Int { return new(big.Int).Set(a.e) }

// inRange 要求 x 非负且严格小于 N。
func (a *Analyzer) inRange(x *big.Int) bool {
	return x != nil && x.Sign() >= 0 && x.Cmp(a.n) < 0
}

// gcdN 返回 gcd(x, N) 的新实例。
func (a *Analyzer) gcdN(x *big.Int) *big.Int {
	g := new(big.Int)
	return g.GCD(nil, nil, x, a.n)
}

// AnalyzeSample 分析单份 (m, s) 样本，绝不修改入参。
func (a *Analyzer) AnalyzeSample(sample Sample) SampleResult {
	m, s := sample.M, sample.S
	if !a.inRange(m) || !a.inRange(s) {
		return SampleResult{Status: StatusInvalid, Reason: "m and s must be non-negative and smaller than N"}
	}

	gM := a.gcdN(m)
	if gM.Cmp(one) != 0 {
		return SampleResult{Status: StatusInvalid, Reason: "gcd(m, N) != 1"}
	}

	v := new(big.Int).Exp(s, a.e, a.n)
	diff := new(big.Int).Sub(v, m)
	diff.Mod(diff, a.n)
	g := a.gcdN(diff)

	if v.Cmp(m) == 0 {
		return SampleResult{Status: StatusValidSignature, V: v, GCD: g}
	}

	if g.Cmp(one) <= 0 || g.Cmp(a.n) >= 0 {
		reason := "gcd(v-m, N) = 1, no factor leaked"
		if g.Cmp(a.n) == 0 {
			reason = "gcd(v-m, N) = N, no non-trivial factor leaked"
		}
		return SampleResult{Status: StatusUnfactorable, Reason: reason, V: v, GCD: g}
	}

	return SampleResult{
		Status:  StatusFactored,
		V:       v,
		GCD:     g,
		Factors: a.describeFactors(g),
	}
}

// describeFactors 构造因子结构说明，并以乘积与素性证据约束措辞。
func (a *Analyzer) describeFactors(g *big.Int) *FactorStructure {
	cofactor := new(big.Int).Quo(a.n, g)
	product := new(big.Int).Mul(g, cofactor)

	_, gOK := IsPrime64(g)
	_, cOK := IsPrime64(cofactor)

	return &FactorStructure{
		G:                 new(big.Int).Set(g),
		Cofactor:          cofactor,
		ProductIsN:        product.Cmp(a.n) == 0,
		GPrimality:        classifyPrimality(g, a.cb),
		CofactorPrimality: classifyPrimality(cofactor, a.cb),
		Deterministic:     gOK && cOK,
	}
}

// AnalyzeBatch 顺序分析多份样本。重复 (m,s) 只分析一次并计入 Duplicates；
// 任何重复、全零、异消息或不验签样本都不会产生假成功，失败证据全部保留。
func (a *Analyzer) AnalyzeBatch(samples []Sample) Report {
	report := Report{Total: len(samples)}
	seen := make(map[string]struct{}, len(samples))

	for idx, sample := range samples {
		key := sampleKey(sample)
		if _, dup := seen[key]; dup {
			report.Duplicates++
			continue
		}
		seen[key] = struct{}{}

		result := a.AnalyzeSample(sample)
		switch result.Status {
		case StatusInvalid:
			report.Invalid++
			report.Failures = append(report.Failures, evidence(idx, sample, result))
		case StatusValidSignature:
			report.Valid++
		case StatusFactored:
			report.Factored++
			report.Success = true
			if report.Structure == nil {
				report.Structure = result.Factors
			}
		case StatusUnfactorable:
			report.Unfactor++
			report.Failures = append(report.Failures, evidence(idx, sample, result))
		}
	}
	return report
}

func sampleKey(sample Sample) string {
	m := sample.M
	s := sample.S
	if m == nil || s == nil {
		return "nil"
	}
	return "m=" + m.Text(36) + "|s=" + s.Text(36)
}

func evidence(index int, sample Sample, result SampleResult) Evidence {
	return Evidence{
		Index:  index,
		Sample: sample,
		Status: result.Status,
		Reason: result.Reason,
		V:      result.V,
		GCD:    result.GCD,
	}
}
