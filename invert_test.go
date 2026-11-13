package netsec

import (
	"math/big"
	"strings"
	"testing"
)

// oddPrimesUpTo 返回不超过 n 的全部奇素数。
func oddPrimesUpTo(n uint64) []uint64 {
	var primes []uint64
	for c := uint64(3); c <= n; c += 2 {
		isPrime := true
		for d := uint64(3); d*d <= c; d += 2 {
			if c%d == 0 {
				isPrime = false
				break
			}
		}
		if isPrime {
			primes = append(primes, c)
		}
	}
	return primes
}

func bigInv(x, p uint64) uint64 {
	r := new(big.Int).ModInverse(new(big.Int).SetUint64(x), new(big.Int).SetUint64(p))
	if r == nil {
		panic("no inverse")
	}
	return r.Uint64()
}

// TestVulnerableExhaustive 对 p≤257 的所有奇素数与全部 x 核对：
// 逆元正确、轨迹字母合法、独立重放通过、预算恰少一步时失败。
func TestVulnerableExhaustive(t *testing.T) {
	for _, p := range oddPrimesUpTo(257) {
		budget := Rounds(p)
		for x := uint64(1); x < p; x++ {
			inv, trace, err := VulnerableInvert(x, p, budget)
			if err != nil {
				t.Fatalf("p=%d x=%d: unexpected error %v", p, x, err)
			}
			if (inv*x)%p != 1 {
				t.Fatalf("p=%d x=%d: bad inverse %d", p, x, inv)
			}
			if inv != bigInv(x, p) {
				t.Fatalf("p=%d x=%d: inverse %d != big.Int reference %d", p, x, inv, bigInv(x, p))
			}
			for i := 0; i < len(trace); i++ {
				switch trace[i] {
				case StepA, StepB, StepC, StepD:
				default:
					t.Fatalf("p=%d x=%d: invalid trace symbol %q", p, x, trace[i])
				}
			}
			if err := VerifyTrace(x, p, inv, trace, budget); err != nil {
				t.Fatalf("p=%d x=%d: verify failed: %v", p, x, err)
			}
			// 预算恰少一步必须失败（轨迹至少一步；p≥3 时 x≥1 均非平凡）。
			if len(trace) > 0 {
				if _, _, err := VulnerableInvert(x, p, len(trace)-1); err != ErrBudget {
					t.Fatalf("p=%d x=%d: budget %d should fail with ErrBudget, got %v", p, x, len(trace)-1, err)
				}
			}
		}
	}
}

// TestVerifyTraceRejectsTampering 核对重放器能发现篡改。
func TestVerifyTraceRejectsTampering(t *testing.T) {
	const p = 257
	for _, x := range []uint64{1, 2, 3, 100, 255, 256} {
		inv, trace, err := VulnerableInvert(x, p, Rounds(p))
		if err != nil {
			t.Fatalf("x=%d: %v", x, err)
		}
		if err := VerifyTrace(x, p, inv, trace, Rounds(p)); err != nil {
			t.Fatalf("x=%d: honest trace rejected: %v", x, err)
		}
		// 错误逆元。
		if err := VerifyTrace(x, p, inv%p+1, trace, Rounds(p)); err == nil {
			t.Fatalf("x=%d: wrong inverse accepted", x)
		}
		// 非法符号。
		if err := VerifyTrace(x, p, inv, "Z"+trace, Rounds(p)); err == nil {
			t.Fatalf("x=%d: invalid symbol accepted", x)
		}
		// 翻转一个分支符号（若轨迹非空）。
		if len(trace) > 0 {
			flipped := []byte(trace)
			if flipped[0] == StepA {
				flipped[0] = StepB
			} else {
				flipped[0] = StepA
			}
			if err := VerifyTrace(x, p, inv, string(flipped), Rounds(p)); err == nil {
				t.Fatalf("x=%d: tampered trace accepted", x)
			}
		}
		// 截断轨迹（未收敛）。
		if len(trace) > 1 {
			if err := VerifyTrace(x, p, inv, trace[:len(trace)-1], Rounds(p)); err == nil {
				t.Fatalf("x=%d: truncated trace accepted", x)
			}
		}
	}
}

// TestHardenedExhaustive 对 p≤257 穷举核对加固回调：
// 结果与大整数参考一致、轨迹为固定轮数统一符号、与待审回调结果相同。
func TestHardenedExhaustive(t *testing.T) {
	for _, p := range oddPrimesUpTo(257) {
		rounds := Rounds(p)
		for x := uint64(1); x < p; x++ {
			inv, trace, err := HardenedInvert(x, p)
			if err != nil {
				t.Fatalf("p=%d x=%d: %v", p, x, err)
			}
			if inv != bigInv(x, p) {
				t.Fatalf("p=%d x=%d: hardened inverse %d != reference %d", p, x, inv, bigInv(x, p))
			}
			if len(trace) != rounds {
				t.Fatalf("p=%d x=%d: trace length %d != fixed rounds %d", p, x, len(trace), rounds)
			}
			if strings.Trim(trace, string(UniformSymbol)) != "" {
				t.Fatalf("p=%d x=%d: trace contains non-uniform symbols: %q", p, x, trace)
			}
			vinv, _, err := VulnerableInvert(x, p, rounds)
			if err != nil {
				t.Fatalf("p=%d x=%d: vulnerable callback: %v", p, x, err)
			}
			if inv != vinv {
				t.Fatalf("p=%d x=%d: hardened %d != vulnerable %d", p, x, inv, vinv)
			}
		}
	}
}

// TestAnalyzeExhaustive 核对候选分区、最短区分前缀与最大未收敛输入。
func TestAnalyzeExhaustive(t *testing.T) {
	for _, p := range oddPrimesUpTo(257) {
		rep, err := Analyze(p)
		if err != nil {
			t.Fatalf("p=%d: %v", p, err)
		}
		// 无未收敛输入，且最长轨迹不超过固定轮数预算。
		if len(rep.Unconverged) != 0 {
			t.Fatalf("p=%d: unconverged inputs %v", p, rep.Unconverged)
		}
		if rep.MaxSteps > Rounds(p) {
			t.Fatalf("p=%d: max steps %d exceeds budget %d", p, rep.MaxSteps, Rounds(p))
		}
		// 分组构成 [1, p-1] 的划分，且组内轨迹一致。
		seen := make(map[uint64]string, p-1)
		traces := make(map[uint64]string, p-1)
		for x := uint64(1); x < p; x++ {
			_, tr, err := VulnerableInvert(x, p, Rounds(p))
			if err != nil {
				t.Fatalf("p=%d x=%d: %v", p, x, err)
			}
			traces[x] = tr
		}
		for _, g := range rep.Groups {
			for _, m := range g.Members {
				if _, dup := seen[m]; dup {
					t.Fatalf("p=%d: x=%d appears in two groups", p, m)
				}
				seen[m] = g.Trace
				if traces[m] != g.Trace {
					t.Fatalf("p=%d: group trace mismatch for x=%d", p, m)
				}
			}
		}
		if len(seen) != int(p-1) {
			t.Fatalf("p=%d: groups cover %d secrets, want %d", p, len(seen), p-1)
		}
		// 同组 iff 轨迹相同。
		for x := uint64(1); x < p; x++ {
			if seen[x] != traces[x] {
				t.Fatalf("p=%d x=%d: partition inconsistent", p, x)
			}
		}
		// 暴力复核最短区分前缀。
		wantMin := 0
		var pair [2]uint64
		for a := uint64(1); a < p; a++ {
			for b := a + 1; b < p; b++ {
				if traces[a] == traces[b] {
					continue
				}
				l := commonPrefixLen(traces[a], traces[b]) + 1
				if wantMin == 0 || l < wantMin {
					wantMin = l
					pair = [2]uint64{a, b}
				}
			}
		}
		if rep.MinPrefixLen != wantMin {
			t.Fatalf("p=%d: min distinguishing prefix %d, want %d", p, rep.MinPrefixLen, wantMin)
		}
		// 报告的前缀对确实在该长度处可分。
		ta, tb := traces[rep.PrefixPair[0]], traces[rep.PrefixPair[1]]
		if commonPrefixLen(ta, tb)+1 != rep.MinPrefixLen {
			t.Fatalf("p=%d: reported pair %v not distinguished at prefix %d", p, rep.PrefixPair, rep.MinPrefixLen)
		}
		if ta[:rep.MinPrefixLen-1] != rep.Prefix {
			t.Fatalf("p=%d: reported prefix %q inconsistent", p, rep.Prefix)
		}
		_ = pair
		t.Logf("p=%d groups=%d maxSteps=%d budget=%d minPrefix=%d pair=%v",
			p, len(rep.Groups), rep.MaxSteps, Rounds(p), rep.MinPrefixLen, rep.PrefixPair)
	}
}

// TestHardenedRejectsInvalidParams 核对参数校验。
func TestHardenedRejectsInvalidParams(t *testing.T) {
	if _, _, err := HardenedInvert(0, 257); err == nil {
		t.Fatal("x=0 accepted")
	}
	if _, _, err := HardenedInvert(257, 257); err == nil {
		t.Fatal("x=p accepted")
	}
	if _, _, err := HardenedInvert(1, 256); err == nil {
		t.Fatal("even p accepted")
	}
	if _, _, err := HardenedInvert(1, MaxModulus+2); err == nil {
		t.Fatal("oversized p accepted")
	}
	if _, _, err := VulnerableInvert(1, 2, 10); err == nil {
		t.Fatal("p=2 accepted")
	}
}
