package netsec

import (
	"errors"
	"fmt"
	"math/big"
)

// Observation 是一个时刻的密文长度观察及其显式丢失掩码。
//
// Lost 为真时表示该时刻消息丢失，Length 被忽略：由于丢失与隐状态独立，
// 该时刻对全部发射长度边缘化，发射因子恒为 1。Lost 为假时 Length 必须
// 落在模型字母表（填充聚合后）中。
type Observation struct {
	Length int
	Lost   bool
}

// Result 是一个观察序列的滤波（前向）推断结果。
type Result struct {
	// Posterior[i] 是给定完整观察序列后末时刻动作 i 的后验概率。
	// 后验总和为 1；当序列不可能发生时切片为空。
	Posterior []*big.Rat

	// Compatible 是后验严格为正的动作下标，按升序排列，即与观察兼容的动作。
	Compatible []int

	// MAP 是后验最大的动作下标；并列时取下标最小者。
	MAP int

	// MAPPosterior 是 MAP 动作的后验值。
	MAPPosterior *big.Rat

	// Likelihood 是观察序列的全联合似然 P(o_1,...,o_T)，
	// 已对全部隐状态路径求和；不可能序列为 0。
	Likelihood *big.Rat

	// Possible 报告序列是否具有正似然。
	Possible bool
}

// Filter 对观察序列执行精确有理前向滤波。
//
// 空序列返回先验分布。序列长度超过 MaxDepth、模型为空或观察到字母表外的
// （未丢失）长度时返回错误。
func Filter(c *Compiled, seq []Observation) (*Result, error) {
	if c == nil {
		return nil, errors.New("netsec: nil model")
	}
	if len(seq) > MaxDepth {
		return nil, fmt.Errorf("netsec: sequence depth %d exceeds MaxDepth %d", len(seq), MaxDepth)
	}

	alpha := make([]*big.Rat, c.n)
	for i := range alpha {
		alpha[i] = new(big.Rat).Set(c.prior[i])
	}

	for t, obs := range seq {
		var sym int
		if !obs.Lost {
			k, ok := c.index[c.blockHead(obs.Length)]
			if !ok {
				return nil, fmt.Errorf("netsec: observation at t=%d length %d is outside the emission alphabet", t, obs.Length)
			}
			sym = k
		}
		// 约定：先验是 X_1 的分布。首时刻只在初始状态上发射；
		// 之后每个时刻在当前状态上发射，再按转移扩散到下一状态。
		next := make([]*big.Rat, c.n)
		for j := range next {
			next[j] = new(big.Rat)
		}
		if t == 0 {
			for i := 0; i < c.n; i++ {
				if obs.Lost {
					next[i].Set(alpha[i])
					continue
				}
				next[i].Mul(alpha[i], c.emissions[i][sym])
			}
			alpha = next
			continue
		}
		for i := 0; i < c.n; i++ {
			if alpha[i].Sign() == 0 {
				continue
			}
			weight := alpha[i]
			if !obs.Lost {
				weight = new(big.Rat).Mul(weight, c.emissions[i][sym])
				if weight.Sign() == 0 {
					continue
				}
			}
			// 丢失时发射因子为 1（对全部发射长度边缘化）。
			for j := 0; j < c.n; j++ {
				next[j].Add(next[j], new(big.Rat).Mul(weight, c.trans[i][j]))
			}
		}
		alpha = next
	}

	return c.resultFrom(alpha), nil
}

// resultFrom 把未归一化的前向向量 alpha 归一化为后验并汇总兼容动作与 MAP。
func (c *Compiled) resultFrom(alpha []*big.Rat) *Result {
	likelihood := new(big.Rat)
	for _, v := range alpha {
		likelihood.Add(likelihood, v)
	}

	res := &Result{
		Posterior:    make([]*big.Rat, c.n),
		Likelihood:   likelihood,
		Possible:     likelihood.Sign() > 0,
		MAP:          0,
		MAPPosterior: new(big.Rat),
	}

	if !res.Possible {
		res.Posterior = []*big.Rat{}
		res.Compatible = []int{}
		return res
	}

	for i, v := range alpha {
		post := new(big.Rat).Quo(v, likelihood)
		res.Posterior[i] = post
		if post.Sign() > 0 {
			res.Compatible = append(res.Compatible, i)
		}
		if post.Cmp(res.MAPPosterior) > 0 {
			res.MAPPosterior.Set(post)
			res.MAP = i
		}
	}
	return res
}
