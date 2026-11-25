package identity

import (
	"errors"
	"fmt"
	"sort"
)

// VerifyWitness 复核一条可达事实的合法前缀（最短前缀或调用方自己构造的前缀）。
// prefix 必须以 readID 对应的受保护读取收尾，复放结果必须等于 outcome；绑定
// 到其他读取或含非法事件的证明会被拒绝。穷尽且存在完整合法历史时，还要求
// 前缀所在状态可扩展为完整合法历史。
func (r *AnalysisResult) VerifyWitness(readID int, outcome Outcome, prefix []int) error {
	if _, err := r.lookupRead(readID); err != nil {
		return err
	}
	if len(prefix) == 0 || prefix[len(prefix)-1] != readID {
		return fmt.Errorf("identity: witness is not bound to protected read %d", readID)
	}
	index := map[int]int{}
	for i, e := range r.input.Events {
		index[e.ID] = i
	}
	st := freshState()
	seen := map[int]bool{}
	got := Outcome(255)
	for _, id := range prefix {
		idx, ok := index[id]
		if !ok {
			return fmt.Errorf("identity: witness references unknown event %d", id)
		}
		if seen[id] {
			return fmt.Errorf("identity: witness replays event %d twice", id)
		}
		seen[id] = true
		next, reason, o, legal := step(st, idx, r.input.Events[idx])
		if !legal {
			return fmt.Errorf("identity: witness prefix is illegal at event %d: %s", id, reason)
		}
		st = next
		got = o
	}
	if got != outcome {
		return fmt.Errorf("identity: witness outcome %v does not match claimed %v", got, outcome)
	}
	if r.Exhaustive && r.HasLegalHistory {
		st.fp = fingerprint(st)
		st.hash = stateHash(st)
		if _, ok := r.liveFps[st.fp]; !ok {
			return errors.New("identity: witness state cannot be extended to a full legal history")
		}
	}
	return nil
}

// VerifyCoverage 独立复核覆盖证明。targetReadID 是调用方要求保护的读取：
// 证书必须严格绑定到该读取，绑定到其他读取（即使其本身有效）也会被拒绝。
// 函数不信任传入证书，而是用相同输入与预算重跑一次前向不动点，再核对模型、
// 穷尽性、结论 outcome 以及覆盖状态集合的完备性。
func (r *AnalysisResult) VerifyCoverage(targetReadID int, cert *CoverageCertificate) error {
	if cert == nil {
		return errors.New("identity: nil coverage certificate")
	}
	if cert.ModelFingerprint != r.ModelFingerprint {
		return errors.New("identity: certificate is bound to a different model")
	}
	if _, err := r.lookupRead(targetReadID); err != nil {
		return err
	}
	if cert.ReadID != targetReadID {
		return fmt.Errorf("identity: certificate is bound to read %d, not target read %d", cert.ReadID, targetReadID)
	}
	if !cert.Exhaustive || !r.Exhaustive || !r.HasLegalHistory {
		return errors.New("identity: coverage requires an exhaustive analysis with at least one legal history")
	}

	independent, err := Analyze(r.input, r.budget)
	if err != nil {
		return fmt.Errorf("identity: independent reanalysis failed: %w", err)
	}
	if !independent.Exhaustive || !independent.HasLegalHistory {
		return errors.New("identity: independent reanalysis was not exhaustive")
	}
	rr, ok := independent.Reads[cert.ReadID]
	if !ok {
		return errors.New("identity: certificate target is not a protected read")
	}
	wantClass := AllConform
	if cert.Outcome == Violation {
		wantClass = AllViolate
	}
	if rr.Class != wantClass {
		return fmt.Errorf("identity: independent reanalysis classifies read %d as %v, not %v", cert.ReadID, rr.Class, wantClass)
	}

	expected := independent.Coverage[cert.ReadID]
	if expected == nil {
		return errors.New("identity: independent reanalysis produced no coverage for the target read")
	}
	if expected.Outcome != cert.Outcome {
		return errors.New("identity: certificate outcome does not match independent reanalysis")
	}
	if !sameStringSet(cert.StateFingerprints, expected.StateFingerprints) {
		return errors.New("identity: certificate state set is incomplete or contains foreign states")
	}
	return nil
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ca := append([]string(nil), a...)
	cb := append([]string(nil), b...)
	sort.Strings(ca)
	sort.Strings(cb)
	for i := range ca {
		if ca[i] != cb[i] {
			return false
		}
	}
	return true
}
