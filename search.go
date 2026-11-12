package netsec

import (
	"errors"
	"fmt"
	"math/big"
)

// ErrNoSequence 表示在 MaxDepth 内没有任何观察序列使 MAP 后验达到阈值。
var ErrNoSequence = errors.New("netsec: no sequence reaches the threshold within MaxDepth")

// Sequence 是枚举产生的一个观察序列及其滤波结果。
type Sequence struct {
	Observations []Observation
	Depth        int
	Result       *Result
}

// symbols 返回枚举用的观察符号：先放丢失符号（Length=0, Lost=true），
// 再按字母表升序放未丢失符号，保证全序列枚举顺序确定。
func (c *Compiled) symbols() []Observation {
	out := make([]Observation, 0, len(c.alphabet)+1)
	out = append(out, Observation{Lost: true})
	for _, head := range c.alphabet {
		out = append(out, Observation{Length: head, Lost: false})
	}
	return out
}

// Walk 按深度优先边界（先深度 1 再深度 2，……）做全序列枚举，对每个
// 长度 1..maxDepth 的观察序列调用 visit。每个深度内部按枚举符号字典序。
//
// 枚举符号包含一个丢失符号和字母表中的每个可观察长度，因此同时覆盖
// “填充后的长度观察”与“消息丢失”两种语义。visit 返回错误时枚举立即停止
// 并透传该错误；maxDepth 必须在 [1, MaxDepth] 内。
func Walk(c *Compiled, maxDepth int, visit func(Sequence) error) error {
	if c == nil {
		return errors.New("netsec: nil model")
	}
	if maxDepth < 1 || maxDepth > MaxDepth {
		return fmt.Errorf("netsec: maxDepth %d out of range [1,%d]", maxDepth, MaxDepth)
	}

	type node struct {
		seq   []Observation
		alpha []*big.Rat
	}

	symbols := c.symbols()
	frontier := []node{{seq: []Observation{}, alpha: cloneRats(c.prior)}}

	for depth := 1; depth <= maxDepth; depth++ {
		next := make([]node, 0, len(frontier)*len(symbols))
		for _, cur := range frontier {
			for _, obs := range symbols {
				childAlpha := c.step(cur.alpha, obs, depth == 1)
				childSeq := append(append([]Observation{}, cur.seq...), obs)
				res := c.resultFrom(childAlpha)
				if err := visit(Sequence{
					Observations: childSeq,
					Depth:        depth,
					Result:       res,
				}); err != nil {
					return err
				}
				next = append(next, node{seq: childSeq, alpha: childAlpha})
			}
		}
		frontier = next
	}
	return nil
}

// Enumerate 返回全部长度 1..maxDepth 的观察序列及其滤波结果，按深度与
// 字典序排列。序列数量为 (|字母|+1)+...+(|字母|+1)^maxDepth。
func Enumerate(c *Compiled, maxDepth int) ([]Sequence, error) {
	var out []Sequence
	err := Walk(c, maxDepth, func(s Sequence) error {
		out = append(out, s)
		return nil
	})
	return out, err
}

// Shortest 以阈值 threshold 做精确比较，返回 MAP 后验 >= threshold 的
// 最短观察序列。同深度下返回枚举字典序的第一个；MaxDepth 内不存在时
// 返回 ErrNoSequence。
//
// threshold 必须是 [0,1] 内的有理数。不可能发生的序列不参与命中判定。
func Shortest(c *Compiled, threshold *big.Rat) (*Sequence, error) {
	if c == nil {
		return nil, errors.New("netsec: nil model")
	}
	if threshold == nil || threshold.Sign() < 0 || threshold.Cmp(big.NewRat(1, 1)) > 0 {
		return nil, errors.New("netsec: threshold must be a rational in [0,1]")
	}

	var hit *Sequence
	err := Walk(c, MaxDepth, func(s Sequence) error {
		if s.Result.Possible && s.Result.MAPPosterior.Cmp(threshold) >= 0 {
			cp := s
			hit = &cp
			return errStop
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStop) {
		return nil, err
	}
	if hit == nil {
		return nil, ErrNoSequence
	}
	return hit, nil
}

// errStop 是 Walk 内部提前终止的哨兵，不向调用方暴露。
var errStop = errors.New("netsec: enumeration stopped")

// step 从当前前向向量推进一个时刻；first 为真时当前向量是先验（X_1），
// 只乘发射不做转移。丢失观察的发射因子为 1。
func (c *Compiled) step(alpha []*big.Rat, obs Observation, first bool) []*big.Rat {
	var sym int
	if !obs.Lost {
		// Walk 只产出字母表内符号，这里必然命中。
		sym = c.index[c.blockHead(obs.Length)]
	}
	if first {
		out := cloneRats(alpha)
		if !obs.Lost {
			for i := range out {
				out[i].Mul(out[i], c.emissions[i][sym])
			}
		}
		return out
	}
	next := make([]*big.Rat, c.n)
	for j := range next {
		next[j] = new(big.Rat)
	}
	for i := 0; i < c.n; i++ {
		if alpha[i].Sign() == 0 {
			continue
		}
		weight := alpha[i]
		if !obs.Lost {
			// 先在当前状态上乘以发射概率，再按转移扩散。
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
	return next
}

func cloneRats(in []*big.Rat) []*big.Rat {
	out := make([]*big.Rat, len(in))
	for i, v := range in {
		out[i] = new(big.Rat).Set(v)
	}
	return out
}
