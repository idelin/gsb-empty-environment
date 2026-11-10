package cors

import "testing"

func TestParseOriginAndSameOrigin(t *testing.T) {
	shop, err := ParseOrigin("https://shop.example:443")
	if err != nil {
		t.Fatal(err)
	}
	shopDefaultPort, _ := ParseOrigin("https://shop.example")
	if !shop.sameAs(shopDefaultPort) {
		t.Fatal("443 显式端口应与默认端口同源")
	}
	other, _ := ParseOrigin("https://other.example")
	if shop.sameAs(other) {
		t.Fatal("不同 host 不应同源")
	}
	opaque, err := ParseOrigin("null")
	if err != nil || !opaque.Opaque {
		t.Fatalf("null 应解析为不透明源: %+v err=%v", opaque, err)
	}
	if opaque.sameAs(opaque) {
		t.Fatal("不透明源之间永不互为同源")
	}
	if _, err := ParseOrigin("not-a-url"); err == nil {
		t.Fatal("非法源应报错")
	}
}

func TestNeedsPreflight(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		headers map[string]string
		want    bool
	}{
		{"简单 GET", "GET", nil, false},
		{"简单 POST 表单", "POST", map[string]string{"Content-Type": "text/plain"}, false},
		{"JSON POST 触发预检", "POST", map[string]string{"Content-Type": "application/json"}, true},
		{"PUT 触发预检", "PUT", nil, true},
		{"自定义头触发预检", "GET", map[string]string{"X-Trace": "abc"}, true},
		{"Origin 头不触发预检", "GET", map[string]string{"Origin": "https://shop.example"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NeedsPreflight(tc.method, tc.headers); got != tc.want {
				t.Fatalf("NeedsPreflight(%s)=%v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestOriginAllowed(t *testing.T) {
	shop, _ := ParseOrigin("https://shop.example")
	opaque, _ := ParseOrigin("null")
	cases := []struct {
		acao   string
		source Origin
		want   bool
	}{
		{"*", shop, true},
		{"*", opaque, false},
		{"null", opaque, true},
		{"null", shop, false},
		{"https://shop.example", shop, true},
		{"https://other.example", shop, false},
	}
	for _, tc := range cases {
		if got := originAllowed(tc.acao, tc.source); got != tc.want {
			t.Errorf("originAllowed(%q, %s)=%v, want %v", tc.acao, tc.source, got, tc.want)
		}
	}
}

func TestValidatePreflight(t *testing.T) {
	source, _ := ParseOrigin("https://shop.example")
	base := map[string]string{
		HeaderACAO: "https://shop.example",
		HeaderACAM: "PUT",
		HeaderACAH: "content-type, x-custom",
		HeaderACAC: "true",
		HeaderACMA: "10",
	}

	t.Run("完整通过", func(t *testing.T) {
		v := ValidatePreflight(204, base, source, "PUT",
			[]string{"content-type", "x-custom"}, true, EnforcementModern, 7200)
		if !v.OK || v.MaxAgeSeconds != 10 {
			t.Fatalf("应通过且 Max-Age=10: %+v", v)
		}
	})

	t.Run("非2xx状态失败", func(t *testing.T) {
		v := ValidatePreflight(403, base, source, "PUT", nil, false, EnforcementModern, 7200)
		if v.OK {
			t.Fatal("403 预检不应通过")
		}
	})

	t.Run("通配源配凭据失败", func(t *testing.T) {
		headers := map[string]string{
			HeaderACAO: "*",
			HeaderACAM: "*",
			HeaderACAH: "*",
		}
		v := ValidatePreflight(204, headers, source, "PUT",
			[]string{"x-custom"}, true, EnforcementModern, 7200)
		if v.OK {
			t.Fatal("凭据预检不允许通配 ACAO/ACAM/ACAH")
		}
	})

	t.Run("非凭据通配通过", func(t *testing.T) {
		headers := map[string]string{
			HeaderACAO: "*",
			HeaderACAM: "*",
			HeaderACAH: "*",
		}
		v := ValidatePreflight(204, headers, source, "PUT",
			[]string{"x-custom"}, false, EnforcementModern, 7200)
		if !v.OK {
			t.Fatalf("非凭据通配应通过: %+v", v.FailureReasons)
		}
	})

	t.Run("方法未授权", func(t *testing.T) {
		v := ValidatePreflight(204, base, source, "DELETE",
			nil, true, EnforcementModern, 7200)
		if v.OK {
			t.Fatal("DELETE 不在 ACAM 中应失败")
		}
	})

	t.Run("头未授权", func(t *testing.T) {
		v := ValidatePreflight(204, base, source, "PUT",
			[]string{"x-admin"}, true, EnforcementModern, 7200)
		if v.OK {
			t.Fatal("x-admin 不在 ACAH 中应失败")
		}
	})

	t.Run("Max-Age 被上限裁剪", func(t *testing.T) {
		headers := map[string]string{
			HeaderACAO: "https://shop.example",
			HeaderACAM: "PUT",
			HeaderACMA: "99999",
		}
		v := ValidatePreflight(204, headers, source, "PUT", nil, false, EnforcementModern, 60)
		if v.MaxAgeSeconds != 60 {
			t.Fatalf("Max-Age 应裁剪到 60, got %d", v.MaxAgeSeconds)
		}
	})

	t.Run("legacy 非凭据不强制 ACAO", func(t *testing.T) {
		headers := map[string]string{
			HeaderACAM: "PUT",
		}
		v := ValidatePreflight(204, headers, source, "PUT", nil, false, EnforcementLegacy, 7200)
		if !v.OK {
			t.Fatalf("legacy 非凭据预检不应强制 ACAO: %+v", v.FailureReasons)
		}
		v2 := ValidatePreflight(204, headers, source, "PUT", nil, true, EnforcementLegacy, 7200)
		if v2.OK {
			t.Fatal("legacy 凭据预检仍应强制 ACAO")
		}
	})
}

func TestValidateActual(t *testing.T) {
	source, _ := ParseOrigin("https://shop.example")

	t.Run("通配无凭据可读", func(t *testing.T) {
		v := ValidateActual(200, map[string]string{HeaderACAO: "*"}, source, false, nil)
		if !v.Readable || !v.BodyReadable {
			t.Fatal("无凭据通配响应应可读")
		}
	})

	t.Run("通配配凭据不透明", func(t *testing.T) {
		v := ValidateActual(200, map[string]string{HeaderACAO: "*"}, source, true, nil)
		if v.Readable {
			t.Fatal("凭据响应不接受通配 ACAO")
		}
	})

	t.Run("精确源+ACAC可读", func(t *testing.T) {
		headers := map[string]string{
			HeaderACAO: "https://shop.example",
			HeaderACAC: "true",
			HeaderACEH: "x-trace",
		}
		v := ValidateActual(200, headers, source, true, map[string]string{"X-Trace": "1"})
		if !v.Readable {
			t.Fatalf("精确回显+ACAC 应可读: %+v", v.FailureReasons)
		}
		found := false
		for _, h := range v.ExposedHeaders {
			if h == "x-trace" {
				found = true
			}
		}
		if !found {
			t.Fatalf("ACEH 中的 x-trace 应可读: %v", v.ExposedHeaders)
		}
	})

	t.Run("缺少ACAO不透明", func(t *testing.T) {
		v := ValidateActual(500, map[string]string{}, source, false, nil)
		if v.Readable || v.Status != 500 {
			t.Fatal("无 ACAO 的 500 响应应对脚本不透明")
		}
	})
}
