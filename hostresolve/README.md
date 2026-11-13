# hostresolve

`hostresolve` resolves the authoritative host of an HTTP/1.1 request into one
canonical key that routing, caching and authentication share. It implements
the authority rules of RFC 9110 §7.2 and RFC 9112 §3.2.2 and closes the
confusion where a request carrying different hosts in the request line and
the `Host` header can be routed by one value and cached/authenticated by
another, enabling cross-tenant cache poisoning.

## Rules

- Absolute-form request target (`GET https://tenant.example/p HTTP/1.1`): the
  authority in the target wins; `Host` is structurally validated but ignored
  for routing.
- Origin-form request target (`GET /p HTTP/1.1`): the validated `Host`
  header is the authority.
- Rejected with `400 Bad Request`: missing `Host`, more than one `Host`
  field line, a comma-separated `Host`, `userinfo@`, and path/query/fragment
  characters in the authority.
- `X-Forwarded-Host` overrides the resolved authority only when the direct
  peer IP passes the configured `IsTrustedProxy` check; from every other
  peer the header is ignored. Malformed forwarding headers from a trusted
  proxy are rejected.
- The returned `Result.Key` is canonical (lower-cased DNS names, canonical IP
  literals, normalized IPv6 bracketing) and is both the route key and the
  cache key.

## Usage

```go
package main

import (
	"log"
	"net"
	"net/http"

	"example.org/netsec/q0812/hostresolve"
)

func main() {
	resolver := &hostresolve.Resolver{
		IsTrustedProxy: func(ip net.IP) bool {
			return ip.IsLoopback() // replace with your proxy allowlist
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		res := hostresolve.FromContext(req.Context())
		// Use the same key for routing lookups, cache entries and auth checks.
		routeKey := res.RouteKey()
		cacheKey := res.CacheKey()
		_ = routeKey
		_ = cacheKey
	})

	log.Fatal(http.ListenAndServe(":8080", resolver.Middleware(mux)))
}
```

Outside middleware, resolve explicitly:

```go
res, err := resolver.ResolveRequest(req)
if err != nil {
	// All errors are 400-class; see the exported sentinel errors.
	http.Error(w, "Bad Request", http.StatusBadRequest)
	return
}
```

Custom frontends (or tests) can call `Resolver.Resolve` with the transport
level `hostresolve.Request`, which keeps the raw request target and every
duplicate header line.

## Error matching

All failures wrap one of the exported sentinel errors — `ErrMissingHost`,
`ErrDuplicateHost`, `ErrInvalidHost`, `ErrInvalidRequestTarget` — so callers
can classify with `errors.Is`.

## Tests

`go test ./...` covers the required conflict, duplicate-header and
untrusted-forwarding cases both as unit tests and through a real HTTP/1.1
connection state machine (in-memory `net.Pipe`, so no socket privileges are
needed).
