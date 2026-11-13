package netsec

import "testing"

// /callback/* 的真实语义：星号只重复紧邻的前一个字符（斜杠），
// 而不是通配符意义上的"任意后缀"。
func TestMatchPathPatternCallbackStar(t *testing.T) {
	pattern := "/callback/*"
	matches := []string{"/callback", "/callback/", "/callback//", "/callback///"}
	nonMatches := []string{"", "/callback/foo", "/callbackx", "/other", "/callback/foo/bar"}
	for _, p := range matches {
		if !MatchPathPattern(pattern, p) {
			t.Errorf("MatchPathPattern(%q, %q) = false, want true", pattern, p)
		}
	}
	for _, p := range nonMatches {
		if MatchPathPattern(pattern, p) {
			t.Errorf("MatchPathPattern(%q, %q) = true, want false", pattern, p)
		}
	}
}

// .* 才匹配任意序列，因此常被写得过宽。
func TestMatchPathPatternDotStar(t *testing.T) {
	for _, p := range []string{"", "/", "/admin", "/callback/confirm", "/anything/at/all"} {
		if !MatchPathPattern(".*", p) {
			t.Errorf("MatchPathPattern(%q, %q) = false, want true", ".*", p)
		}
	}
	if !MatchPathPattern("/callback/.*", "/callback/confirm") {
		t.Error("pathPattern /callback/.* should match /callback/confirm")
	}
	if !MatchPathPattern("/callback/.*", "/callback/") {
		t.Error("pathPattern /callback/.* should match /callback/")
	}
	if MatchPathPattern("/callback/.*", "/callback") {
		t.Error("pathPattern /callback/.* should not match /callback")
	}
	if MatchPathPattern("/callback/.*", "/other/confirm") {
		t.Error("pathPattern /callback/.* should not match /other/confirm")
	}
}

// 转义点号只匹配字面点号。
func TestMatchPathPatternEscapedDot(t *testing.T) {
	pattern := "/callback\\.html"
	if !MatchPathPattern(pattern, "/callback.html") {
		t.Errorf("MatchPathPattern(%q, %q) = false, want true", pattern, "/callback.html")
	}
	for _, p := range []string{"/callbackXhtml", "/callback.htmlx", "/callback.htm"} {
		if MatchPathPattern(pattern, p) {
			t.Errorf("MatchPathPattern(%q, %q) = true, want false", pattern, p)
		}
	}
}

// 点号匹配任意单字符；星号重复零次或多次；转义星号按字面。
func TestMatchPathPatternDotStarEscape(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"/cb.", "/cbx", true},
		{"/cb.", "/cb", false},
		{"/cb.", "/cbxy", false},
		{"/ab*c", "/ac", true},
		{"/ab*c", "/abc", true},
		{"/ab*c", "/abbc", true},
		{"/ab*c", "/abdc", false},
		{"/a\\*b", "/a*b", true},
		{"/a\\*b", "/ab", false},
		{"/a\\*b", "/aaab", false},
	}
	for _, c := range cases {
		if got := MatchPathPattern(c.pattern, c.path); got != c.want {
			t.Errorf("MatchPathPattern(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

// path 全等、pathPrefix 前缀、pathSuffix 后缀。
func TestMatchPathSpec(t *testing.T) {
	exact := pathSpec{kind: pathExact, value: "/callback"}
	prefix := pathSpec{kind: pathPrefix, value: "/callback"}
	suffix := pathSpec{kind: pathSuffix, value: ".html"}

	if !matchPathSpec(exact, "/callback") || matchPathSpec(exact, "/callback/x") {
		t.Error("path exact match failed")
	}
	if !matchPathSpec(prefix, "/callback/x") || matchPathSpec(prefix, "/other") {
		t.Error("pathPrefix match failed")
	}
	if !matchPathSpec(suffix, "/a/b.html") || matchPathSpec(suffix, "/a/b.htm") {
		t.Error("pathSuffix match failed")
	}
}
