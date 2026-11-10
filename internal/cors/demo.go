package cors

// DemoInput 构造三组可分辨“两步判定”的内置复盘场景。
func DemoInput() *Input {
	return &Input{
		PreflightEnforcement: EnforcementModern,
		Routes:               demoRoutes(),
		Requests:             demoRequests(),
	}
}

func demoRoutes() []Route {
	return []Route{
		// —— 场景一：通配源与凭据 ——
		{
			ID:     "public-api",
			Origin: "https://api.target.example",
			Path:   "/public",
			Preflight: &PreflightSpec{
				Status: 204,
				Headers: map[string]string{
					HeaderACAO: "*",
					HeaderACAM: "GET, POST, PUT",
					HeaderACAH: "*",
					HeaderACMA: "600",
				},
			},
			Reply: &ReplySpec{
				Status:  200,
				Headers: map[string]string{HeaderACAO: "*"},
				Body:    "public payload",
			},
		},
		{
			ID:     "null-echo",
			Origin: "https://api.target.example",
			Path:   "/null-echo",
			Reply: &ReplySpec{
				Status:  200,
				Headers: map[string]string{HeaderACAO: "null"},
			},
		},

		// —— 场景二：预检过期 + 缓存键隔离 ——
		{
			ID:     "orders",
			Origin: "https://api.target.example",
			Path:   "/orders",
			Preflight: &PreflightSpec{
				Status: 204,
				Headers: map[string]string{
					HeaderACAO: "https://shop.example",
					HeaderACAM: "PUT",
					HeaderACAH: "content-type, x-custom",
					HeaderACAC: "true",
					HeaderACMA: "10",
				},
				// t=30 后服务端“变脸”：不再授权 x-custom。
				Changes: []PreflightChange{
					{
						AfterSeconds: 30,
						Replace:      true,
						Headers: map[string]string{
							HeaderACAO: "https://shop.example",
							HeaderACAM: "PUT",
							HeaderACAH: "content-type",
							HeaderACAC: "true",
							HeaderACMA: "10",
						},
					},
				},
			},
			Reply: &ReplySpec{
				Status: 200,
				Headers: map[string]string{
					HeaderACAO: "https://shop.example",
					HeaderACAC: "true",
				},
			},
		},
		{
			ID:     "orders-details",
			Origin: "https://api.target.example",
			Path:   "/orders/details",
			Preflight: &PreflightSpec{
				Status: 204,
				Headers: map[string]string{
					HeaderACAO: "https://shop.example",
					HeaderACAM: "PUT",
					HeaderACAH: "content-type",
					HeaderACAC: "true",
					HeaderACMA: "120",
				},
			},
			Reply: &ReplySpec{
				Status: 200,
				Headers: map[string]string{
					HeaderACAO: "https://shop.example",
					HeaderACAC: "true",
				},
			},
		},

		// —— 场景三：重定向换源 ——
		{
			ID:     "edge-start",
			Origin: "https://edge.example",
			Path:   "/start",
			Reply:  &ReplySpec{Status: 302, Location: "https://cdn.partner.example/assets/a.json"},
		},
		{
			ID:     "edge-upload",
			Origin: "https://edge.example",
			Path:   "/upload-redirect",
			Preflight: &PreflightSpec{
				Status: 204,
				Headers: map[string]string{
					HeaderACAO: "https://app.example",
					HeaderACAM: "PUT",
					HeaderACAH: "x-custom",
					HeaderACMA: "60",
				},
			},
			Reply: &ReplySpec{Status: 307, Location: "https://cdn.partner.example/upload"},
		},
		{
			ID:     "edge-form",
			Origin: "https://edge.example",
			Path:   "/form",
			Preflight: &PreflightSpec{
				Status: 204,
				Headers: map[string]string{
					HeaderACAO: "https://app.example",
					HeaderACAM: "POST",
					HeaderACAH: "content-type",
				},
			},
			Reply: &ReplySpec{Status: 303, Location: "https://cdn.partner.example/assets/a.json"},
		},
		{
			ID:     "cdn-asset",
			Origin: "https://cdn.partner.example",
			Path:   "/assets/*",
			Reply: &ReplySpec{
				Status:  200,
				Headers: map[string]string{HeaderACAO: "https://app.example"},
			},
		},
		{
			ID:     "cdn-upload",
			Origin: "https://cdn.partner.example",
			Path:   "/upload",
			Reply: &ReplySpec{
				Status:  201,
				Headers: map[string]string{HeaderACAO: "https://app.example"},
			},
		},
	}
}

func demoRequests() []Request {
	return []Request{
		// 场景一：通配源 + 凭据
		{
			ID: "w1", AtSeconds: 0, Source: "https://shop.example",
			URL: "https://api.target.example/public", Method: "GET",
			Note: "简单 GET + 通配 ACAO：请求发出且可读",
		},
		{
			ID: "w2", AtSeconds: 1, Source: "https://shop.example",
			URL: "https://api.target.example/public", Method: "PUT",
			Headers: map[string]string{"X-Custom": "1"}, Credentials: CredentialsInclude,
			Note: "带凭据的非简单请求：预检见 ACAO:* 直接失败，请求未发出",
		},
		{
			ID: "w3", AtSeconds: 2, Source: "https://shop.example",
			URL: "https://api.target.example/public", Method: "GET", Credentials: CredentialsInclude,
			Note: "带凭据的简单 GET：无预检、请求已发出，但 ACAO:* 导致响应不透明",
		},
		{
			ID: "w4", AtSeconds: 3, Source: "null",
			URL: "https://api.target.example/null-echo", Method: "GET",
			Note: "不透明源需要显式回显 null：可读",
		},
		{
			ID: "w5", AtSeconds: 4, Source: "null",
			URL: "https://api.target.example/public", Method: "GET",
			Note: "通配符不匹配不透明源：响应不透明",
		},

		// 场景二：预检过期 + 缓存隔离
		{
			ID: "p1", AtSeconds: 10, Source: "https://shop.example",
			URL: "https://api.target.example/orders", Method: "PUT",
			Headers:     map[string]string{"Content-Type": "application/json", "X-Custom": "1"},
			Credentials: CredentialsInclude,
			Note:        "首次预检通过并缓存（Max-Age=10）",
		},
		{
			ID: "p2", AtSeconds: 12, Source: "https://shop.example",
			URL: "https://api.target.example/orders", Method: "PUT",
			Headers:     map[string]string{"Content-Type": "application/json", "X-Custom": "1"},
			Credentials: CredentialsInclude,
			Note:        "缓存命中，不发预检",
		},
		{
			ID: "p3", AtSeconds: 22, Source: "https://shop.example",
			URL: "https://api.target.example/orders", Method: "PUT",
			Headers:     map[string]string{"Content-Type": "application/json", "X-Custom": "1"},
			Credentials: CredentialsInclude,
			Note:        "t=20 缓存过期，重新预检；服务端尚未变脸，再次通过并刷新到 t=32",
		},
		{
			ID: "p4", AtSeconds: 40, Source: "https://shop.example",
			URL: "https://api.target.example/orders", Method: "PUT",
			Headers:     map[string]string{"Content-Type": "application/json", "X-Custom": "1"},
			Credentials: CredentialsInclude,
			Note:        "t=32 再次过期后预检：服务端已在 t=30 变脸撤权 x-custom，预检失败、请求未发出",
		},
		{
			ID: "p5", AtSeconds: 42, Source: "https://other.example",
			URL: "https://api.target.example/orders", Method: "PUT",
			Headers:     map[string]string{"Content-Type": "application/json"},
			Credentials: CredentialsInclude,
			Note:        "发起源不同 → 独立缓存键，必然重新预检并因 ACAO 不匹配失败",
		},
		{
			ID: "p6", AtSeconds: 44, Source: "https://shop.example",
			URL: "https://api.target.example/orders", Method: "PUT",
			Headers: map[string]string{"Content-Type": "application/json", "X-Custom": "1"},
			Note:    "同目标但凭据模式 omit → 与 include 分属不同缓存条目，同样独立预检",
		},
		{
			ID: "p7", AtSeconds: 46, Source: "https://shop.example",
			URL: "https://api.target.example/orders", Method: "PUT",
			Headers:     map[string]string{"Content-Type": "application/json", "X-Admin": "1"},
			Credentials: CredentialsInclude,
			Note:        "新头不在缓存授权集合内 → 重新预检，x-admin 未被允许而失败（含通配凭据规则）",
		},
		{
			ID: "p8", AtSeconds: 48, Source: "https://shop.example",
			URL: "https://api.target.example/orders/details", Method: "PUT",
			Headers:     map[string]string{"Content-Type": "application/json"},
			Credentials: CredentialsInclude,
			Note:        "不同目标 URL → 独立缓存键；该条目在终态快照中仍存活，便于对比隔离与过期",
		},

		// 场景三：重定向换源
		{
			ID: "r1", AtSeconds: 50, Source: "https://app.example",
			URL: "https://edge.example/start", Method: "GET",
			Note: "简单请求跟随 302 到 CDN：中间跳转不做 CORS，终态 ACAO 匹配 → 可读",
		},
		{
			ID: "r2", AtSeconds: 51, Source: "https://app.example",
			URL: "https://edge.example/upload-redirect", Method: "PUT",
			Headers: map[string]string{"X-Custom": "1"},
			Note:    "307 保留方法与头：edge 预检通过后，CDN 一跳仍需独立预检，缺失即阻断",
		},
		{
			ID: "r3", AtSeconds: 52, Source: "https://app.example",
			URL: "https://edge.example/form", Method: "POST",
			Headers: map[string]string{"Content-Type": "application/json"},
			Note:    "303 改 GET 但保留请求头：CDN 上 GET+content-type 仍触发预检，失败阻断",
		},
		{
			ID: "r4", AtSeconds: 53, Source: "https://edge.example",
			URL: "https://edge.example/start", Method: "GET",
			Note: "首跳同源免 CORS；换源后发起源仍是 edge，CDN 的 ACAO 不匹配 → 不透明",
		},
	}
}
