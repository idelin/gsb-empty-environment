# DPoP proof binding to token cnf.jkt

An importable Go library for resource servers. It verifies a DPoP proof and
refuses to authorize the request unless the proof key is the key bound to the
access token:

- The token must contain a non-empty `cnf.jkt`.
- The proof header must contain a public JWK, and its RFC 7638 thumbprint must
  exactly equal the token's `cnf.jkt`.
- The proof signature, `htm`, `htu`, and `iat` must all be valid.
- A token without `cnf.jkt` is rejected even when the proof carries a perfectly
  valid key.
- Replacing only the proof key while keeping the original token is rejected
  with `ErrBindingMismatch`.

## Usage

The resource server first authenticates the access token with its issuer (the
library parses token claims but does not have the token issuer's signing key).
It then validates the proof and binding:

```go
import netsec "example.org/netsec/q0165"

result, err := netsec.ValidateRequest(r, accessToken)
if err != nil {
    http.Error(w, "invalid DPoP request", http.StatusUnauthorized)
    return
}

// result.JKT is the thumbprint shared by the proof JWK and cnf.jkt.
// result.ProofClaims contains the verified proof claims.
```

If the headers are not on an `*http.Request`, use the explicit form:

```go
result, err := netsec.ValidateDPoP(
    accessToken,
    proofJWT,
    http.MethodGet,
    "https://api.example.com/orders",
)
```

`NewValidator` accepts options for proof age, clock skew, required `ath`, and
an optional replay cache:

```go
validator := netsec.NewValidator(
    netsec.WithMaxAge(2*time.Minute),
    netsec.WithRequireATH(true),
    netsec.WithReplayCache(netsec.NewMemoryReplayCache()),
)
result, err := validator.ValidateRequest(r, "")
```

Supported proof algorithms are `ES256`, `ES384`, `ES512`, `RS256`, `RS384`,
`RS512`, `PS256`, `PS384`, `PS512`, and `EdDSA` with Ed25519.

Run the regression suite with:

```sh
go test ./...
```
