package netsec

import "testing"

// host 以 *. 开头时匹配任意前缀子域；scheme 与 host 不区分大小写。
func TestMatchHost(t *testing.T) {
	cases := []struct {
		pattern string
		host    string
		want    bool
	}{
		{"*.example.com", "evil.example.com", true},
		{"*.example.com", "a.b.example.com", true},
		{"*.example.com", "example.com", false},
		{"*.example.com", "notexample.com", false},
		{"*.example.com", "example.com.evil.com", false},
		{"app.example.com", "APP.EXAMPLE.COM", true},
		{"app.example.com", "other.example.com", false},
	}
	for _, c := range cases {
		if got := matchHost(c.pattern, c.host); got != c.want {
			t.Errorf("matchHost(%q, %q) = %v, want %v", c.pattern, c.host, got, c.want)
		}
	}
	if !matchScheme("HTTPS", "https") {
		t.Error("scheme comparison should be case-insensitive")
	}
}

// 同一 filter 内多个 data 元素的 scheme、host、path 取笛卡尔积：
// 路径 /y 与 host a.example.com 虽来自不同 data 元素，组合后仍匹配。
func TestFilterCartesianProduct(t *testing.T) {
	f := Filter{
		Name: "combo",
		Data: []Data{
			{Scheme: "https", Host: "a.example.com", Path: "/x"},
			{Scheme: "https", Host: "b.example.com", Path: "/y"},
		},
	}
	if got := f.Matches(URL{Scheme: "https", Host: "a.example.com", Path: "/y"}); len(got) == 0 {
		t.Error("cross-combination of host from one data element and path from another should match")
	}
	if got := f.Matches(URL{Scheme: "https", Host: "c.example.com", Path: "/y"}); len(got) != 0 {
		t.Error("undeclared host should not match")
	}
	if got := f.Matches(URL{Scheme: "https", Host: "a.example.com", Path: "/z"}); len(got) != 0 {
		t.Error("undeclared path should not match")
	}
	if got := f.Matches(URL{Scheme: "HTTPS", Host: "A.Example.COM", Path: "/x"}); len(got) == 0 {
		t.Error("scheme and host comparison should be case-insensitive")
	}
}

// 四种路径属性在 filter 层面的行为。
func TestFilterPathKinds(t *testing.T) {
	f := Filter{
		Name: "kinds",
		Data: []Data{
			{Scheme: "https", Host: "h.example.com", Path: "/exact"},
			{Scheme: "https", Host: "h.example.com", PathPrefix: "/pre"},
			{Scheme: "https", Host: "h.example.com", PathSuffix: ".html"},
			{Scheme: "https", Host: "h.example.com", PathPattern: "/callback\\.html"},
		},
	}
	matches := []string{"/exact", "/pre", "/prefix", "/a/b.html", "/callback.html"}
	nonMatches := []string{"/exact2", "/other", "/callbackXhtml"}
	for _, p := range matches {
		if got := f.Matches(URL{Scheme: "https", Host: "h.example.com", Path: p}); len(got) == 0 {
			t.Errorf("path %q should match filter", p)
		}
	}
	for _, p := range nonMatches {
		if got := f.Matches(URL{Scheme: "https", Host: "h.example.com", Path: p}); len(got) != 0 {
			t.Errorf("path %q should not match filter", p)
		}
	}
}

func TestParseURL(t *testing.T) {
	u, err := ParseURL("https://Evil.Example.COM:8443/callback/confirm?x=1")
	if err != nil {
		t.Fatalf("ParseURL: %v", err)
	}
	if u.Scheme != "https" || u.Host != "Evil.Example.COM" || u.Path != "/callback/confirm" {
		t.Errorf("ParseURL = %+v", u)
	}
	// 大小写差异由 matchHost 处理，解析结果仍应命中声明小写 host 的 filter。
	f := Filter{Name: "legit", Data: []Data{{Scheme: "https", Host: "evil.example.com", PathPrefix: "/callback"}}}
	if got := f.Matches(u); len(got) == 0 {
		t.Error("filter should match URL regardless of host case")
	}
}
