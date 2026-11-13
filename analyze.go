package netsec

import (
	"fmt"
	"sort"
)

// Group 是一条轨迹对应的候选秘密集合。
type Group struct {
	Trace   string   // 该组共享的轨迹
	Members []uint64 // 产生该轨迹的全部秘密 x，升序
}

// Report 汇总对给定模数 p 的穷举分析结果。
type Report struct {
	P uint64
	// Groups 按轨迹分组的结果，按组内最小秘密升序排列。
	Groups []Group
	// MinPrefixLen 是任意两个不同轨迹秘密之间的最短区分前缀长度，
	// 即攻击者最少只需观察多少步就能区分某一对秘密。
	// 若所有秘密轨迹相同（不会发生）则为 0。
	MinPrefixLen int
	// PrefixPair 是达到最短区分前缀的一对秘密及其公共前缀之后的分叉符号。
	PrefixPair [2]uint64
	// Prefix 是 PrefixPair 的公共前缀（长度为 MinPrefixLen-1）。
	Prefix string
	// MaxSteps 是所有秘密中最长的轨迹长度。
	MaxSteps int
	// Unconverged 是在 Rounds(p) 预算内未收敛的秘密列表（正常应为空）。
	Unconverged []uint64
}

// commonPrefixLen 返回两字符串的公共前缀长度。
func commonPrefixLen(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

// Analyze 枚举 x ∈ [1, p-1]，用 VulnerableInvert 生成轨迹并按轨迹分组，
// 计算任意两秘密轨迹的最短区分前缀、每组候选集合、最长轨迹与未收敛输入。
func Analyze(p uint64) (*Report, error) {
	if err := ValidateParams(1, p); err != nil {
		return nil, err
	}
	budget := Rounds(p)
	rep := &Report{P: p}
	byTrace := make(map[string][]uint64)
	for x := uint64(1); x < p; x++ {
		inv, trace, err := VulnerableInvert(x, p, budget)
		if err == ErrBudget {
			rep.Unconverged = append(rep.Unconverged, x)
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := VerifyTrace(x, p, inv, trace, budget); err != nil {
			return nil, fmt.Errorf("netsec: self-check failed for x=%d: %w", x, err)
		}
		byTrace[trace] = append(byTrace[trace], x)
		if len(trace) > rep.MaxSteps {
			rep.MaxSteps = len(trace)
		}
	}
	for trace, members := range byTrace {
		rep.Groups = append(rep.Groups, Group{Trace: trace, Members: members})
	}
	sort.Slice(rep.Groups, func(i, j int) bool {
		return rep.Groups[i].Members[0] < rep.Groups[j].Members[0]
	})
	// 最短区分前缀：两两轨迹比较，公共前缀长度 +1 即区分该对所需步数。
	rep.MinPrefixLen = 0
	for i := 0; i < len(rep.Groups); i++ {
		for j := i + 1; j < len(rep.Groups); j++ {
			ti, tj := rep.Groups[i].Trace, rep.Groups[j].Trace
			l := commonPrefixLen(ti, tj) + 1
			if rep.MinPrefixLen == 0 || l < rep.MinPrefixLen {
				rep.MinPrefixLen = l
				rep.PrefixPair = [2]uint64{rep.Groups[i].Members[0], rep.Groups[j].Members[0]}
				rep.Prefix = ti[:l-1]
			}
		}
	}
	return rep, nil
}
