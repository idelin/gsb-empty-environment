# RFC 9101 Request Object Parser

This module implements a Go request-object parser for an OAuth 2.0 /
OpenID Connect authorization endpoint. It treats the outer query as
untrusted and returns authorization parameters only from the verified JOSE
object.

## Security policy

- `request` must be a compact JWS, or a compact JWE whose plaintext is a
  compact JWS. A JWE inside a JWE is rejected before any second decryption.
- A verified request object that contains a top-level `request` or
  `request_uri` claim is rejected.
- Only `client_id`, `request`, and `request_uri` are read from the outer
  query. Every other query authorization parameter is ignored, so a query
  value can never override a signed claim.
- The query `client_id` must be present exactly once and must equal the
  request object's `client_id`.
- `iss` must equal that same client id.
- `aud` must be a string or array containing the configured issuer identifier.
- `alg=none` is always rejected. The JWS `alg` must be listed in the
  client's `RequestObjectSigningAlgorithms`; an empty allow-list rejects every
  algorithm.
- Duplicate JSON member names are rejected in headers and claims. Duplicate
  query parameters for `client_id`, `request`, and `request_uri` are rejected.
- Optional `exp` and `nbf` claims are checked against the configured clock and
  leeway.

Supported JWS algorithms: `HS256`, `HS384`, `HS512`, `RS256`, `RS384`,
`RS512`, `PS256`, `PS384`, `PS512`, `ES256`, `ES384`, `ES512`, and `EdDSA`.

Supported JWE key-management algorithms: `RSA-OAEP`, `RSA-OAEP-256`, `dir`,
`A128KW`, `A192KW`, and `A256KW`.

Supported JWE content-encryption algorithms: `A128GCM`, `A192GCM`,
`A256GCM`, `A128CBC-HS256`, and `A256CBC-HS512`.

## Usage

```go
parser, err := netsec.NewParser(netsec.Config{
	Issuer: "https://as.example",
	Clients: netsec.StaticClients{
		"client-a": {
			ClientID:                       "client-a",
			RequestObjectSigningAlgorithms: []string{"RS256"},
			RequestObjectSigningKeys:       []any{clientAPublicKey},
		},
	},
	DecryptionKeys: []any{authorizationServerPrivateKey},
})
if err != nil {
	return err
}

object, err := parser.ParseAuthorizationRequest(r.Context(), r)
if err != nil {
	// Map the sentinel errors in errors.go to the appropriate OAuth error.
	return err
}

// These parameters come from the verified JOSE object, never from the query.
scope := object.EffectiveParameters().Get("scope")
```

For `request_uri`, configure `Config.RequestURIFetcher`. The included
`HTTPRequestURIFetcher` is HTTPS-only, disables redirects, and limits the
response body. Applications with additional SSRF controls should provide their
own implementation.

Run the test suite with:

```sh
go test ./...
```
