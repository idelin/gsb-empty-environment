# CORS preflight layer

`cors` is a network-layer CORS implementation for a Go browser-testing
framework. It wraps any `http.RoundTripper` and applies the Fetch standard's
CORS protocol, including the **CORS-preflight cache**, to requests that carry
a browsing-context `Policy`.

## The vulnerability it fixes

The previous preflight cache keyed decisions on too little information, so a
preflight answer obtained for one identity could be replayed for another
(cross-tenant / cross-credential authorization bypass). Redirects and policy
revocations could likewise inherit a decision that no longer applied.

## Cache identity

A cached decision is shared only when **all** of the following match:

| Component | Source |
| --- | --- |
| source origin | `Policy.Origin` |
| target origin | request URL scheme + host + port |
| credentials mode | `ModeOmit` / `ModeInclude` |

Each entry additionally records the permitted **methods** and **headers** and
its own `Access-Control-Max-Age` expiry, so even within one identity tuple a
new method or header forces a fresh preflight. Failed preflights are never
cached.

Security-relevant rules:

- `Access-Control-Allow-Origin: *` is accepted only in anonymous
  (`ModeOmit`) mode; credentialed requests require an exact echoed origin plus
  `Access-Control-Allow-Credentials: true`.
- `Access-Control-Allow-Headers: *` matches any header only in anonymous mode.
- Forbidden request headers (`Origin`, `Referer`, `Cookie`, `Host`, ...) are
  excluded from the CORS check, matching the Fetch standard. This matters when
  `net/http` adds a `Referer` while following a cross-origin redirect.
- `Access-Control-Max-Age` defaults to 5 seconds when absent and is capped at
  7200 seconds.
- Concurrent requests with the same identity tuple are coalesced onto one
  preflight; different tuples are never coalesced.

## Redirects and revocation

- `http.Client` invokes the transport once per redirect hop, and each hop is
  evaluated against its own target-origin cache key. A decision taken for the
  entry server can never authorize the redirect target.
- `Invalidate(origin, target, mode)`, `InvalidateOrigin(origin)` and `Clear()`
  drop cached decisions so the next request re-validates with the server after
  a policy change.

## Usage

```go
transport := cors.NewCORSTransport(cors.Config{Base: http.DefaultTransport})
client := &http.Client{Transport: transport}

ctx := cors.WithPolicy(req.Context(), cors.Policy{
    Origin: "https://app.example",
    Mode:   cors.ModeInclude,
})
req = req.WithContext(ctx)
resp, err := client.Do(req)
```

## Tests

The tests run a real HTTP/1.1 server (`http.Server`) over in-process
`net.Pipe` connections (no kernel sockets required by the sandbox) and assert
on separate preflight (`OPTIONS`) and business-request counters:

- anonymous wildcard caching and credentialed wildcard rejection
- origin, target and credentials-mode partitioning (cross-identity regression)
- method/header permission boundaries
- expiry with an injected clock and with the real wall clock
- concurrent coalescing and non-coalescing of distinct identities
- revocation via `Invalidate`, `InvalidateOrigin` and `Clear`
- cross-origin redirect re-preflight and denial on the second hop
- denial not being cached, max-age capping, content-type safelisting

```sh
go test ./... -race -count=1
```
