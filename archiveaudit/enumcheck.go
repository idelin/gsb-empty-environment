package main

import (
	"fmt"
	"io"
	"math/big"
)

// EnumResult 保存随机域枚举核对的结果。
type EnumResult struct {
	Total       *big.Int            // 总权重 W，即第一抽的随机域大小
	FirstCounts map[string]*big.Int // 第一抽中每个块在随机域中的命中次数，应恰等于其权重
	PairCounts  map[string]*big.Int // 有序对 "i->j" 的命中次数，应恰等于 w_i * w_j
	PairDomain  *big.Int            // 两抽的总随机域大小 sum_i w_i*(W-w_i)
	Depth       int                 // 实际枚举的深度（1 或 2）
}

// EnumCheck 对小归档枚举全部随机域，核对加权无放回抽样的概率。
//
// 第一抽的随机值均匀分布在 [0, W) 上，逐一枚举每个取值并统计命中块，
// 命中次数必须严格等于块权重（概率 = w_i / W）；
// 随机域足够小时继续枚举第二抽，有序对 (i,j) 的命中次数必须等于 w_i*w_j
// （即 P(先 i 后 j) = w_i/W * w_j/(W-w_i)）。
//
// 枚举与真实抽样共用同一个 selectIndex，保证核对的就是实际实现。
func EnumCheck(a *Archive, limit int64) (*EnumResult, error) {
	total := a.TotalLength()
	if !total.IsInt64() {
		return nil, fmt.Errorf("总逻辑长度 %s 超出 int64，归档过大，无法枚举随机域", total.String())
	}
	w := total.Int64()
	if w > limit {
		return nil, fmt.Errorf("随机域大小 %d 超过枚举上限 %d，归档过大", w, limit)
	}

	res := &EnumResult{
		Total:       new(big.Int).Set(total),
		FirstCounts: make(map[string]*big.Int),
		PairCounts:  make(map[string]*big.Int),
		Depth:       1,
	}

	// 深度 1：枚举第一抽随机域 [0, W)
	r := new(big.Int)
	for i := int64(0); i < w; i++ {
		r.SetInt64(i)
		idx := selectIndex(a.Blocks, r)
		id := a.Blocks[idx].ID
		if res.FirstCounts[id] == nil {
			res.FirstCounts[id] = new(big.Int)
		}
		res.FirstCounts[id].Add(res.FirstCounts[id], big.NewInt(1))
	}

	// 深度 2：随机域 sum_i w_i*(W-w_i) 不超过上限时枚举两抽
	pairDomain := int64(0)
	for _, b := range a.Blocks {
		wi := b.Length.Int64()
		pairDomain += wi * (w - wi)
	}
	if pairDomain <= limit {
		res.Depth = 2
		res.PairDomain = big.NewInt(pairDomain)
		r1 := new(big.Int)
		for i := int64(0); i < w; i++ {
			r1.SetInt64(i)
			idx1 := selectIndex(a.Blocks, r1)
			first := a.Blocks[idx1]
			// 无放回：移除第一抽命中的块后，第二抽随机域为 [0, W-w_i)
			rest := make([]*Block, 0, len(a.Blocks)-1)
			for j, b := range a.Blocks {
				if j != idx1 {
					rest = append(rest, b)
				}
			}
			wi := first.Length.Int64()
			r2 := new(big.Int)
			for v := int64(0); v < w-wi; v++ {
				r2.SetInt64(v)
				idx2 := selectIndex(rest, r2)
				key := first.ID + "->" + rest[idx2].ID
				if res.PairCounts[key] == nil {
					res.PairCounts[key] = new(big.Int)
				}
				res.PairCounts[key].Add(res.PairCounts[key], big.NewInt(1))
			}
		}
	}
	return res, nil
}

// Verify 检查枚举结果是否与理论概率一致，返回不一致之处的描述。
func (res *EnumResult) Verify(a *Archive) []string {
	var problems []string
	for _, b := range a.Blocks {
		c := res.FirstCounts[b.ID]
		if c == nil || c.Cmp(b.Length) != 0 {
			got := "0"
			if c != nil {
				got = c.String()
			}
			problems = append(problems, fmt.Sprintf(
				"块 %s 第一抽命中次数 %s，期望等于权重 %s", b.ID, got, b.Length.String()))
		}
	}
	if res.Depth >= 2 {
		for _, bi := range a.Blocks {
			for _, bj := range a.Blocks {
				if bi.ID == bj.ID {
					if c := res.PairCounts[bi.ID+"->"+bj.ID]; c != nil && c.Sign() != 0 {
						problems = append(problems, fmt.Sprintf(
							"有序对 %s->%s 出现 %s 次，无放回抽样中应为 0", bi.ID, bj.ID, c.String()))
					}
					continue
				}
				want := new(big.Int).Mul(bi.Length, bj.Length)
				got := res.PairCounts[bi.ID+"->"+bj.ID]
				if got == nil || got.Cmp(want) != 0 {
					g := "0"
					if got != nil {
						g = got.String()
					}
					problems = append(problems, fmt.Sprintf(
						"有序对 %s->%s 命中 %s 次，期望 %s (= w_i*w_j)", bi.ID, bj.ID, g, want.String()))
				}
			}
		}
	}
	return problems
}

// PrintReport 输出枚举核对报告。
func (res *EnumResult) PrintReport(a *Archive, out io.Writer) {
	total := a.TotalLength()
	fmt.Fprintf(out, "随机域枚举核对（深度 %d），总权重 W = %s\n", res.Depth, total.String())
	for _, b := range a.Blocks {
		c := res.FirstCounts[b.ID]
		p := new(big.Rat).SetFrac(b.Length, total)
		f, _ := p.Float64()
		fmt.Fprintf(out, "块 %s: 权重 %s，第一抽命中 %s 次，概率 %s/%s = %.6f\n",
			b.ID, b.Length.String(), c.String(), b.Length.String(), total.String(), f)
	}
	if res.Depth >= 2 {
		fmt.Fprintf(out, "两抽随机域大小 %s，有序对命中次数均等于 w_i*w_j（详见校验结果）\n",
			res.PairDomain.String())
	}
	problems := res.Verify(a)
	if len(problems) == 0 {
		fmt.Fprintln(out, "校验结果: 枚举核对通过，抽样概率与理论值完全一致。")
	} else {
		fmt.Fprintf(out, "校验结果: 发现 %d 处不一致:\n", len(problems))
		for _, p := range problems {
			fmt.Fprintf(out, "  - %s\n", p)
		}
	}
}
