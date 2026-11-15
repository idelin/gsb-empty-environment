package pathclass

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		path string
		want Verdict
	}{
		// 受保护服务：精确匹配。
		{"/grpc.reflection.v1.ServerReflection/ServerReflectionInfo", Protected},
		{"/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo", Protected},
		{"/grpc.channelz.v1.Channelz/GetServers", Protected},
		{"/grpc.admin.v1.Admin/Reload", Protected},
		{"/grpc.testing.TestService/UnaryCall", Protected},
		{"/grpc.testing.TestService/StreamingCall", Protected},
		{"/grpc.health.v1.Health/Check", Protected},
		{"/grpc.health.v1.Health/Watch", Protected},

		// 普通服务放行。
		{"/example.ping.v1.Ping/Ping", Allow},
		{"/grpc.health.v2.Health/Check", Allow},      // 仅 v1 受保护
		{"/grpc.reflection/List", Allow},             // 缺少子包名，非受保护前缀
		{"/mygrpc.testing.Fake/Call", Allow},         // 前缀必须从头匹配
		{"/grpc.health.v1.HealthCheck/Check", Allow}, // 非精确全名

		// 大小写变体：一律拒绝。
		{"/GRPC.REFLECTION.v1.ServerReflection/ServerReflectionInfo", Reject},
		{"/Grpc.Health.V1.Health/Check", Reject},
		{"/gRPC.testing.TestService/UnaryCall", Reject},
		{"/GRPC.CHANNELZ.V1.CHANNELZ/GetServers", Reject},

		// 非法路径：多余段、百分号编码、空服务名、空方法名、缺前导斜杠。
		{"", Reject},
		{"/", Reject},
		{"grpc.health.v1.Health/Check", Reject},
		{"/grpc.health.v1.Health", Reject},
		{"//Check", Reject},
		{"/grpc.health.v1.Health/", Reject},
		{"/grpc.health.v1.Health/Check/Extra", Reject},
		{"/a/b/c", Reject},
		{"/grpc.health.v1.Health%2fX/Check", Reject},
		{"/grpc.health.v1.Health/Che%63k", Reject},
		{"/%2e%2e/admin/Reload", Reject},
	}
	for _, c := range cases {
		if got := Classify(c.path); got != c.want {
			t.Errorf("Classify(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
