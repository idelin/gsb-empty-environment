# jwtmw — JWT verification middleware for `net/http`

A dependency-free (standard library only) JWT authentication middleware for Go
HTTP gateways, backed by a self-refreshing JWKS endpoint.

## Why this exists

A verifier that picks the signature algorithm from the JWT header (`alg`) is
vulnerable to **algorithm confusion**: an attacker forges a token whose header
says `HS256`, and a naive verifier feeds the RSA **public key** into HMAC as a
shared secret, accepting the forged signature. The classic `alg: none` attack is
the same family of bug.

This package never trusts the header for algorithm selection.

## Security model

- **Algorithm is decided by the key type.** A JWK's `kty` determines which
  algorithms are even possible: RSA keys only verify `RS*`/`PS*`, EC keys only
  verify `ES*` (with a curve/hash size check), Ed25519 only verifies `EdDSA`.
  The header `alg` is then checked for compatibility with that key.
- **`alg: none` is always rejected**, including case variants, before any key
  lookup or network fetch.
- **Symmetric keys are never accepted.** `kty: oct` JWKs are dropped at parse
  time, so no public key can ever be misused as an HMAC secret. HMAC
  algorithms (`HS*`) are not in the accepted set at all.
- **JWK `alg` pinning** is honored: when a JWK carries an `alg`, the token must
  match it exactly.
- **An allowlist** (`Config.AllowedAlgs`) can further restrict accepted
  algorithms; symmetric values are rejected at construction.

## JWKS lifecycle

- **Lazy, coalesced refresh.** An unknown `kid` triggers a refresh. Concurrent
  requests share a single in-flight HTTP call (singleflight-style); they all
  wait on the same result instead of stampeding the identity provider.
- **Rate limiting.** While the cached document is still within its TTL,
  refresh attempts are limited to one per `MinRefreshInterval`. A genuinely
  expired cache and explicit `Refresh` calls bypass the limiter, so real key
  rotation is never blocked.
- **Rotation grace period.** Keys that disappear from a new JWKS document are
  retained in a retired state until `GracePeriod` elapses, keeping in-flight
  tokens valid during rotation, then evicted.
- **Fail safe on refresh errors.** A failed refresh while the cache is still
  fresh leaves the working cache untouched. Once the cache is stale and refresh
  fails, the middleware returns `503` (it never fails open or accepts
  unverified tokens).

## Status codes

- `401` for malformed tokens, bad signatures, unknown/throttled `kid`,
  `alg: none`, algorithm mismatch, and invalid claims.
- `503` when the JWKS service is unavailable and no unexpired cache exists.
- Both responses include a `WWW-Authenticate: Bearer` challenge.

## Usage

```go
verifier, err := jwtmw.New(jwtmw.Config{
    JWKSURI:            "https://issuer.example/.well-known/jwks.json",
    CacheTTL:           10 * time.Minute,
    GracePeriod:        time.Minute,
    MinRefreshInterval: 30 * time.Second,
    Issuer:             "https://issuer.example",
    Audience:           "api-gateway",
    Leeway:             5 * time.Second,
})
if err != nil {
    log.Fatal(err)
}

mux.Handle("/protected", verifier.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    claims, _ := jwtmw.ClaimsFromContext(r.Context())
    fmt.Fprintf(w, "hello %s", claims.Subject)
})))
```

## Tests

Tests run the full `net/http` client/server stack over in-memory `net.Pipe`
connections (no TCP ports required) and use an injectable fake clock for
deterministic time boundaries.

```sh
go test -race ./...
```

Key scenarios:

- `alg: none` (all case variants) rejected without any JWKS fetch.
- HMAC/RSA confusion using the public key DER and the modulus as the secret.
- Header `alg`/key-type mismatch and JWK `alg` pin enforcement.
- A 50-request unknown-`kid` storm coalescing into exactly one JWKS fetch.
- Rate-limit window behavior and its reset.
- Key rotation with grace-period acceptance at the boundary, then eviction.
- Refresh failures against an unexpired cache, and `503` once it expires.
- `exp`/`nbf` boundaries and clock-skew leeway.
