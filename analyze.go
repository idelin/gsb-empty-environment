package netsec

import (
	"math/big"
	"slices"
)

func (v *Verifier) unknownSentinel() string {
	return v.cfg.UnknownSentinel
}

// evaluateGroupLocked 对单个泛化组同时执行 k 大小、l-多样性与有理占比检查，
// 并确定性地输出组画像与（可能存在的）最小违规证据。
func (v *Verifier) evaluateGroupLocked(g *rawGroup) (GroupInfo, *Violation) {
	records := slices.Clone(g.records)
	slices.SortFunc(records, func(a, b Record) int { return compareRecordID(a.ID, b.ID) })

	memberIDs := make([]string, 0, len(records))
	byValue := make(map[string][]string)
	unknownCount := 0
	unknownSentinel := v.unknownSentinel()

	for _, rec := range records {
		memberIDs = append(memberIDs, rec.ID)
		if rec.Sensitive == unknownSentinel {
			unknownCount++
			continue
		}
		byValue[rec.Sensitive] = append(byValue[rec.Sensitive], rec.ID)
	}

	values := make([]string, 0, len(byValue))
	for value := range byValue {
		values = append(values, value)
	}
	slices.Sort(values)

	knownCount := len(records) - unknownCount
	denominator := len(records)
	if v.cfg.RatioDenominator == RatioOverKnown {
		denominator = knownCount
	}

	frequencies := make([]Frequency, 0, len(values))
	for _, value := range values {
		count := len(byValue[value])
		var ratio *big.Rat
		if denominator > 0 {
			ratio = ExactShare(count, denominator)
		}
		frequencies = append(frequencies, Frequency{
			Value: value,
			Count: count,
			Ratio: ratio,
		})
	}

	info := GroupInfo{
		Key:           g.key,
		Size:          len(records),
		KnownCount:    knownCount,
		UnknownCount:  unknownCount,
		Members:       memberIDs,
		Frequencies:   frequencies,
		DistinctKnown: len(values),
	}

	var reasons Reason
	var violation Violation

	if v.cfg.MinGroupSize > 0 && len(records) < v.cfg.MinGroupSize {
		reasons |= ReasonGroupSize
	}

	// 多样性只统计“已知”敏感值种类：未知记录不充当一个类别。
	if len(values) < v.cfg.L {
		reasons |= ReasonDiversity
		representatives := make([]string, 0, len(values))
		for _, value := range values {
			representatives = append(representatives, byValue[value][0])
		}
		violation.Diversity = &DiversityEvidence{
			DistinctKnown:         len(values),
			Required:              v.cfg.L,
			RepresentativeMembers: representatives,
		}
	}

	// 占比检查：未知值不出现在分子里；分母按配置取全组或仅已知记录。
	// 阈值边界 == 为合规，严格 > 才违规，全程 big.Rat 精确比较。
	if v.cfg.MaxRatio != nil && denominator > 0 {
		for _, value := range values {
			count := len(byValue[value])
			ratio := ExactShare(count, denominator)
			if ratio.Cmp(v.cfg.MaxRatio) > 0 {
				reasons |= ReasonRatio
				violation.Ratios = append(violation.Ratios, RatioEvidence{
					Value:       value,
					Count:       count,
					Ratio:       new(big.Rat).Set(ratio),
					Limit:       new(big.Rat).Set(v.cfg.MaxRatio),
					Members:     slices.Clone(byValue[value]),
					Basis:       v.cfg.RatioDenominator,
					Denominator: denominator,
				})
			}
		}
	}

	if reasons == ReasonNone {
		return info, nil
	}
	violation.Key = g.key
	violation.Size = len(records)
	violation.Reasons = reasons
	return info, &violation
}

func compareRecordID(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
