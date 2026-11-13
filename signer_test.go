package netsec

import (
	"math/big"
	"testing"
)

func TestNewCRTSignerValidation(t *testing.T) {
	cases := []struct {
		name    string
		p, q, e int64
	}{
		{"composite factor", 9, 13, 7},
		{"equal factors", 11, 11, 7},
		{"exponent not coprime", 11, 13, 3}, // gcd(3,120)=3
		{"exponent out of range", 11, 13, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewCRTSigner(bi(tc.p), bi(tc.q), bi(tc.e)); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestBitFaultBounds(t *testing.T) {
	// p=3（位宽 2，合法位 0、1），q=5（位宽 3，合法位 0..2）。
	signer := mustSigner(t, 3, 5, 3)

	if _, err := signer.SignWithBitFault(bi(1), BranchP, 2); err != ErrFaultPosition {
		t.Fatalf("p-branch bit 2 must be rejected, got %v", err)
	}
	if _, err := signer.SignWithBitFault(bi(1), BranchQ, 3); err != ErrFaultPosition {
		t.Fatalf("q-branch bit 3 must be rejected, got %v", err)
	}

	// 位宽内的位仍可能因具体余数翻转后越界而被拒绝（例如 sp=1 翻 bit1 得 3，
	// 不小于 p=3）；该拒绝同样返回 ErrFaultPosition，作为无法注入的证据。
	var accepted int
	for bit := uint(0); bit < uint(signer.p.BitLen()); bit++ {
		_, err := signer.SignWithBitFault(bi(1), BranchP, bit)
		if err == ErrFaultPosition {
			continue
		}
		if err != nil {
			t.Fatalf("unexpected error at bit %d: %v", bit, err)
		}
		accepted++
	}
	if accepted == 0 {
		t.Fatalf("expected at least one injectable in-range fault bit")
	}
}

func TestFaultySignatureDiffersAndDoesNotVerify(t *testing.T) {
	signer := mustSigner(t, 11, 13, 7)
	m := bi(4)
	good, err := signer.Sign(m)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	bad, err := signer.SignWithBitFault(m, BranchP, 0)
	if err != nil {
		t.Fatalf("SignWithBitFault: %v", err)
	}
	if good.Cmp(bad) == 0 {
		t.Fatalf("fault injection did not change the signature")
	}
	v := new(big.Int).Exp(bad, bi(7), signer.N())
	if v.Cmp(m) == 0 {
		t.Fatalf("faulty signature unexpectedly verifies")
	}
}

func TestUnfactorableSampleRetainedAsEvidence(t *testing.T) {
	// N=35, e=5, m=1, s=2：v=2^5 mod 35=32，gcd(32-1,35)=gcd(31,35)=1，
	// 无法分解，但必须作为证据保留，不能崩溃也不能假成功。
	analyzer, err := NewAnalyzer(bi(35), bi(5), nil)
	if err != nil {
		t.Fatalf("NewAnalyzer: %v", err)
	}
	result := analyzer.AnalyzeSample(Sample{M: bi(1), S: bi(2)})
	if result.Status != StatusUnfactorable {
		t.Fatalf("expected unfactorable, got %s", result.Status)
	}
	if result.GCD.Cmp(one) != 0 {
		t.Fatalf("expected gcd 1, got %v", result.GCD)
	}

	report := analyzer.AnalyzeBatch([]Sample{{M: bi(1), S: bi(2)}})
	if report.Success || report.Unfactor != 1 || len(report.Failures) != 1 {
		t.Fatalf("unfactorable sample must be retained as evidence: %+v", report)
	}
	if report.Failures[0].Status != StatusUnfactorable {
		t.Fatalf("evidence status mismatch: %+v", report.Failures[0])
	}
}

func TestInputBigIntsAreNotMutated(t *testing.T) {
	n, e, m, s := bi(143), bi(7), bi(4), bi(8)
	nCopy, eCopy, mCopy, sCopy := bi(143), bi(7), bi(4), bi(8)
	analyzer, err := NewAnalyzer(n, e, nil)
	if err != nil {
		t.Fatalf("NewAnalyzer: %v", err)
	}
	_ = analyzer.AnalyzeSample(Sample{M: m, S: s})
	signer := mustSigner(t, 11, 13, 7)
	good, _ := signer.Sign(m)
	_ = analyzer.AnalyzePair(m, good, s)
	if n.Cmp(nCopy) != 0 || e.Cmp(eCopy) != 0 || m.Cmp(mCopy) != 0 || s.Cmp(sCopy) != 0 {
		t.Fatalf("analyzer mutated caller-provided big.Int values")
	}
}
