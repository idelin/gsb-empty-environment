package netsec

import "testing"

// 审查场景：合法应用处理 /callback 前缀；攻击者 filter 用
// *. 主机加过宽的 .* 接管任意子域路径；另一个 filter 把
// pathPattern 误写成通配符风格的 /callback/*。
func reviewFilters() []Filter {
	return []Filter{
		{Name: "legit", Data: []Data{
			{Scheme: "https", Host: "app.example.com", PathPrefix: "/callback"},
		}},
		{Name: "wildcard-takeover", Data: []Data{
			{Scheme: "https", Host: "*.example.com", PathPattern: ".*"},
		}},
		{Name: "glob-mistake", Data: []Data{
			{Scheme: "https", Host: "evil.com", PathPattern: "/callback/*"},
		}},
	}
}

func hostsOf(report PathReport) map[string]bool {
	set := map[string]bool{}
	for _, h := range report.Hosts {
		set[h] = true
	}
	return set
}

func matchedByFilter(report PathReport, name string) bool {
	for _, m := range report.Matches {
		if m.Filter == name {
			return true
		}
	}
	return false
}

func TestAnalyzePaths(t *testing.T) {
	reports := AnalyzePaths(reviewFilters(), []string{"/callback/confirm", "/settings", "/callback//"})
	byPath := map[string]PathReport{}
	for _, r := range reports {
		byPath[r.Path] = r
	}

	// 预期只由 app.example.com 处理的路径被 *.example.com 也命中 -> 冲突。
	confirm := byPath["/callback/confirm"]
	if !confirm.Conflict {
		t.Errorf("/callback/confirm should report conflict, hosts = %v", confirm.Hosts)
	}
	hosts := hostsOf(confirm)
	if !hosts["app.example.com"] || !hosts["*.example.com"] {
		t.Errorf("/callback/confirm hosts = %v, want app.example.com and *.example.com", confirm.Hosts)
	}
	// /callback/* 的真实语义不匹配 /callback/confirm。
	if matchedByFilter(confirm, "glob-mistake") {
		t.Error("pathPattern /callback/* should not match /callback/confirm")
	}

	// /settings 只被过宽的 .* 接管，单一 host 不构成冲突。
	settings := byPath["/settings"]
	if settings.Conflict {
		t.Errorf("/settings should not report conflict, hosts = %v", settings.Hosts)
	}
	if len(settings.Hosts) != 1 || settings.Hosts[0] != "*.example.com" {
		t.Errorf("/settings hosts = %v, want only *.example.com", settings.Hosts)
	}

	// /callback// 是 /callback/* 真实语义下少数能命中的路径之一。
	slashes := byPath["/callback//"]
	if !matchedByFilter(slashes, "glob-mistake") {
		t.Error("pathPattern /callback/* should match /callback//")
	}
}

func TestAnalyzeURLs(t *testing.T) {
	urls := []URL{
		{Scheme: "https", Host: "evil.example.com", Path: "/callback/confirm"},
		{Scheme: "https", Host: "evil.com", Path: "/callback/confirm"},
		{Scheme: "https", Host: "evil.com", Path: "/callback//"},
		{Scheme: "https", Host: "app.example.com", Path: "/callback/confirm"},
	}
	reports := AnalyzeURLs(reviewFilters(), urls)

	// *. 主机加 .* 接管任意子域的登录回调。
	if !reports[0].Matched || !matchedByFilter(PathReport{Matches: reports[0].Matches}, "wildcard-takeover") {
		t.Errorf("attacker URL on subdomain should be taken over, matches = %v", reports[0].Matches)
	}
	// /callback/* 的真实语义放过了正常子路径。
	if reports[1].Matched {
		t.Errorf("/callback/* should not match /callback/confirm, matches = %v", reports[1].Matches)
	}
	// 但重复斜杠的畸形路径反而命中。
	if !reports[2].Matched {
		t.Error("/callback/* should match /callback//")
	}
	// 合法 URL 同时被合法 filter 与接管 filter 命中。
	if !reports[3].Matched {
		t.Error("legit URL should be matched")
	}
	if !matchedByFilter(PathReport{Matches: reports[3].Matches}, "legit") {
		t.Error("legit URL should be matched by legit filter")
	}
}
