package netsec

import "testing"

func TestClassifyPathValid(t *testing.T) {
	cases := map[string]string{
		"/grpc.health.v1.Health/Check":              "grpc.health.v1.Health",
		"/grpc.reflection.v1.ServerReflection/Info": "grpc.reflection.v1.ServerReflection",
		"/echo.v1.Echo/Ping":                        "echo.v1.Echo",
		"/a/b":                                      "a",
		"/svc/method-with.dots_and-dashes":          "svc",
	}
	for path, want := range cases {
		service, ok := classifyPath(path)
		if !ok {
			t.Errorf("classifyPath(%q) rejected a valid path", path)
			continue
		}
		if service != want {
			t.Errorf("classifyPath(%q) service = %q, want %q", path, service, want)
		}
	}
}

func TestClassifyPathInvalid(t *testing.T) {
	paths := []string{
		"",                                   // 空路径
		"/",                                  // 只有分隔符
		"noslash",                            // 没有前导斜杠
		"svc/method",                         // 缺少前导斜杠
		"/svc",                               // 缺少方法名
		"//Check",                            // 空服务名
		"/grpc.health.v1.Health/",            // 空方法名
		"/a/b/c",                             // 多余路径段
		"/grpc.health.v1.Health/Check/Extra", // 多余路径段
		"/svc/m%65thod",                      // 方法名百分号编码
		"/svc%2ename/method",                 // 服务名百分号编码
		"/%73vc/method",                      // 服务名百分号编码
		"/svc/method%2f..%2f",                // 编码的路径穿越
		"/svc/method?x=1%2f",                 // 含百分号的查询样式后缀
	}
	for _, path := range paths {
		if service, ok := classifyPath(path); ok {
			t.Errorf("classifyPath(%q) = %q, true; want rejected", path, service)
		}
	}
}

func TestIsGatedService(t *testing.T) {
	gated := []string{
		"grpc.reflection.v1.ServerReflection",
		"grpc.reflection.v1alpha.ServerReflection",
		"grpc.channelz.v1.Channelz",
		"grpc.admin.v1.Admin",
		"grpc.testing.TestService",
		"grpc.health.v1.Health",
	}
	for _, service := range gated {
		if !isGatedService(service) {
			t.Errorf("isGatedService(%q) = false, want true", service)
		}
	}

	open := []string{
		"GRPC.REFLECTION.v1.ServerReflection", // 大小写变体不在门控名单
		"Grpc.Health.V1.Health",
		"grpc.reflection",        // 缺少包名后缀的点号
		"grpc.health.v1.Health2", // 健康检查仅精确匹配
		"grpc.health.v1.HealthWatch",
		"echo.v1.Echo",
		"my.grpc.reflection.v1.Foo", // 前缀必须从头匹配
	}
	for _, service := range open {
		if isGatedService(service) {
			t.Errorf("isGatedService(%q) = true, want false", service)
		}
	}
}
