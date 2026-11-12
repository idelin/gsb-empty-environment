package svcgate

import "testing"

// These tests exercise only the path parsing and matching rules and have no
// gRPC dependencies, so they run in offline environments as well.

func TestMalformedPathsAreRejected(t *testing.T) {
	t.Parallel()
	malformed := []string{
		"",
		"pkg.Service/Method",
		"/",
		"/pkg.Service",
		"/pkg.Service/",
		"//Method",
		"/pkg.Service/Sub/Method",
		"/pkg.Service//Method",
		"/pkg.Service/Method/",
		"/grpc%2Ereflection.v1.ServerReflection/Info",
		"/grpc.reflection.v1.ServerReflection/Server%20Reflection",
		"/grpc.health.v1.Health/Che%63k",
	}
	for _, fullMethod := range malformed {
		if _, ok := parseService(fullMethod); ok {
			t.Errorf("parseService(%q) unexpectedly succeeded", fullMethod)
		}
	}
}

func TestWellFormedPathsExtractService(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"/pkg.Service/Method": "pkg.Service",
		"/grpc.reflection.v1.ServerReflection/ServerReflectionInfo": "grpc.reflection.v1.ServerReflection",
		"/GRPC.REFLECTION.V1.ServerReflection/Info":                 "GRPC.REFLECTION.V1.ServerReflection",
		"/grpc.health.v1.Health/Check":                              "grpc.health.v1.Health",
	}
	for fullMethod, want := range cases {
		got, ok := parseService(fullMethod)
		if !ok || got != want {
			t.Errorf("parseService(%q) = %q, %v; want %q, true", fullMethod, got, ok, want)
		}
	}
}
