package netsec

import (
	"math/big"
	"testing"
)

func TestStringersAndAccessors(t *testing.T) {
	if PrimalityUnknown.String() != "unknown" || PrimalityPrime.String() != "prime" ||
		PrimalityComposite.String() != "composite" || Primality(99).String() != "unknown" {
		t.Fatal("primality stringer mismatch")
	}
	statuses := map[Status]string{
		StatusInvalid: "invalid", StatusValidSignature: "valid-signature",
		StatusFactored: "factored", StatusUnfactorable: "unfactorable",
		Status(99): "unknown",
	}
	for st, want := range statuses {
		if st.String() != want {
			t.Fatalf("status %v string = %q, want %q", st, st.String(), want)
		}
	}
	pairStatuses := map[PairStatus]string{
		PairInvalid: "invalid", PairNoFault: "no-fault",
		PairAgree: "agree", PairModelViolation: "model-violation", PairStatus(99): "unknown",
	}
	for st, want := range pairStatuses {
		if st.String() != want {
			t.Fatalf("pair status %v string = %q, want %q", st, st.String(), want)
		}
	}
	if BranchP.String() != "p-branch" || BranchQ.String() != "q-branch" ||
		Branch(99).String() != "unknown-branch" {
		t.Fatal("branch stringer mismatch")
	}

	analyzer, err := NewAnalyzer(bi(143), bi(7), nil)
	if err != nil {
		t.Fatalf("NewAnalyzer: %v", err)
	}
	if analyzer.N().Cmp(bi(143)) != 0 || analyzer.E().Cmp(bi(7)) != 0 {
		t.Fatal("analyzer accessor mismatch")
	}

	signer := mustSigner(t, 11, 13, 7)
	if signer.N().Cmp(bi(143)) != 0 || signer.P().Cmp(bi(11)) != 0 || signer.Q().Cmp(bi(13)) != 0 {
		t.Fatal("signer accessor mismatch")
	}
}

func TestIsPrime64Edges(t *testing.T) {
	cases := map[*big.Int]struct {
		prime bool
		ok    bool
	}{
		bi(0): {false, true}, bi(1): {false, true}, bi(2): {true, true},
		bi(3): {true, true}, bi(4): {false, true}, bi(143): {false, true},
		bi(-5): {false, false}, nil: {false, false},
	}
	for n, want := range cases {
		prime, ok := IsPrime64(n)
		if prime != want.prime || ok != want.ok {
			t.Fatalf("IsPrime64(%v)=(%v,%v), want (%v,%v)", n, prime, ok, want.prime, want.ok)
		}
	}
	over := new(big.Int).Lsh(bi(1), 64)
	if prime, ok := IsPrime64(over); prime || ok {
		t.Fatalf("IsPrime64(2^64) must report not-deterministic")
	}
}

func TestSignerAndAnalyzerRejectBadMessage(t *testing.T) {
	signer := mustSigner(t, 11, 13, 7)
	if _, err := signer.Sign(nil); err == nil {
		t.Fatal("nil message must be rejected")
	}
	if _, err := signer.Sign(signer.N()); err == nil {
		t.Fatal("m == N must be rejected")
	}
	if _, _, err := signer.BranchResults(bi(-1)); err == nil {
		t.Fatal("negative message must be rejected by BranchResults")
	}
	if _, err := signer.SignWithBitFault(bi(143), BranchP, 0); err == nil {
		t.Fatal("out-of-range message must be rejected")
	}
	if _, err := signer.SignWithBitFault(bi(1), Branch(99), 0); err == nil {
		t.Fatal("unknown branch must be rejected")
	}

	analyzer, _ := NewAnalyzer(bi(143), bi(7), nil)
	if r := analyzer.AnalyzeSample(Sample{M: nil, S: bi(1)}); r.Status != StatusInvalid {
		t.Fatalf("nil m must be invalid, got %s", r.Status)
	}
}

func TestNewCRTSignerRejectsNonDeterministicPrime(t *testing.T) {
	over := new(big.Int).Lsh(bi(1), 64)
	if _, err := NewCRTSigner(over, bi(13), bi(7)); err == nil {
		t.Fatal("signer must reject primes outside deterministic 64-bit range")
	}
	if _, err := NewCRTSigner(nil, bi(13), bi(7)); err == nil {
		t.Fatal("signer must reject nil p")
	}
}

func TestNewAnalyzerNilAndPrimeCallback(t *testing.T) {
	if _, err := NewAnalyzer(nil, bi(3), nil); err == nil {
		t.Fatal("nil N must be rejected")
	}
	if _, err := NewAnalyzer(bi(35), nil, nil); err == nil {
		t.Fatal("nil e must be rejected")
	}
	// 超出 64 位时回调是唯一依据：回调把 N 报为素数则构造器必须拒绝。
	overN := new(big.Int).Mul(new(big.Int).Lsh(bi(1), 64), bi(3))
	if _, err := NewAnalyzer(overN, bi(3), func(*big.Int) bool { return true }); err == nil {
		t.Fatal("over-64-bit N reported prime by callback must be rejected")
	}
}

func TestPairNoFaultAndTrivialGCDs(t *testing.T) {
	analyzer, _ := NewAnalyzer(bi(35), bi(5), nil)
	// m=1, s_good=s_bad=1：正常签名且签名差为 0，gcd(0,35)=35=N，应判 no-fault。
	r := analyzer.AnalyzePair(bi(1), bi(1), bi(1))
	if r.Status != PairNoFault {
		t.Fatalf("identical valid signatures must be no-fault, got %s: %s", r.Status, r.Reason)
	}

	// m=1, s_bad=2：v=32，gcd(31,35)=1；签名差 gcd(1-2,35)=gcd(34,35)=1，
	// 两方法都拿不到非平凡因子，必须是模型违背而非成功。
	r2 := analyzer.AnalyzePair(bi(1), bi(1), bi(2))
	if r2.Status != PairModelViolation || r2.Structure != nil {
		t.Fatalf("trivial gcds must be model violation, got %s", r2.Status)
	}

	// 越界入参直接拒绝。
	if r3 := analyzer.AnalyzePair(bi(1), bi(35), bi(2)); r3.Status != PairInvalid {
		t.Fatalf("out-of-range s_good must be invalid, got %s", r3.Status)
	}
}

func TestSampleKeyNilBranch(t *testing.T) {
	if got := sampleKey(Sample{M: nil, S: bi(1)}); got != "nil" {
		t.Fatalf("nil component key = %q, want nil", got)
	}
}
