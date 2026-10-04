package main

import (
	"fmt"
	"math/big"
)

// DrawRecord 记录一次抽取的完整过程，用于输出可复现的抽样轨迹。
type DrawRecord struct {
	Step      int      // 第几次抽取（从 1 开始）
	Remaining *big.Int // 本次抽取前剩余块的总权重
	Roll      *big.Int // 在 [0, Remaining) 上均匀抽取的随机值
	BlockID   string   // 被抽中的块
	Weight    *big.Int // 被抽中块的权重
}

// selectIndex 按累积权重区间定位随机值 r 命中的块。
// r 落在块 i 的区间 [sum(w_0..w_{i-1}), sum(w_0..w_i)) 内即选中 i，
// 因此每个块被选中的概率严格正比于其逻辑长度。
func selectIndex(remaining []*Block, r *big.Int) int {
	acc := new(big.Int)
	for i, b := range remaining {
		acc.Add(acc, b.Length)
		if r.Cmp(acc) < 0 {
			return i
		}
	}
	panic("selectIndex: 随机值超出总权重范围")
}

// Sample 按块逻辑长度加权、无放回地抽取 k 个块。
//
// 抽样过程完全由 seed 决定：同一种子重复抽样得到相同序列；
// 且 Sample(seed, k) 的结果是 Sample(seed, k+m) 的前缀，
// 因此中断后恢复、或追加抽样时，已选中的块不会被改变。
func Sample(a *Archive, seed string, k int) ([]*Block, []DrawRecord, error) {
	if k <= 0 {
		return nil, nil, fmt.Errorf("抽样数量 k 必须为正整数")
	}
	if k > len(a.Blocks) {
		return nil, nil, fmt.Errorf("抽样数量 %d 超过块总数 %d（无放回抽样）", k, len(a.Blocks))
	}

	remaining := make([]*Block, len(a.Blocks))
	copy(remaining, a.Blocks)
	stream := newStream(seed)

	var picked []*Block
	var records []DrawRecord
	for step := 1; step <= k; step++ {
		total := new(big.Int)
		for _, b := range remaining {
			total.Add(total, b.Length)
		}
		roll := uniformBig(stream, total)
		idx := selectIndex(remaining, roll)
		b := remaining[idx]

		records = append(records, DrawRecord{
			Step:      step,
			Remaining: new(big.Int).Set(total),
			Roll:      roll,
			BlockID:   b.ID,
			Weight:    new(big.Int).Set(b.Length),
		})
		picked = append(picked, b)
		// 无放回：把抽中的块从候选集中移除
		remaining = append(remaining[:idx], remaining[idx+1:]...)
	}
	return picked, records, nil
}
