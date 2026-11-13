package netsec

import "sort"

// PathReport 是一条被保护路径的审查结果。
type PathReport struct {
	Path     string   // 被保护路径
	Matches  []Match  // 命中该路径的 filter scheme/host 组合
	Hosts    []string // 命中该路径的去重 host 集合（排序后）
	Conflict bool     // 是否被多于一个 host 接管
}

// AnalyzePaths 判断每条被保护路径会被哪些 filter 的哪些
// scheme/host 组合接管；预期只由一个 host 处理的路径若同时
// 被另一个 host 命中，则标记 Conflict 报告冲突。
func AnalyzePaths(filters []Filter, paths []string) []PathReport {
	reports := make([]PathReport, 0, len(paths))
	for _, path := range paths {
		report := PathReport{Path: path}
		seen := map[string]bool{}
		for _, f := range filters {
			for _, m := range f.MatchesPath(path) {
				report.Matches = append(report.Matches, m)
				if !seen[m.Host] {
					seen[m.Host] = true
					report.Hosts = append(report.Hosts, m.Host)
				}
			}
		}
		sort.Strings(report.Hosts)
		report.Conflict = len(report.Hosts) > 1
		reports = append(reports, report)
	}
	return reports
}

// URLReport 是一条攻击者 URL 的判定结果。
type URLReport struct {
	URL     URL     // 攻击者构造的 URL
	Matched bool    // 是否被任一 filter 匹配
	Matches []Match // 命中的 filter scheme/host 组合
}

// AnalyzeURLs 判断每条攻击者 URL 是否被任一 filter 匹配。
func AnalyzeURLs(filters []Filter, urls []URL) []URLReport {
	reports := make([]URLReport, 0, len(urls))
	for _, u := range urls {
		report := URLReport{URL: u}
		for _, f := range filters {
			report.Matches = append(report.Matches, f.Matches(u)...)
		}
		report.Matched = len(report.Matches) > 0
		reports = append(reports, report)
	}
	return reports
}
