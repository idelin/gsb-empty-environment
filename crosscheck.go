package netsec

// CrossCheckReport 汇总三条独立路径的核对结论。
type CrossCheckReport struct {
	// Feasible 与 Analyze 的结论一致。
	Feasible bool
	// MatchCount 是枚举到的完整合法匹配数量（截断时仅为已枚举部分）。
	MatchCount int
	// Truncated 表示枚举达到上限提前停止。
	Truncated bool
	// AllMatchingsValid 表示每个枚举结果都通过 ValidMatching 与
	// IsComplete 的独立校验。
	AllMatchingsValid bool
	// AnalysisMatchingValid 表示 Analyze 返回的匹配通过独立校验。
	AnalysisMatchingValid bool
	// WitnessValid 表示不可行时的 Hall 缺口证据通过独立校验。
	WitnessValid bool
	// Consistent 为 true 当且仅当上述各路径结论互不矛盾。
	Consistent bool
}

// CrossCheck 用“最大匹配判定 + Hall 证据 + 全量枚举”三条独立路径
// 互相核对同一事件流，供 Go 验证宿主作为最终结论使用。
//
// limit 控制枚举上限，传 0 或负数使用 DefaultEnumerationLimit。
func CrossCheck(events []Event, limit int) (*CrossCheckReport, error) {
	analysis, err := Analyze(events)
	if err != nil {
		return nil, err
	}
	enumeration, err := EnumerateMatchings(events, limit)
	if err != nil {
		return nil, err
	}

	report := &CrossCheckReport{
		Feasible:              analysis.Feasible,
		MatchCount:            len(enumeration.Matchings),
		Truncated:             enumeration.Truncated,
		AllMatchingsValid:     true,
		AnalysisMatchingValid: true,
		WitnessValid:          true,
	}

	for _, matching := range enumeration.Matchings {
		if !ValidMatching(events, matching) || !IsComplete(events, matching) {
			report.AllMatchingsValid = false
			break
		}
	}

	enumerationFoundMatch := len(enumeration.Matchings) > 0

	if analysis.Feasible {
		report.AnalysisMatchingValid =
			ValidMatching(events, analysis.Matching) && IsComplete(events, analysis.Matching)
		report.Consistent = report.AnalysisMatchingValid &&
			report.AllMatchingsValid && enumerationFoundMatch
	} else {
		report.WitnessValid = ValidWitness(events, analysis.Witness)
		// 未截断时枚举必须为空；截断场景下无法用枚举证伪，只能依赖其余检查。
		enumerationAgrees := enumeration.Truncated || !enumerationFoundMatch
		report.Consistent = report.WitnessValid &&
			report.AllMatchingsValid && enumerationAgrees
	}

	return report, nil
}
