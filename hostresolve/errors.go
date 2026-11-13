package hostresolve

import "errors"

// Source identifies where the resolved authority came from.
type Source int

const (
	// SourceUnknown is the zero value and is never returned by a successful
	// resolution.
	SourceUnknown Source = iota
	// SourceHost means the authority came from the Host header of an
	// origin-form request.
	SourceHost
	// SourceRequestTarget means the authority came from the absolute-form
	// request target; the Host header was ignored for routing.
	SourceRequestTarget
	// SourceForwardedHost means a trusted proxy's X-Forwarded-Host header
	// overrode the otherwise resolved authority.
	SourceForwardedHost
)

// String returns the wire-level name of the authority source.
func (s Source) String() string {
	switch s {
	case SourceHost:
		return "Host"
	case SourceRequestTarget:
		return "request-target"
	case SourceForwardedHost:
		return "X-Forwarded-Host"
	default:
		return "unknown"
	}
}

// Sentinel errors. Wrap them with fmt.Errorf("%w: ...") and match with
// errors.Is. All of them represent a client-side mistake and should be
// answered with HTTP 400 (see [Resolver.Middleware]).
var (
	// ErrMissingHost is returned when no Host header was sent.
	ErrMissingHost = errors.New("hostresolve: missing Host header")
	// ErrDuplicateHost is returned when more than one Host field line, or a
	// single field line containing a comma-separated list, was sent.
	ErrDuplicateHost = errors.New("hostresolve: duplicate Host header")
	// ErrInvalidHost is returned when a Host or X-Forwarded-Host value cannot
	// be parsed as a single host[:port] authority, including userinfo and
	// path/query/fragment characters.
	ErrInvalidHost = errors.New("hostresolve: invalid host authority")
	// ErrInvalidRequestTarget is returned when the request target is malformed
	// or uses a form that carries no routable authority for this server.
	ErrInvalidRequestTarget = errors.New("hostresolve: invalid request target")
)
