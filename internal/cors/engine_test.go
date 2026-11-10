package cors

import "testing"

func TestPreflightCacheIsolation(t *testing.T) {
	shop, _ := ParseOrigin("https://shop.example")
	other, _ := ParseOrigin("https://other.example")
	target := "https://api.target.example/orders"

	cache := NewPreflightCache()
	cache.SetTime(0)
	if err := cache.Store(shop, target, true, "PUT",
		[]string{"content-type"}, 10); err != nil {
		t.Fatal(err)
	}

	// 同键命中。
	hit, err := cache.Lookup(shop, target, true, "PUT", []string{"content-type"})
	if err != nil || !hit {
		t.Fatalf("同键应命中: hit=%v err=%v", hit, err)
	}

	// 发起源不同 → 隔离。
	hit, _ = cache.Lookup(other, target, true, "PUT", []string{"content-type"})
	if hit {
		t.Fatal("不同发起源必须隔离缓存")
	}

	// 凭据条件不同 → 隔离。
	hit, _ = cache.Lookup(shop, target, false, "PUT", []string{"content-type"})
	if hit {
		t.Fatal("不同凭据条件必须隔离缓存")
	}

	// 目标 URL 不同 → 隔离。
	hit, _ = cache.Lookup(shop, "https://api.target.example/orders/2", true,
		"PUT", []string{"content-type"})
	if hit {
		t.Fatal("不同目标 URL 必须隔离缓存")
	}

	// 方法/头不在授权集合 → 未命中。
	hit, _ = cache.Lookup(shop, target, true, "DELETE", []string{"content-type"})
	if hit {
		t.Fatal("未授权方法不应命中")
	}
	hit, _ = cache.Lookup(shop, target, true, "PUT", []string{"x-admin"})
	if hit {
		t.Fatal("未授权头不应命中")
	}

	// 过期后查询应失效并惰性删除。
	cache.SetTime(10)
	hit, _ = cache.Lookup(shop, target, true, "PUT", []string{"content-type"})
	if hit {
		t.Fatal("t=10 达到过期时刻应失效")
	}
	if _, ok := cache.entries[cacheKey{
		Source:        shop.String(),
		URLNoFragment: target,
		Credentials:   true,
	}]; ok {
		t.Fatal("过期条目应被惰性删除")
	}

	// fragment 不影响缓存键。
	cache.SetTime(20)
	if err := cache.Store(shop, target, false, "GET", nil, -1); err != nil {
		t.Fatal(err)
	}
	hit, _ = cache.Lookup(shop, target+"#frag", false, "GET", nil)
	if !hit {
		t.Fatal("fragment 不应参与缓存键")
	}
}

func TestRunTwoStepBlockedVersusOpaque(t *testing.T) {
	source := "https://shop.example"
	in := &Input{
		Routes: []Route{
			{
				ID: "api", Origin: "https://api.target.example", Path: "/data",
				Preflight: &PreflightSpec{
					Status:  204,
					Headers: map[string]string{HeaderACAO: "https://other.example", HeaderACAM: "PUT"},
				},
				Reply: &ReplySpec{
					Status:  200,
					Headers: map[string]string{HeaderACAO: "https://other.example"},
				},
			},
		},
		Requests: []Request{
			{
				ID: "blocked", AtSeconds: 0, Source: source,
				URL: "https://api.target.example/data", Method: "PUT",
			},
			{
				ID: "opaque", AtSeconds: 1, Source: source,
				URL: "https://api.target.example/data", Method: "GET",
			},
		},
	}

	result, err := Run(in)
	if err != nil {
		t.Fatal(err)
	}
	if result.Requests[0].Status != StatusBlocked || result.Requests[0].RequestSent {
		t.Fatalf("预检失败应阻断且请求未发出: %+v", result.Requests[0])
	}
	if result.Requests[1].Status != StatusOpaque ||
		!result.Requests[1].RequestSent || result.Requests[1].ScriptReadable {
		t.Fatalf("简单请求应发出但响应不透明: %+v", result.Requests[1])
	}
}

func TestRunRedirectOriginSwitch(t *testing.T) {
	in := &Input{
		Routes: []Route{
			{
				ID: "edge", Origin: "https://edge.example", Path: "/go",
				Reply: &ReplySpec{
					Status:   307,
					Location: "https://cdn.partner.example/in",
				},
			},
			{
				ID: "cdn-allowed", Origin: "https://cdn.partner.example", Path: "/in",
				Preflight: &PreflightSpec{
					Status: 204,
					Headers: map[string]string{
						HeaderACAO: "https://app.example",
						HeaderACAM: "PUT",
						HeaderACAH: "x-custom",
					},
				},
				Reply: &ReplySpec{
					Status:  200,
					Headers: map[string]string{HeaderACAO: "https://app.example"},
				},
			},
		},
		Requests: []Request{
			{
				ID: "redirect-preflight", AtSeconds: 0, Source: "https://app.example",
				URL: "https://edge.example/go", Method: "PUT",
				Headers: map[string]string{"X-Custom": "1"},
				Note:    "edge 无预检建模但 PUT 会先触发预检 → 第一跳即阻断",
			},
		},
	}

	result, err := Run(in)
	if err != nil {
		t.Fatal(err)
	}
	report := result.Requests[0]
	if report.Status != StatusBlocked || report.RequestSent {
		t.Fatalf("第一跳预检失败应阻断: %+v", report)
	}
	if len(report.Hops) != 1 || report.Hops[0].Preflight == nil || report.Hops[0].Preflight.OK {
		t.Fatalf("应停在第一跳预检: %+v", report.Hops)
	}
}

func TestRunRedirectSecondHopPreflight(t *testing.T) {
	in := &Input{
		Routes: []Route{
			{
				ID: "edge", Origin: "https://edge.example", Path: "/go",
				Preflight: &PreflightSpec{
					Status: 204,
					Headers: map[string]string{
						HeaderACAO: "https://app.example",
						HeaderACAM: "PUT",
						HeaderACAH: "x-custom",
					},
				},
				Reply: &ReplySpec{
					Status:   307,
					Location: "https://cdn.partner.example/in",
				},
			},
			{
				ID: "cdn-no-preflight", Origin: "https://cdn.partner.example", Path: "/in",
				Reply: &ReplySpec{
					Status:  200,
					Headers: map[string]string{HeaderACAO: "https://app.example"},
				},
			},
		},
		Requests: []Request{
			{
				ID: "second-hop-block", AtSeconds: 0, Source: "https://app.example",
				URL: "https://edge.example/go", Method: "PUT",
				Headers: map[string]string{"X-Custom": "1"},
			},
		},
	}

	result, err := Run(in)
	if err != nil {
		t.Fatal(err)
	}
	report := result.Requests[0]
	if report.Status != StatusBlocked {
		t.Fatalf("第二跳缺预检应阻断: %+v", report)
	}
	if len(report.Hops) != 2 {
		t.Fatalf("应记录两跳: %d", len(report.Hops))
	}
	if report.Hops[0].Preflight == nil || !report.Hops[0].Preflight.OK {
		t.Fatal("第一跳预检应通过")
	}
	if report.Hops[1].Preflight == nil || report.Hops[1].Preflight.OK {
		t.Fatal("第二跳预检应失败")
	}
}

func TestRun303RewritesMethodButKeepsHeaders(t *testing.T) {
	in := &Input{
		Routes: []Route{
			{
				ID: "edge", Origin: "https://edge.example", Path: "/form",
				Preflight: &PreflightSpec{
					Status: 204,
					Headers: map[string]string{
						HeaderACAO: "https://app.example",
						HeaderACAM: "POST",
						HeaderACAH: "content-type",
					},
				},
				Reply: &ReplySpec{
					Status:   303,
					Location: "https://cdn.partner.example/a",
				},
			},
			{
				ID: "cdn", Origin: "https://cdn.partner.example", Path: "/a",
				Reply: &ReplySpec{
					Status:  200,
					Headers: map[string]string{HeaderACAO: "https://app.example"},
				},
			},
		},
		Requests: []Request{
			{
				ID: "303", AtSeconds: 0, Source: "https://app.example",
				URL: "https://edge.example/form", Method: "POST",
				Headers: map[string]string{"Content-Type": "application/json"},
			},
		},
	}

	result, err := Run(in)
	if err != nil {
		t.Fatal(err)
	}
	report := result.Requests[0]
	if report.Status != StatusBlocked {
		t.Fatalf("第二跳 GET + 非安全 content-type 仍需预检: %+v", report)
	}
	if report.Hops[1].Method != "GET" {
		t.Fatalf("303 应改写为 GET, got %s", report.Hops[1].Method)
	}
}

func TestRunSameOriginSkipsCORS(t *testing.T) {
	in := &Input{
		Routes: []Route{
			{
				ID: "same", Origin: "https://app.example", Path: "/x",
				Reply: &ReplySpec{Status: 200},
			},
		},
		Requests: []Request{
			{
				ID: "so", AtSeconds: 0, Source: "https://app.example",
				URL: "https://app.example/x", Method: "DELETE",
				Headers: map[string]string{"X-Whatever": "1"},
			},
		},
	}
	result, err := Run(in)
	if err != nil {
		t.Fatal(err)
	}
	report := result.Requests[0]
	if report.Status != StatusOK || !report.RequestSent || !report.ScriptReadable {
		t.Fatalf("同源请求应直接可读: %+v", report)
	}
	if report.Hops[0].Preflight != nil {
		t.Fatal("同源不应产生预检")
	}
}
