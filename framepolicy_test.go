package netsec

import (
	"net/http"
	"net/url"
	"testing"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

func header(pairs ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(pairs); i += 2 {
		h.Add(pairs[i], pairs[i+1])
	}
	return h
}

// TestXFrameOptionsIgnoredWhenFrameAncestorsPresent 验证存在 frame-ancestors 时
// X-Frame-Options 完全被忽略：即使 XFO 是 DENY，CSP 放行即放行。
func TestXFrameOptionsIgnoredWhenFrameAncestorsPresent(t *testing.T) {
	resource := mustURL(t, "https://app.example.com/dashboard")
	parent := mustURL(t, "https://trust.example/index.html")
	h := header(
		"Content-Security-Policy", "frame-ancestors https://trust.example",
		"X-Frame-Options", "DENY",
	)

	got := Evaluate(h, resource, []*url.URL{parent})
	if !got.Allowed {
		t.Fatalf("expected allow because frame-ancestors takes precedence, got %+v", got)
	}
	if got.Mechanism != ReasonCSP {
		t.Fatalf("expected mechanism %q, got %q", ReasonCSP, got.Mechanism)
	}

	// 反向验证：CSP 拒绝时，宽松的 XFO 无法挽救。
	h = header(
		"Content-Security-Policy", "frame-ancestors 'none'",
		"X-Frame-Options", "SAMEORIGIN",
	)
	got = Evaluate(h, resource, []*url.URL{parent})
	if got.Allowed {
		t.Fatalf("expected deny by frame-ancestors 'none', got %+v", got)
	}
}

// TestNoneCoexistsWithOtherSources 验证 'none' 与其他来源共存时 'none' 失效。
func TestNoneCoexistsWithOtherSources(t *testing.T) {
	resource := mustURL(t, "https://app.example.com/widget")
	allowedParent := mustURL(t, "https://trust.example/")
	otherParent := mustURL(t, "https://evil.example/")
	h := header(
		"Content-Security-Policy", "frame-ancestors 'none' https://trust.example",
	)

	if !Allowed(h, resource, []*url.URL{allowedParent}) {
		t.Fatal("'none' must be ignored when co-located with a real source; trust.example should be allowed")
	}
	if Allowed(h, resource, []*url.URL{otherParent}) {
		t.Fatal("source not in the list must still be denied")
	}

	// 单独的 'none' 仍表示拒绝一切。
	noneOnly := header("Content-Security-Policy", "frame-ancestors 'none'")
	if Allowed(noneOnly, resource, []*url.URL{allowedParent}) {
		t.Fatal("frame-ancestors 'none' alone must deny all framing")
	}
}

// TestReportOnlyNotEnforced 验证 Report-Only 不参与判定。
func TestReportOnlyNotEnforced(t *testing.T) {
	resource := mustURL(t, "https://app.example.com/widget")
	parent := mustURL(t, "https://evil.example/")

	// 只有 Report-Only 时相当于没有强制策略，默认允许。
	reportOnly := header(
		"Content-Security-Policy-Report-Only", "frame-ancestors 'none'",
	)
	got := Evaluate(reportOnly, resource, []*url.URL{parent})
	if !got.Allowed || got.Mechanism != ReasonNoPolicy {
		t.Fatalf("report-only must not enforce; got %+v", got)
	}

	// Report-Only 与强制头并存时只看强制头；此处强制头放行。
	mixed := header(
		"Content-Security-Policy-Report-Only", "frame-ancestors 'none'",
		"Content-Security-Policy", "frame-ancestors https://evil.example",
	)
	got = Evaluate(mixed, resource, []*url.URL{parent})
	if !got.Allowed {
		t.Fatalf("enforce header should win over report-only; got %+v", got)
	}
}

// TestRepeatedXFrameOptionsFailsClosed 验证重复头、逗号多值与非法值一律拒绝。
func TestRepeatedXFrameOptionsFailsClosed(t *testing.T) {
	resource := mustURL(t, "https://app.example.com/page")
	parent := mustURL(t, "https://app.example.com/other")

	cases := map[string]http.Header{
		"repeated header": header(
			"X-Frame-Options", "DENY",
			"X-Frame-Options", "SAMEORIGIN",
		),
		"comma separated values": header("X-Frame-Options", "DENY, SAMEORIGIN"),
		"single comma token":     header("X-Frame-Options", "DENY,SAMEORIGIN"),
		"unknown directive":      header("X-Frame-Options", "ALLOW-FROM https://trust.example"),
		"empty value":            header("X-Frame-Options", "   "),
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			got := Evaluate(h, resource, []*url.URL{parent})
			if got.Allowed || got.Mechanism != ReasonXFO {
				t.Fatalf("expected fail-closed XFO decision, got %+v", got)
			}
		})
	}

	// 合法的单一值仍然正常工作。
	if !Allowed(header("X-Frame-Options", "SAMEORIGIN"), resource, []*url.URL{parent}) {
		t.Fatal("single valid SAMEORIGIN with same-origin parent should allow")
	}
}

// TestThreeLevelNestingMiddleAncestor 验证三层嵌套中中间层不匹配即拒绝：
// 最外层与最内层（直接父页面）都匹配，唯独中间层不在允许集合内。
func TestThreeLevelNestingMiddleAncestor(t *testing.T) {
	resource := mustURL(t, "https://app.example.com/frame")
	outer := mustURL(t, "https://trust.example/top")
	middle := mustURL(t, "https://evil.example/mid")
	immediate := mustURL(t, "https://trust.example/parent")

	h := header("Content-Security-Policy", "frame-ancestors https://trust.example")

	// 中间层是恶意源：必须拒绝，即便直接父页面完全匹配。
	got := Evaluate(h, resource, []*url.URL{outer, middle, immediate})
	if got.Allowed {
		t.Fatalf("middle ancestor must be checked; expected deny, got %+v", got)
	}

	// 三层全部匹配时放行。
	allMatch := []*url.URL{
		outer,
		mustURL(t, "https://trust.example/mid"),
		immediate,
	}
	got = Evaluate(h, resource, allMatch)
	if !got.Allowed {
		t.Fatalf("all three ancestors match; expected allow, got %+v", got)
	}

	// 仅检查直接父页面会漏掉的情形在这里被拦截。
	if Allowed(h, resource, []*url.URL{middle, immediate}) {
		t.Fatal("non-matching outer ancestor must also deny")
	}
}

// TestMultipleCSPHeadersIntersect 验证同名 CSP 头多次出现时取交集。
func TestMultipleCSPHeadersIntersect(t *testing.T) {
	resource := mustURL(t, "https://app.example.com/gadget")
	trust := mustURL(t, "https://trust.example/")
	other := mustURL(t, "https://other.example/")

	h := header(
		"Content-Security-Policy", "frame-ancestors https://trust.example https://other.example",
		"Content-Security-Policy", "frame-ancestors https://trust.example",
	)
	if !Allowed(h, resource, []*url.URL{trust}) {
		t.Fatal("trust.example is in both policies and must be allowed")
	}
	if Allowed(h, resource, []*url.URL{other}) {
		t.Fatal("other.example is absent from the second policy and must be denied by intersection")
	}
}

// TestSourceMatching 覆盖 self、方案、通配主机与端口等来源表达式。
func TestSourceMatching(t *testing.T) {
	resource := mustURL(t, "https://app.example.com:8443/home")

	cases := []struct {
		name      string
		directive string
		ancestor  string
		want      bool
	}{
		{"self matches", "frame-ancestors 'self'", "https://app.example.com:8443/x", true},
		{"self differs by port", "frame-ancestors 'self'", "https://app.example.com/x", false},
		{"scheme source", "frame-ancestors https:", "https://any.example/path", true},
		{"scheme source wrong scheme", "frame-ancestors https:", "http://any.example/path", false},
		{"host default port", "frame-ancestors https://shop.example", "https://shop.example/", true},
		{"host non-default needs explicit port", "frame-ancestors https://shop.example", "https://shop.example:8443/", false},
		{"explicit port", "frame-ancestors https://shop.example:8443", "https://shop.example:8443/", true},
		{"wildcard port", "frame-ancestors https://shop.example:*", "https://shop.example:9000/", true},
		{"wildcard subdomain", "frame-ancestors https://*.shop.example", "https://a.b.shop.example/", true},
		{"wildcard apex per CSP3", "frame-ancestors https://*.shop.example", "https://shop.example/", true},
		{"wildcard rejects sibling suffix", "frame-ancestors https://*.shop.example", "https://notshop.example/", false},
		{"scheme mismatch on host source", "frame-ancestors https://shop.example", "http://shop.example/", false},
		{"malformed token ignored, real source works", "frame-ancestors https://bad::token https://shop.example", "https://shop.example/", true},
		{"all malformed tokens fall through", "frame-ancestors https://bad::token", "https://shop.example/", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := header("Content-Security-Policy", tc.directive)
			got := Allowed(h, resource, []*url.URL{mustURL(t, tc.ancestor)})
			if got != tc.want {
				t.Fatalf("directive %q ancestor %q: want %v got %v", tc.directive, tc.ancestor, tc.want, got)
			}
		})
	}
}

// TestXFODenyAndSameOrigin 验证 XFO 两条合法指令的基础行为与顶层加载。
func TestXFODenyAndSameOrigin(t *testing.T) {
	resource := mustURL(t, "https://app.example.com/page")
	sameOrigin := mustURL(t, "https://app.example.com/other")
	crossOrigin := mustURL(t, "https://other.example/")

	if Allowed(header("X-Frame-Options", "DENY"), resource, nil) != true {
		t.Fatal("top-level load must always be allowed")
	}
	if Allowed(header("X-Frame-Options", "DENY"), resource, []*url.URL{sameOrigin}) {
		t.Fatal("DENY must block even same-origin framing")
	}
	if !Allowed(header("X-Frame-Options", "SAMEORIGIN"), resource, []*url.URL{sameOrigin}) {
		t.Fatal("SAMEORIGIN must allow same-origin parent")
	}
	if Allowed(header("X-Frame-Options", "SAMEORIGIN"), resource, []*url.URL{crossOrigin}) {
		t.Fatal("SAMEORIGIN must block cross-origin parent")
	}
}

// TestNoPolicyAndNilHeaders 验证无策略与 nil 头的默认行为。
func TestNoPolicyAndNilHeaders(t *testing.T) {
	resource := mustURL(t, "https://app.example.com/")
	parent := mustURL(t, "https://anyone.example/")
	got := Evaluate(nil, resource, []*url.URL{parent})
	if !got.Allowed || got.Mechanism != ReasonNoPolicy {
		t.Fatalf("nil headers should default to allow, got %+v", got)
	}
}

// TestSourceEdgeCases 覆盖 IPv6、裸通配主机、用户信息/路径拒绝等边界。
func TestSourceEdgeCases(t *testing.T) {
	resource := mustURL(t, "https://app.example.com/")

	cases := []struct {
		name      string
		directive string
		ancestor  string
		want      bool
	}{
		{"bare wildcard host", "frame-ancestors https://*", "https://anything.example:443/", true},
		{"bare wildcard host wrong scheme", "frame-ancestors https://*", "http://anything.example/", false},
		{"ipv6 host with port", "frame-ancestors https://[2001:db8::1]:8443", "https://[2001:db8::1]:8443/", true},
		{"ipv6 host port mismatch", "frame-ancestors https://[2001:db8::1]:8443", "https://[2001:db8::1]:9000/", false},
		{"userinfo rejected", "frame-ancestors https://user@shop.example", "https://shop.example/", true},
		{"path rejected", "frame-ancestors https://shop.example/path", "https://shop.example/", true},
		{"non-scheme prefix rejected", "frame-ancestors 1bad://shop.example", "https://shop.example/", true},
		{"bad wildcard label", "frame-ancestors https://*..shop.example", "https://shop.example/", true},
		{"ws default port", "frame-ancestors ws:", "ws://socket.example/", true},
		{"unknown scheme no default port", "frame-ancestors custom:", "custom://host/", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := header("Content-Security-Policy", tc.directive)
			got := Allowed(h, resource, []*url.URL{mustURL(t, tc.ancestor)})
			if got != tc.want {
				t.Fatalf("directive %q ancestor %q: want %v got %v", tc.directive, tc.ancestor, tc.want, got)
			}
		})
	}

	// 指令仅含畸形 token 时不产生限制，等价于无 frame-ancestors，回落到默认允许。
	fallthroughHeader := header("Content-Security-Policy", "frame-ancestors https://user@evil https://*..bad")
	got := Evaluate(fallthroughHeader, resource, []*url.URL{mustURL(t, "https://anyone.example/")})
	if !got.Allowed || got.Mechanism != ReasonNoPolicy {
		t.Fatalf("all-malformed directive should be ignored, got %+v", got)
	}

	// XFO 与全畸形 CSP 并存时回落到 XFO 判定，DENY 仍然生效。
	withXFO := header(
		"Content-Security-Policy", "frame-ancestors https://user@evil",
		"X-Frame-Options", "DENY",
	)
	if Allowed(withXFO, resource, []*url.URL{mustURL(t, "https://anyone.example/")}) {
		t.Fatal("fallback to XFO DENY must block framing")
	}
}

// TestDuplicateDirectiveInSinglePolicy 验证单条 CSP 内重复指令时首个生效。
func TestDuplicateDirectiveInSinglePolicy(t *testing.T) {
	resource := mustURL(t, "https://app.example.com/")
	h := header(
		"Content-Security-Policy",
		"frame-ancestors https://first.example; frame-ancestors 'none'",
	)
	if !Allowed(h, resource, []*url.URL{mustURL(t, "https://first.example/")}) {
		t.Fatal("first frame-ancestors directive should win within one policy")
	}
}
