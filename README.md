# canonkey

Canonicalizes structured requests into unambiguous keys that embed the
tenant, the parameter types, and the config version that affects results.
Standard library only.

## Canonical form

Self-delimiting, type-tagged byte encoding:

| Value   | Encoding                              |
|---------|---------------------------------------|
| null    | `z`                                   |
| bool    | `bt` / `bf`                           |
| number  | `n<digits>e<exp>` (exact, normalized) |
| string  | `s<bytelen>:<utf8 bytes>`             |
| array   | `a<count>[ ... ]` (order preserved)   |
| object  | `o<count>{ k v ... }` (keys sorted)   |

Key envelope: `k1( <tenant> <configVersion> <params|'m'> )` where `m`
marks absent params (distinct from explicit null `z`). The key is
`SHA-256` of those bytes; the bytes are kept for collision re-verification.

## Verifiable policies

- **Field order**: object keys sort by raw UTF-8 byte order; arrays keep
  their order.
- **Numbers**: exact decimal normalization (`1`, `1.0`, `1e0` are equal;
  `1.0000000000000000000000001` keeps full precision). Number `1` and
  string `"1"` carry different type tags and never collide.
- **Unicode**: byte-exact UTF-8; invalid UTF-8 is rejected. Composed and
  decomposed spellings are intentionally distinct.
- **Supported scope**: JSON-shaped values only. Cycles are rejected with
  `ErrCycle`; shared acyclic substructures expand by value. Different
  types and reference semantics can never encode to the same key.
- **Collision handling**: storage buckets by digest, but a hit requires
  byte-exact equality of the canonical request — forced digest
  collisions are re-verified against the original canonical bytes.
- **Budgets**: depth, node count, string/number length, and input bytes
  are all capped via `Limits`.
- **Isolation**: keys embed tenant and config version; both are required
  (fail-closed). Old keys stay isolated after a config upgrade.
- **Dependencies**: data source, clock, and failure hook are injected
  functions; the store is local in-memory (no remote cache); `Close`
  releases the sweeper and is verified via `Done`.

## Run

```sh
go test ./...
go run ./cmd/demo
```
