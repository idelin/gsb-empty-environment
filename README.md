# Reverse-route privilege check

This Go program checks whether an unauthorized role can reach a restricted backend through two ordered, byte-prefix reverse-proxy route tables.

## Route semantics

1. Gateway routes are checked against the original request path in array order.
2. The first gateway route whose `prefix` is a byte prefix of the path performs exactly one replacement:
   `replacement + path[len(prefix):]`.
3. Gateway authorization uses the original path, before replacement.
4. Origin routes are checked against the rewritten path in array order.
5. The first matching origin route performs one replacement and selects its configured backend.
6. A valid request path must contain only bytes from `alphabet`, in the given alphabet order.
7. The answer is the shortest valid path; ties use the supplied alphabet order.

## Why this does not enumerate paths

For a gateway route with prefix `g` and replacement `r`, every path that reaches it has the form `g + s`.
Its gateway rewrite has the form `r + s`.

For an origin route with prefix `o`, that rewrite can match `o` only when one of these holds:

- `o` starts with `r`; then the unique shortest suffix is `o[len(r):]`.
- `r` starts with `o`; then the unique shortest suffix is empty.

If neither prefix relationship holds, that route pair cannot produce a match.

Each gateway/origin pair therefore contributes at most one shortest candidate. The solver only needs to verify:

- the candidate path stays inside the requested maximum length;
- every byte of the original candidate path is in the alphabet;
- no earlier gateway route matches the original path;
- no earlier origin route matches the gateway rewrite;
- the origin route reaches a restricted backend.

With route count `R` and maximum route-prefix length `P`, the cost is `O(R²P)` time and `O(R)` extra space. The maximum path length does not make the search larger.

## Input

Pass JSON with `-file`, or provide it on standard input:

```json
{
  "alphabet": "ab",
  "max_path_length": 8,
  "role": "user",
  "restricted_backends": ["admin"],
  "gateway_routes": [
    {"prefix": "b", "replacement": "public", "role": "user"},
    {"prefix": "a", "replacement": "admin", "role": "user"}
  ],
  "origin_routes": [
    {"prefix": "public", "replacement": "/p", "backend": "public"},
    {"prefix": "admin", "replacement": "/a", "backend": "admin"}
  ]
}
```

Route indices in output are zero-based. Empty-string prefixes are valid prefixes and match every path.

## Run

```sh
go run . -file input.json
go test ./...
```

A successful exploit returns:

```json
{
  "vulnerable": true,
  "path": "a",
  "length": 1,
  "gateway_route": 1,
  "origin_route": 1,
  "gateway_rewrite": "admin",
  "origin_rewrite": "/a",
  "backend": "admin"
}
```

When no path exists, the output is:

```json
{"vulnerable": false}
```
