# navigator — embeddable navigation state machine

`navigator` is a browser-like request engine for a web-embeddable component.
It exists because `net/http.Client` treats every redirect as a fresh request
whose cookie decisions are made against the *last hop*, which lets a
cross-site embed silently ride the top-level page's login state.

## Model

- `Context` is the immutable initiator of a navigation:
  - `NewTopLevelContext` — the top-level document; its site is the
    site-for-cookies.
  - `NewEmbeddedContext` — an embedded resource; only the embedder's site is
    available, never the top-level login state.
  - `NewOpaqueEmbeddedContext` — sandboxed/opaque embed; every request is
    cross-site.
- `State` is the pure state machine (`init → issuing → redirect → … →
  done/error`). `State.advance` keeps the same `Context` for every hop and
  recomputes the cross-site flag from the whole chain.
- `Jar` is a SameSite-aware cookie store implementing Lax-by-default,
  `SameSite=None; Secure`, the two-minute Lax+POST intervention, domain/path
  matching and secure-context checks.
- `Navigator.Navigate` drives the machine hop by hop with redirect following
  disabled on the underlying `RoundTripper`, so the engine owns every
  method/body/cookie decision itself.

## Redirect and SameSite semantics

- 301/302/303 downgrade non-safe methods to GET and drop the body
  (RFC 9110 §15.4).
- 307/308 preserve the method and replay the body; a non-replayable body
  terminates the redirect instead of silently losing data.
- The cross-site flag follows RFC 6265bis §5.2: once any URL in the redirect
  chain is cross-site to the site-for-cookies, every later hop stays
  cross-site even if it returns to the original site.
- Cookies are recomputed per hop from the original `Context`. A redirect
  never promotes an embedded request to top-level and never treats the
  previous hop as the initiator.
- Cross-site redirects strip `Authorization`/`Proxy-Authorization`;
  `Cookie` is deliberately not stripped there because the jar re-derives it
  for each request.

## Verification

The dual-site suite (`navigator_test.go`) runs two cross-site HTTPS-style
origins behind an in-process `RoundTripper` (the sandbox forbids socket
binding; request headers are still built and recorded exactly as on the
wire). It asserts the real `Cookie` header for:

- top-level 307: POST method and body preserved, login cookie retained;
- top-level 303: POST downgraded to GET, cookies retained same-site;
- cross-site embedded 307 into the login site: Lax/Strict cookies withheld,
  `SameSite=None` still sent;
- widget → bank → widget chain: cross-site poisoning survives the return
  hop;
- cross-site top-level GET vs POST: Lax allowed only on the safe method.

Run:

```sh
go test ./... -race -cover
```
