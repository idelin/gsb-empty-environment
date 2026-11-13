package netsec

import (
	"math/big"
	"testing"
)

// smallPrimes 覆盖穷举使用的小奇素数。
var smallPrimes = []int64{3, 5, 7, 11, 13, 17, 19, 23}

// smallExponents 是常见小公开指数，测试中按 gcd(e,(p-1)(q-1))=1 过滤。
var smallExponents = []int64{3, 5, 7, 11, 13, 17, 23, 257}

// unfactorableRecord 保留无法注入或无法分解的故障证据。
type unfactorableRecord struct {
	p, q, e int64
	m       int64
	branch  Branch
	bit     uint
	reason  string
}

// TestExhaustiveSmallRSA 穷举小素数对、公开指数、与 N 互素的消息，以及
// p/q 两个分支上所有合法故障位。对每一次单分支单比特故障：
//   - 两种 gcd 方法必须一致，且返回因子真实整除 N、并等于被注入分支的模数；
//   - 正常签名必须验签；
//   - 越界故障位与非互素消息被拒绝，全部作为证据保留。
func TestExhaustiveSmallRSA(t *testing.T) {
	if testing.Short() {
		t.Skip("exhaustive fault sweep skipped in -short mode")
	}

	var rejected, factored, trials int
	var failures []unfactorableRecord

	for i := 0; i < len(smallPrimes); i++ {
		for j := i + 1; j < len(smallPrimes); j++ {
			p, q := smallPrimes[i], smallPrimes[j]
			n := p * q
			for _, e := range smallExponents {
				signer, err := NewCRTSigner(bi(p), bi(q), bi(e))
				if err != nil {
					continue // e 与 phi 不互素，换一个指数
				}
				analyzer, err := NewAnalyzer(bi(n), bi(e), nil)
				if err != nil {
					t.Fatalf("NewAnalyzer(%d,%d): %v", n, e, err)
				}

				for m := int64(1); m < n; m++ {
					mBig := bi(m)
					if new(big.Int).GCD(nil, nil, mBig, bi(n)).Cmp(one) != 0 {
						// 非互素消息必须被拒绝并保留证据，不得造成假成功。
						r := analyzer.AnalyzeSample(Sample{M: mBig, S: bi(0)})
						if r.Status != StatusInvalid {
							t.Fatalf("non-coprime m=%d for N=%d not rejected", m, n)
						}
						rejected++
						continue
					}

					good, err := signer.Sign(mBig)
					if err != nil {
						t.Fatalf("Sign: %v", err)
					}
					if verify := new(big.Int).Exp(good, bi(e), bi(n)); verify.Cmp(mBig) != 0 {
						t.Fatalf("good signature failed to verify")
					}

					sweepBranch(t, signer, analyzer, p, q, e, m, BranchP, good, &trials, &factored, &failures)
					sweepBranch(t, signer, analyzer, p, q, e, m, BranchQ, good, &trials, &factored, &failures)
				}
			}
		}
	}

	if factored == 0 {
		t.Fatalf("exhaustive sweep produced no factored sample")
	}
	if trials != factored {
		t.Fatalf("every in-range single-branch fault must factor: trials=%d factored=%d, failures=%d",
			trials, factored, len(failures))
	}
	t.Logf("sweep done: trials=%d factored=%d rejected(non-coprime)=%d retained-fault-records=%d",
		trials, factored, rejected, len(failures))

	// 保留至少一份“无法注入”的越界故障证据，证明该路径被显式记录而非静默吞掉。
	if len(failures) == 0 {
		t.Fatalf("expected retained evidence for out-of-range fault positions")
	}
}

func sweepBranch(
	t *testing.T,
	signer *CRTSigner,
	analyzer *Analyzer,
	p, q, e, m int64,
	branch Branch,
	good *big.Int,
	trials, factored *int,
	failures *[]unfactorableRecord,
) {
	t.Helper()
	// Bellcore 模型：故障分支的余数被破坏，另一分支仍满足 s^e ≡ m。
	// 因此 gcd(v-m, N) 暴露的是“未受影响”分支的模数：
	// p 支故障 => 因子 q；q 支故障 => 因子 p。
	modulus := p        // 被注入分支的模数，决定合法故障位范围
	expectedFactor := q // 未受影响分支的模数，即 gcd 应暴露的因子
	if branch == BranchQ {
		modulus = q
		expectedFactor = p
	}
	mBig := bi(m)

	// 位宽多探一位：最后一位必然越界，用于保留无法注入的证据。
	for bit := uint(0); bit <= uint(bi(modulus).BitLen()); bit++ {
		bad, err := signer.SignWithBitFault(mBig, branch, bit)
		if err != nil {
			if err != ErrFaultPosition {
				t.Fatalf("unexpected signer error: %v", err)
			}
			*failures = append(*failures, unfactorableRecord{
				p: p, q: q, e: e, m: m, branch: branch, bit: bit, reason: err.Error(),
			})
			continue
		}

		*trials++
		sample := Sample{M: mBig, S: bad}
		result := analyzer.AnalyzeSample(sample)
		if result.Status != StatusFactored {
			*failures = append(*failures, unfactorableRecord{
				p: p, q: q, e: e, m: m, branch: branch, bit: bit,
				reason: "unfactorable: " + result.Reason,
			})
			continue
		}

		// 所有返回因子必须真实整除 N。
		structure := result.Factors
		if !structure.ProductIsN || new(big.Int).Mod(signer.N(), structure.G).Sign() != 0 {
			t.Fatalf("returned factor does not divide N")
		}
		// 单分支故障下 gcd 必须恰好等于未受影响分支的模数。
		if structure.G.Cmp(bi(expectedFactor)) != 0 {
			t.Fatalf("branch %s fault gave factor %v, want %d", branch, structure.G, expectedFactor)
		}

		pair := analyzer.AnalyzePair(mBig, good, bad)
		if pair.Status != PairAgree {
			t.Fatalf("methods disagree on single-branch fault: %s (%s)", pair.Status, pair.Reason)
		}
		if pair.GCDValue.Cmp(pair.GCDSignature) != 0 || pair.GCDValue.Cmp(bi(expectedFactor)) != 0 {
			t.Fatalf("pair gcd mismatch: value=%v sig=%v want=%d",
				pair.GCDValue, pair.GCDSignature, expectedFactor)
		}
		*factored++
	}
}

// TestExhaustiveEvidenceReplay 抽样回放一份被保留的越界证据，确认错误稳定可复现。
func TestExhaustiveEvidenceReplay(t *testing.T) {
	signer := mustSigner(t, 3, 5, 3)
	// p=3 位宽为 2，合法位 0、1；位 2 越界。
	if _, err := signer.SignWithBitFault(bi(1), BranchP, 2); err != ErrFaultPosition {
		t.Fatalf("expected ErrFaultPosition, got %v", err)
	}
}
