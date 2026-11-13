package netsec

import (
	"math/big"
	"testing"
)

func bi(x int64) *big.Int { return big.NewInt(x) }

func mustSigner(t *testing.T, p, q, e int64) *CRTSigner {
	t.Helper()
	s, err := NewCRTSigner(bi(p), bi(q), bi(e))
	if err != nil {
		t.Fatalf("NewCRTSigner(%d,%d,%d): %v", p, q, e, err)
	}
	return s
}

func TestNewAnalyzerRejectsBadParameters(t *testing.T) {
	cases := []struct {
		name string
		n    *big.Int
		e    *big.Int
	}{
		{"even modulus", bi(14), bi(3)},
		{"small modulus", bi(7), bi(3)},
		{"prime modulus", bi(13), bi(3)},
		{"exponent too small", bi(35), bi(1)},
		{"exponent equals n", bi(35), bi(35)},
		{"exponent above n", bi(35), bi(36)},
		{"nil modulus", nil, bi(3)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewAnalyzer(tc.n, tc.e, nil); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestAnalyzeSampleRejectsOutOfRangeAndNonCoprime(t *testing.T) {
	a, err := NewAnalyzer(bi(35), bi(3), nil)
	if err != nil {
		t.Fatalf("NewAnalyzer: %v", err)
	}

	cases := []struct {
		name string
		m    *big.Int
		s    *big.Int
	}{
		{"negative m", bi(-1), bi(2)},
		{"s equals N", bi(1), bi(35)},
		{"s above N", bi(1), bi(40)},
		{"m shares factor p", bi(7), bi(2)},
		{"m equals N", bi(35), bi(2)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := a.AnalyzeSample(Sample{M: tc.m, S: tc.s})
			if got.Status != StatusInvalid {
				t.Fatalf("expected invalid, got %s (%s)", got.Status, got.Reason)
			}
		})
	}
}

func TestCRTFaultFactorsAndPrimality(t *testing.T) {
	// N = 11*13 = 143, e=7 与 (10)(12)=120 互素。
	signer := mustSigner(t, 11, 13, 7)
	analyzer, err := NewAnalyzer(signer.N(), bi(7), nil)
	if err != nil {
		t.Fatalf("NewAnalyzer: %v", err)
	}

	const m int64 = 4
	good, err := signer.Sign(bi(m))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if r := new(big.Int).Exp(good, bi(7), signer.N()); r.Cmp(bi(m)) != 0 {
		t.Fatalf("good signature does not verify")
	}

	// 11 的有效位为 0..3，翻第 0 位保持余数落在 [0,11)。
	bad, err := signer.SignWithBitFault(bi(m), BranchP, 0)
	if err != nil {
		t.Fatalf("SignWithBitFault: %v", err)
	}
	result := analyzer.AnalyzeSample(Sample{M: bi(m), S: bad})
	if result.Status != StatusFactored {
		t.Fatalf("expected factored, got %s: %s", result.Status, result.Reason)
	}

	structure := result.Factors
	if !structure.ProductIsN {
		t.Fatalf("factor product does not equal N")
	}
	if structure.GPrimality != PrimalityPrime || structure.CofactorPrimality != PrimalityPrime {
		t.Fatalf("factors must be labeled prime: %s, %s", structure.GPrimality, structure.CofactorPrimality)
	}
	if !structure.Deterministic {
		t.Fatalf("64-bit factors should be deterministically classified")
	}
	if new(big.Int).Mod(signer.N(), structure.G).Sign() != 0 {
		t.Fatalf("g does not divide N")
	}
	if structure.G.Cmp(bi(11)) != 0 && structure.G.Cmp(bi(13)) != 0 {
		t.Fatalf("unexpected factor %v", structure.G)
	}

	pair := analyzer.AnalyzePair(bi(m), good, bad)
	if pair.Status != PairAgree {
		t.Fatalf("expected pair agreement, got %s: %s", pair.Status, pair.Reason)
	}
	if pair.Structure.G.Cmp(structure.G) != 0 {
		t.Fatalf("pair and value method returned different factors")
	}
}

func TestZeroAndGarbageSignaturesNeverSucceed(t *testing.T) {
	signer := mustSigner(t, 11, 13, 7)
	analyzer, err := NewAnalyzer(signer.N(), bi(7), nil)
	if err != nil {
		t.Fatalf("NewAnalyzer: %v", err)
	}

	// 这些输入与消息 m=3 无关；若某条恰好给出真实因子，那是真实分解而非假成功，
	// 因此仅断言“不会出现验签成功”，并要求所有非分解结果落入 unfactorable/invalid。
	for _, s := range []int64{0, 1, 2, 100, 142} {
		result := analyzer.AnalyzeSample(Sample{M: bi(3), S: bi(s)})
		if result.Status == StatusValidSignature {
			t.Fatalf("non-signing sample s=%d was reported valid", s)
		}
	}

	// 全零签名显式记录为无法分解（或正常当 m=0，但 m=0 已被 gcd 前置条件拒绝）。
	zero := analyzer.AnalyzeSample(Sample{M: bi(3), S: bi(0)})
	if zero.Status == StatusFactored || zero.Status == StatusValidSignature {
		t.Fatalf("zero signature must not succeed, got %s", zero.Status)
	}
}

func TestBatchDeduplicatesAndRetainsEvidence(t *testing.T) {
	signer := mustSigner(t, 11, 13, 7)
	analyzer, err := NewAnalyzer(signer.N(), bi(7), nil)
	if err != nil {
		t.Fatalf("NewAnalyzer: %v", err)
	}

	good, _ := signer.Sign(bi(4))
	bad, _ := signer.SignWithBitFault(bi(4), BranchQ, 0)
	samples := []Sample{
		{M: bi(4), S: bad},
		{M: bi(4), S: bad}, // 重复，不得重复计数
		{M: bi(4), S: good},
		{M: bi(3), S: bi(0)},  // 不验签，保留证据
		{M: bi(11), S: bi(2)}, // gcd(m,N)!=1，保留证据
	}
	report := analyzer.AnalyzeBatch(samples)

	if report.Total != 5 || report.Duplicates != 1 {
		t.Fatalf("unexpected counts: %+v", report)
	}
	if !report.Success || report.Factored != 1 {
		t.Fatalf("expected exactly one factored sample: %+v", report)
	}
	if report.Valid != 1 || report.Invalid != 1 || report.Unfactor != 1 {
		t.Fatalf("unexpected bucket counts: %+v", report)
	}
	if len(report.Failures) != 2 {
		t.Fatalf("expected two retained failure evidences, got %d", len(report.Failures))
	}
}

func TestCompositeFactorIsNotCalledPrime(t *testing.T) {
	// N = 3*5*7 = 105，e=5。m=1, s=31 时 31^5 mod 105 = 61，
	// gcd(61-1,105)=15；15 本身是合数，必须标注 composite，不能称素数。
	n := bi(105)
	analyzer, err := NewAnalyzer(n, bi(5), nil)
	if err != nil {
		t.Fatalf("NewAnalyzer: %v", err)
	}

	result := analyzer.AnalyzeSample(Sample{M: bi(1), S: bi(31)})
	if result.Status != StatusFactored {
		t.Fatalf("expected factored, got %s (%s)", result.Status, result.Reason)
	}
	if result.Factors.G.Cmp(bi(15)) != 0 {
		t.Fatalf("expected g=15, got %v", result.Factors.G)
	}
	if result.Factors.GPrimality != PrimalityComposite {
		t.Fatalf("g=15 is composite but labeled %s", result.Factors.GPrimality)
	}
	if result.Factors.CofactorPrimality != PrimalityPrime {
		t.Fatalf("cofactor 7 should be prime, got %s", result.Factors.CofactorPrimality)
	}
	if !result.Factors.ProductIsN {
		t.Fatalf("product check failed")
	}
}

func TestAnalyzePairGuards(t *testing.T) {
	signer := mustSigner(t, 11, 13, 7)
	analyzer, _ := NewAnalyzer(signer.N(), bi(7), nil)

	goodA, _ := signer.Sign(bi(4))
	goodB, _ := signer.Sign(bi(5))
	badA, _ := signer.SignWithBitFault(bi(4), BranchP, 0)

	// 不同消息：两种 gcd 方法不会稳定指向同一单分支因子，绝不允许报告一致成功。
	if r := analyzer.AnalyzePair(bi(4), goodA, goodB); r.Status == PairAgree {
		t.Fatalf("different-message pair must not be reported as agreement")
	}
	// s_good 本身不验签：拒绝，杜绝假成功。
	if r := analyzer.AnalyzePair(bi(4), badA, badA); r.Status != PairInvalid {
		t.Fatalf("non-verifying s_good must be invalid, got %s", r.Status)
	}
	// 两份都是正常签名：无故障。
	if r := analyzer.AnalyzePair(bi(4), goodA, goodA); r.Status != PairNoFault {
		t.Fatalf("identical good signatures must report no fault, got %s", r.Status)
	}
}

func TestAnalyzePairDetectsModelViolation(t *testing.T) {
	// 构造双分支同时故障：两个分支余数都被翻转，两种 gcd 方法会得到 1 或不同因子，
	// 必须报告模型违背，而不是输出假因子。
	signer := mustSigner(t, 11, 13, 7)
	analyzer, _ := NewAnalyzer(signer.N(), bi(7), nil)

	m := bi(4)
	good, _ := signer.Sign(m)
	sp, sq, err := signer.BranchResults(m)
	if err != nil {
		t.Fatalf("BranchResults: %v", err)
	}
	fp, _ := flipWithin(sp, signer.p, 0)
	fq, _ := flipWithin(sq, signer.q, 0)
	doubleFault := signer.combine(fp, fq)

	r := analyzer.AnalyzePair(m, good, doubleFault)
	if r.Status == PairAgree {
		t.Fatalf("double-branch fault must not be reported as agreement")
	}
	if r.Status != PairModelViolation {
		t.Fatalf("expected model violation, got %s: %s", r.Status, r.Reason)
	}
}

func TestLargeModulusRequiresCallbackAndUsesIt(t *testing.T) {
	// 2^68 + ？ 构造一个超出 64 位的奇合数：取两个大素数的乘积。
	bigPrime1 := new(big.Int)
	bigPrime1.SetString("18446744073709551629", 10) // 2^64+13，素数
	bigPrime2 := bi(17)
	n := new(big.Int).Mul(bigPrime1, bigPrime2)

	if _, err := NewAnalyzer(n, bi(3), nil); err == nil {
		t.Fatalf(">64-bit composite without callback must be rejected")
	}

	// 宿主回调仅区分已知的两个素因子，其它值称合数。
	cb := func(x *big.Int) bool {
		return x.Cmp(bigPrime1) == 0 || x.Cmp(bigPrime2) == 0
	}
	analyzer, err := NewAnalyzer(n, bi(3), cb)
	if err != nil {
		t.Fatalf("NewAnalyzer with callback: %v", err)
	}

	// 直接验证 classify 路径：因子 17 为素数，N/17 为素数，其余标记来源为回调。
	if got := classifyPrimality(bi(17), analyzer.cb); got != PrimalityPrime {
		t.Fatalf("17 should classify prime, got %s", got)
	}
	if got := classifyPrimality(n, analyzer.cb); got != PrimalityComposite {
		t.Fatalf("N should classify composite via callback, got %s", got)
	}
}
