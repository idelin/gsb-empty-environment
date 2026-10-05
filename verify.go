package main

import (
	"fmt"
	"math/big"
)

// ---------- 有限小域精确枚举验证 ----------
//
// 对每个输入的有理误差区间取有限网格点（端点 + 四分点），
// 做笛卡尔积，用 big.Rat 精确求值，检查所有样本都落在
// 仿射分析给出的保守界内。除数恰为 0 的样本点单独计数跳过。

type VerifyResult struct {
	Samples int      // 成功求值的样本数
	Skipped int      // 因除数为 0 跳过的样本数
	Min     *big.Rat // 样本最小值
	Max     *big.Rat // 样本最大值
	OK      bool     // 是否全部落在保守界内
}

func gridPoints(in *Node) []*big.Rat {
	lo := new(big.Rat).Add(in.nominal, in.errLo)
	hi := new(big.Rat).Add(in.nominal, in.errHi)
	if lo.Cmp(hi) == 0 {
		return []*big.Rat{lo}
	}
	four := big.NewRat(4, 1)
	mix := func(wLo, wHi int64) *big.Rat {
		num := new(big.Rat).Add(
			new(big.Rat).Mul(big.NewRat(wLo, 1), lo),
			new(big.Rat).Mul(big.NewRat(wHi, 1), hi),
		)
		return num.Quo(num, four)
	}
	return []*big.Rat{lo, mix(3, 1), mix(2, 2), mix(1, 3), hi}
}

// Verify 枚举所有输入网格组合，验证 root 的精确值都在 bound 内。
func Verify(root *Node, inputs []*Node, bound Interval) VerifyResult {
	grids := make([][]*big.Rat, len(inputs))
	for i, in := range inputs {
		grids[i] = gridPoints(in)
	}
	res := VerifyResult{OK: true}
	env := map[*Node]*big.Rat{}
	var rec func(i int)
	rec = func(i int) {
		if i == len(inputs) {
			v, err := root.EvalExact(env, map[*Node]*big.Rat{})
			if err != nil {
				res.Skipped++
				return
			}
			res.Samples++
			if res.Min == nil || v.Cmp(res.Min) < 0 {
				res.Min = new(big.Rat).Set(v)
			}
			if res.Max == nil || v.Cmp(res.Max) > 0 {
				res.Max = new(big.Rat).Set(v)
			}
			if !bound.contains(v) {
				res.OK = false
			}
			return
		}
		for _, p := range grids[i] {
			env[inputs[i]] = p
			rec(i + 1)
		}
	}
	rec(0)
	return res
}

func (r VerifyResult) String() string {
	status := "OK（全部被包含）"
	if !r.OK {
		status = "失败（存在越界样本！）"
	}
	minMax := "n/a"
	if r.Min != nil {
		minMax = fmt.Sprintf("min=%s max=%s", r.Min.RatString(), r.Max.RatString())
	}
	return fmt.Sprintf("枚举验证: %s | 样本=%d 跳过(除0)=%d | %s", status, r.Samples, r.Skipped, minMax)
}
