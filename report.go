package netsec

import (
	"fmt"
	"math/big"
	"strings"
)

// Report bundles a best-trail search with an optional ground-truth
// verification for one chosen (input, output) difference pair.
//
// The two probability notions are deliberately kept separate:
//
//   - Search.Best.Score is the probability of ONE single trail (the
//     exact rational product of its active S-box transitions).
//   - TrueProb is the true differential probability obtained by full
//     plaintext enumeration; it merges the contributions of ALL trails
//     between the two differences for the specified keys.
//
// The single-trail probability must never be reported as the true
// combined probability.
type Report struct {
	Rounds  int
	Starts  []uint16
	Targets []uint16
	Search  *SearchResult

	Verified  bool
	Keys      []uint16
	InDiff    uint16
	OutDiff   uint16
	TrueCount uint32
	TrueProb  *big.Rat
}

// Analyze runs the best-trail search and, when roundKeys is non-nil,
// additionally verifies the true differential probability by exhaustive
// plaintext enumeration. The verified pair is the best trail's
// endpoints, or (Starts[0], Targets[0]) when no trail exists.
func Analyze(c *Cipher, req SearchRequest, roundKeys []uint16) (*Report, error) {
	res, err := c.SearchTrails(req)
	if err != nil {
		return nil, err
	}
	rep := &Report{
		Rounds:  c.rounds,
		Starts:  append([]uint16(nil), req.Starts...),
		Targets: append([]uint16(nil), req.Targets...),
		Search:  res,
	}
	if roundKeys == nil {
		return rep, nil
	}
	if len(roundKeys) != c.rounds {
		return nil, fmt.Errorf("netsec: need exactly %d round keys, got %d", c.rounds, len(roundKeys))
	}
	inDiff, outDiff := req.Starts[0], req.Targets[0]
	if res.Best != nil {
		inDiff = res.Best.Differences[0]
		outDiff = res.Best.Differences[len(res.Best.Differences)-1]
	}
	count, prob, err := c.CountDifferential(roundKeys, inDiff, outDiff)
	if err != nil {
		return nil, err
	}
	rep.Verified = true
	rep.Keys = append([]uint16(nil), roundKeys...)
	rep.InDiff, rep.OutDiff = inDiff, outDiff
	rep.TrueCount, rep.TrueProb = count, prob
	return rep, nil
}

func ratApprox(r *big.Rat) string {
	f, _ := new(big.Float).SetRat(r).Float64()
	return fmt.Sprintf("%.6g", f)
}

// String renders a human-readable report that explicitly separates the
// single-trail probability from the combined true probability.
func (r *Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "差分分析报告（%d 轮 16 位 SPN）\n", r.Rounds)
	fmt.Fprintf(&b, "起始差分集合: %v  目标差分集合: %v\n", r.Starts, r.Targets)
	b.WriteString("—— 单条轨迹（分支定界搜索）——\n")
	s := r.Search
	if s.Best == nil {
		if s.Optimal {
			b.WriteString("不存在连接两集合的差分轨迹（搜索已完备）。\n")
		} else {
			b.WriteString("预算内尚未找到轨迹，且搜索未完备，不能排除轨迹存在。\n")
		}
	} else {
		fmt.Fprintf(&b, "最佳单条轨迹差分序列: %v\n", s.Best.Differences)
		fmt.Fprintf(&b, "单条轨迹分数（精确有理数）: %s ≈ %s（等分轨迹数: %d）\n",
			s.Best.Score.RatString(), ratApprox(s.Best.Score), s.Ties)
	}
	fmt.Fprintf(&b, "展开节点: %d，剩余未排除节点: %d\n", s.Expanded, s.Remaining)
	if s.Optimal {
		b.WriteString("搜索状态: 已闭合，最佳轨迹分数即为最优。\n")
	} else {
		fmt.Fprintf(&b, "搜索状态: 预算耗尽，未闭合！尚未排除的概率上界: %s ≈ %s；最佳已知轨迹未达到该上界，不能称最优。\n",
			s.UpperBound.RatString(), ratApprox(s.UpperBound))
	}
	if r.Verified {
		b.WriteString("—— 真实差分概率（逐明文枚举复核）——\n")
		fmt.Fprintf(&b, "轮密钥: %v  输入差分: %d  输出差分: %d\n", r.Keys, r.InDiff, r.OutDiff)
		fmt.Fprintf(&b, "真实计数: %d/65536，真实概率: %s ≈ %s\n",
			r.TrueCount, r.TrueProb.RatString(), ratApprox(r.TrueProb))
	}
	b.WriteString("注意: 单条轨迹概率只是多条轨迹合并后的真实差分概率的一个组成部分（密钥平均意义下真实概率等于所有轨迹概率之和），两者不可混为一谈；真实概率以逐明文枚举结果为准。\n")
	return b.String()
}
