package netsec

import "math/big"

// PairStatus 描述一份正常签名与一份故障签名的联合分析结论。
type PairStatus int

const (
	// PairInvalid 表示输入不满足前置条件或并非同一消息的签名对。
	PairInvalid PairStatus = iota
	// PairNoFault 表示两份输入都能正常验签，没有故障可分析。
	PairNoFault
	// PairAgree 表示 v 方法与签名差 gcd 方法得到同一个非平凡因子，符合单分支 CRT 故障模型。
	PairAgree
	// PairModelViolation 表示两种方法结论不一致，输入不符合单分支 CRT 故障模型。
	PairModelViolation
)

// String 返回配对状态的可读描述。
func (p PairStatus) String() string {
	switch p {
	case PairInvalid:
		return "invalid"
	case PairNoFault:
		return "no-fault"
	case PairAgree:
		return "agree"
	case PairModelViolation:
		return "model-violation"
	default:
		return "unknown"
	}
}

// PairResult 是 AnalyzePair 的完整结论，保留两种方法各自的 gcd 证据。
type PairResult struct {
	Status PairStatus
	Reason string

	// VBad 是故障签名的重算值 s_bad^e mod N。
	VBad *big.Int
	// GCDValue 是方法一：gcd(v_bad - m mod N, N)。
	GCDValue *big.Int
	// GCDSignature 是方法二：gcd(s_good - s_bad mod N, N)。
	GCDSignature *big.Int

	// Structure 仅在 PairAgree 时非空。
	Structure *FactorStructure
}

// AnalyzePair 对同一消息的正常签名 sGood 与故障签名 sBad 做交叉分析。
// 方法一由 v_bad 与消息的差求 gcd；方法二直接由两份签名的差求 gcd。
// 单分支 CRT 故障模型下两者必须给出同一个非平凡因子，否则明确报告模型违背，
// 绝不把不一致结果包装成成功分解。
func (a *Analyzer) AnalyzePair(m, sGood, sBad *big.Int) PairResult {
	if !a.inRange(m) || !a.inRange(sGood) || !a.inRange(sBad) {
		return PairResult{Status: PairInvalid, Reason: "m, s_good and s_bad must be non-negative and smaller than N"}
	}
	if a.gcdN(m).Cmp(one) != 0 {
		return PairResult{Status: PairInvalid, Reason: "gcd(m, N) != 1"}
	}

	vGood := new(big.Int).Exp(sGood, a.e, a.n)
	if vGood.Cmp(m) != 0 {
		return PairResult{Status: PairInvalid, Reason: "s_good does not verify against m"}
	}
	vBad := new(big.Int).Exp(sBad, a.e, a.n)

	valueDiff := new(big.Int).Sub(vBad, m)
	valueDiff.Mod(valueDiff, a.n)
	gValue := a.gcdN(valueDiff)

	sigDiff := new(big.Int).Sub(sGood, sBad)
	sigDiff.Mod(sigDiff, a.n)
	gSig := a.gcdN(sigDiff)

	result := PairResult{
		VBad:         vBad,
		GCDValue:     gValue,
		GCDSignature: gSig,
	}

	if vBad.Cmp(m) == 0 {
		// 故障签名同样验签：没有故障。若签名差 gcd 却泄漏因子，属于模型外输入。
		if gSig.Cmp(one) > 0 && gSig.Cmp(a.n) < 0 {
			result.Status = PairModelViolation
			result.Reason = "s_bad verifies but gcd(s_good-s_bad, N) is non-trivial"
			return result
		}
		result.Status = PairNoFault
		result.Reason = "both signatures verify"
		return result
	}

	valueOK := gValue.Cmp(one) > 0 && gValue.Cmp(a.n) < 0
	sigOK := gSig.Cmp(one) > 0 && gSig.Cmp(a.n) < 0

	switch {
	case valueOK && sigOK && gValue.Cmp(gSig) == 0:
		result.Status = PairAgree
		result.Structure = a.describeFactors(gValue)
	case valueOK || sigOK:
		result.Status = PairModelViolation
		result.Reason = "the two gcd methods disagree, input is not a single-branch CRT fault"
	default:
		result.Status = PairModelViolation
		result.Reason = "neither gcd method yields a non-trivial factor"
	}
	return result
}
