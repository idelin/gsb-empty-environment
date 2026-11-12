package svcgate

import "strings"

// blockedPrefixes lists gRPC service-name prefixes that must never be served
// on externally exposed listeners. Matching is case-sensitive, so uppercase
// variants such as "GRPC.REFLECTION.Foo" do not match and instead fall
// through to the normal unknown-service handling, which also yields
// codes.Unimplemented.
var blockedPrefixes = []string{
	"grpc.reflection.",
	"grpc.channelz.",
	"grpc.admin.",
	"grpc.testing.",
}

// blockedExactServices lists fully qualified service names that must never be
// served on externally exposed listeners.
var blockedExactServices = map[string]struct{}{
	"grpc.health.v1.Health": {},
}

// parseService splits a gRPC full method, which is the value of the HTTP/2
// ":path" header, into its service name. The only accepted shape is
// "/<service>/<method>" with exactly two non-empty segments and no percent
// encoding. Everything else is rejected: paths without a leading slash,
// empty service or method names, extra segments and percent-encoded bytes.
// The service name is the text between the first "/" and the last "/".
func parseService(fullMethod string) (service string, ok bool) {
	if len(fullMethod) == 0 || fullMethod[0] != '/' {
		return "", false
	}
	rest := fullMethod[1:]
	first := strings.IndexByte(rest, '/')
	last := strings.LastIndexByte(rest, '/')
	if first < 0 || first != last || first == 0 || first == len(rest)-1 {
		return "", false
	}
	service = rest[:first]
	method := rest[first+1:]
	if strings.IndexByte(service, '%') >= 0 || strings.IndexByte(method, '%') >= 0 {
		return "", false
	}
	return service, true
}

// serviceGated reports whether service is one of the internal-only services.
// The comparison is case-sensitive by design.
func serviceGated(service string) bool {
	for _, prefix := range blockedPrefixes {
		if strings.HasPrefix(service, prefix) {
			return true
		}
	}
	_, exact := blockedExactServices[service]
	return exact
}
