package svcgate

import (
	"testing"
)

func TestParseService(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		fullMethod string
		service    string
		ok         bool
	}{
		{name: "simple", fullMethod: "/pkg.Service/Method", service: "pkg.Service", ok: true},
		{name: "reflection", fullMethod: "/grpc.reflection.v1.ServerReflection/ServerReflectionInfo", service: "grpc.reflection.v1.ServerReflection", ok: true},
		{name: "health", fullMethod: "/grpc.health.v1.Health/Check", service: "grpc.health.v1.Health", ok: true},
		{name: "empty path", fullMethod: "", ok: false},
		{name: "missing leading slash", fullMethod: "pkg.Service/Method", ok: false},
		{name: "only leading slash", fullMethod: "/", ok: false},
		{name: "missing method", fullMethod: "/pkg.Service", ok: false},
		{name: "missing method with slash", fullMethod: "/pkg.Service/", ok: false},
		{name: "empty service", fullMethod: "//Method", ok: false},
		{name: "extra segment", fullMethod: "/pkg.Service/Sub/Method", ok: false},
		{name: "double slash before method", fullMethod: "/pkg.Service//Method", ok: false},
		{name: "trailing slash", fullMethod: "/pkg.Service/Method/", ok: false},
		{name: "encoded path", fullMethod: "/grpc.reflection.v1.ServerReflection/Server%20Reflection", ok: false},
		{name: "encoded service", fullMethod: "/grpc%2Ereflection.v1.ServerReflection/Info", ok: false},
		{name: "uppercase reflection", fullMethod: "/GRPC.REFLECTION.V1.ServerReflection/Info", service: "GRPC.REFLECTION.V1.ServerReflection", ok: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseService(tc.fullMethod)
			if ok != tc.ok || got != tc.service {
				t.Fatalf("parseService(%q) = %q, %v; want %q, %v", tc.fullMethod, got, ok, tc.service, tc.ok)
			}
		})
	}
}

func TestServiceGated(t *testing.T) {
	t.Parallel()
	gated := []string{
		"grpc.reflection.v1.ServerReflection",
		"grpc.reflection.v1alpha.ServerReflection",
		"grpc.channelz.v1.Channelz",
		"grpc.admin.v1.AdminService",
		"grpc.testing.TestService",
		"grpc.health.v1.Health",
	}
	for _, service := range gated {
		if !serviceGated(service) {
			t.Errorf("serviceGated(%q) = false; want true", service)
		}
	}
	open := []string{
		"example.echo.Echo",
		"GRPC.REFLECTION.V1.ServerReflection",
		"Grpc.Reflection.V1.ServerReflection",
		"grpc.health.v1.HealthCheck",
		"grpc.health.v2.Health",
	}
	for _, service := range open {
		if serviceGated(service) {
			t.Errorf("serviceGated(%q) = true; want false", service)
		}
	}
}
