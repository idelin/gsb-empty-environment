// Package bayes 提供基于有理数的精确贝叶斯证据更新。
//
// 证据以“证据组”的形式加入：每组证据直接给出该组观测的联合似然
// P(E|H)，库不会把组内观测拆成独立事件相乘。后验用 math/big.Rat
// 精确计算；证据可按编号撤销；同一编号的证据重复加入只计一次；
// 若加入某组证据后总似然为零，则报告矛盾且状态不变。
package bayes

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
)

// Hypothesis 标识一个假设。
type Hypothesis string

// ErrContradiction 表示证据与先验矛盾：所有假设下的总似然为零。
var ErrContradiction = errors.New("bayes: 证据与先验矛盾（总似然为零）")

var one = big.NewRat(1, 1)

// Engine 维护先验、已接受的证据组，并计算精确后验。
type Engine struct {
	hypotheses []Hypothesis
	prior      map[Hypothesis]*big.Rat
	evidence   map[string]map[Hypothesis]*big.Rat
}

// New 以有理先验创建 Engine。先验必须非空、各项非负且总和恰为 1。
func New(prior map[Hypothesis]*big.Rat) (*Engine, error) {
	if len(prior) == 0 {
		return nil, errors.New("bayes: 先验不能为空")
	}
	sum := new(big.Rat)
	hs := make([]Hypothesis, 0, len(prior))
	for h, p := range prior {
		if p == nil {
			return nil, fmt.Errorf("bayes: 假设 %q 的先验为 nil", h)
		}
		if p.Sign() < 0 {
			return nil, fmt.Errorf("bayes: 假设 %q 的先验为负: %s", h, p)
		}
		sum.Add(sum, p)
		hs = append(hs, h)
	}
	if sum.Cmp(one) != 0 {
		return nil, fmt.Errorf("bayes: 先验总和为 %s，应为 1", sum)
	}
	sort.Slice(hs, func(i, j int) bool { return hs[i] < hs[j] })
	cp := make(map[Hypothesis]*big.Rat, len(prior))
	for h, p := range prior {
		cp[h] = new(big.Rat).Set(p)
	}
	return &Engine{
		hypotheses: hs,
		prior:      cp,
		evidence:   make(map[string]map[Hypothesis]*big.Rat),
	}, nil
}

// AddEvidence 加入一组证据。likelihood[h] 是该组观测在假设 h 下的
// 联合似然 P(E|H=h)，取值须在 [0,1] 且覆盖全部假设。
//
// 若 id 已存在，本次调用为空操作（重复证据只计一次），返回 (false, nil)。
// 若加入后总似然为零，返回 ErrContradiction，且引擎状态保持不变。
func (e *Engine) AddEvidence(id string, likelihood map[Hypothesis]*big.Rat) (bool, error) {
	if _, dup := e.evidence[id]; dup {
		return false, nil
	}
	lik := make(map[Hypothesis]*big.Rat, len(e.hypotheses))
	for _, h := range e.hypotheses {
		l, ok := likelihood[h]
		if !ok || l == nil {
			return false, fmt.Errorf("bayes: 证据 %q 缺少假设 %q 的似然", id, h)
		}
		if l.Sign() < 0 || l.Cmp(one) > 0 {
			return false, fmt.Errorf("bayes: 证据 %q 在假设 %q 下的似然 %s 不在 [0,1]", id, h, l)
		}
		lik[h] = new(big.Rat).Set(l)
	}
	if e.massWith(lik).Sign() == 0 {
		return false, ErrContradiction
	}
	e.evidence[id] = lik
	return true, nil
}

// RemoveEvidence 撤销一组证据，返回该证据是否存在。
func (e *Engine) RemoveEvidence(id string) bool {
	if _, ok := e.evidence[id]; !ok {
		return false
	}
	delete(e.evidence, id)
	return true
}

// HasEvidence 报告编号为 id 的证据是否已加入。
func (e *Engine) HasEvidence(id string) bool {
	_, ok := e.evidence[id]
	return ok
}

// jointWeights 返回未归一化的联合权重 P(h)·∏_e P(e|h)。
func (e *Engine) jointWeights() map[Hypothesis]*big.Rat {
	w := make(map[Hypothesis]*big.Rat, len(e.hypotheses))
	for _, h := range e.hypotheses {
		joint := new(big.Rat).Set(e.prior[h])
		for _, lik := range e.evidence {
			joint.Mul(joint, lik[h])
		}
		w[h] = joint
	}
	return w
}

// massWith 计算在现有证据基础上再加入 lik 后的总似然。
func (e *Engine) massWith(lik map[Hypothesis]*big.Rat) *big.Rat {
	total := new(big.Rat)
	for _, h := range e.hypotheses {
		joint := new(big.Rat).Set(e.prior[h])
		for _, ev := range e.evidence {
			joint.Mul(joint, ev[h])
		}
		joint.Mul(joint, lik[h])
		total.Add(total, joint)
	}
	return total
}

// Posterior 返回精确后验分布（各假设概率之和为 1）。
func (e *Engine) Posterior() map[Hypothesis]*big.Rat {
	w := e.jointWeights()
	total := new(big.Rat)
	for _, h := range e.hypotheses {
		total.Add(total, w[h])
	}
	post := make(map[Hypothesis]*big.Rat, len(e.hypotheses))
	for _, h := range e.hypotheses {
		post[h] = new(big.Rat).Quo(w[h], total)
	}
	return post
}

// PosteriorOf 返回单个假设的精确后验概率。
func (e *Engine) PosteriorOf(h Hypothesis) (*big.Rat, error) {
	if _, ok := e.prior[h]; !ok {
		return nil, fmt.Errorf("bayes: 未知假设 %q", h)
	}
	return e.Posterior()[h], nil
}

// Outcome 描述候选观测的一个可能结果：Prob[h] = P(该结果|h)。
type Outcome struct {
	Prob map[Hypothesis]*big.Rat
}

// Candidate 是一个候选新观测：在各假设下，其结果构成概率分布。
type Candidate struct {
	ID       string
	Outcomes []Outcome
}

// ExpectedError 计算在当前后验下观测 candidate 后的期望误判率。
//
// 采用联合概率枚举：对每个结果 o，联合概率 J(h,o)=P(h)P(o|h)，
// 最优判决取 argmax_h J(h,o)，误判贡献为 P(o)−max_h J(h,o)；
// 对所有结果求和即为期望误判率。
func (e *Engine) ExpectedError(c Candidate) (*big.Rat, error) {
	if len(c.Outcomes) == 0 {
		return nil, fmt.Errorf("bayes: 候选 %q 没有可能结果", c.ID)
	}
	for _, h := range e.hypotheses {
		sum := new(big.Rat)
		for _, o := range c.Outcomes {
			p, ok := o.Prob[h]
			if !ok || p == nil {
				return nil, fmt.Errorf("bayes: 候选 %q 缺少假设 %q 的结果概率", c.ID, h)
			}
			if p.Sign() < 0 || p.Cmp(one) > 0 {
				return nil, fmt.Errorf("bayes: 候选 %q 在假设 %q 下的结果概率 %s 不在 [0,1]", c.ID, h, p)
			}
			sum.Add(sum, p)
		}
		if sum.Cmp(one) != 0 {
			return nil, fmt.Errorf("bayes: 候选 %q 在假设 %q 下的结果概率之和为 %s，应为 1", c.ID, h, sum)
		}
	}
	post := e.Posterior()
	err := new(big.Rat)
	for _, o := range c.Outcomes {
		po := new(big.Rat)
		var maxJoint *big.Rat
		for _, h := range e.hypotheses {
			joint := new(big.Rat).Mul(post[h], o.Prob[h])
			po.Add(po, joint)
			if maxJoint == nil || joint.Cmp(maxJoint) > 0 {
				maxJoint = joint
			}
		}
		err.Add(err, po.Sub(po, maxJoint))
	}
	return err, nil
}

// BestCandidate 在候选观测中选择期望误判率最低的一项；
// 平局时按编号（字典序）取较小者。返回所选候选及其期望误判率。
func (e *Engine) BestCandidate(cands []Candidate) (Candidate, *big.Rat, error) {
	if len(cands) == 0 {
		return Candidate{}, nil, errors.New("bayes: 没有候选观测")
	}
	var best Candidate
	var bestErr *big.Rat
	for _, c := range cands {
		er, err := e.ExpectedError(c)
		if err != nil {
			return Candidate{}, nil, err
		}
		if bestErr == nil || er.Cmp(bestErr) < 0 ||
			(er.Cmp(bestErr) == 0 && c.ID < best.ID) {
			best, bestErr = c, er
		}
	}
	return best, bestErr, nil
}
