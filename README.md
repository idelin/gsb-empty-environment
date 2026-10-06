# speckey

Go library for specification keys composed of multiple nullable fields,
with a deduplicating store, explicit conflict strategies, and validated
checkpoints.

## Key encoding

A `SpecKey` is an ordered list of `Field` values. Each field is either
**null** (`Null()`) or **set** (`Str(s)`); null and the empty string are
distinct values and encode differently.

Canonical encoding: `v1|<field count>("|" <field>)...` where each field is

- `N` — null
- `E` — empty string
- `V<decimal byte length>:<raw bytes>` — non-empty value

Length-prefixing makes the encoding injective and self-delimiting:
values containing `N`, `E`, `|`, `:`, or text that looks like an
encoding can never collide or be misparsed. `ParseKey` is the exact
inverse of `SpecKey.String`.

## Dedup set and conflict strategies

`Set.Add(key, content)` never silently overwrites:

| Situation | Outcome |
|---|---|
| key absent | `Added` |
| same key, same content | `Duplicate` (no-op dedup) |
| same key, different content | per strategy |

Strategies: `ConflictError` (reject, keep original, return
`ErrConflict`), `ConflictIsolate` (store the new content under a
deterministic content-addressed isolation key; both stay addressable
via `GetAll`), `ConflictReplace` (explicit overwrite, reported as
`Replaced`).

## Checkpoints

`Set.Save`/`SaveTo` persist the full dedup set (primary + isolated
records, strategy, count, SHA-256 digest). `SaveTo` is atomic
(temp file + rename). `Load`/`LoadFrom` validate version, record
count, and digest before restoring, so corrupted checkpoints fail
loudly instead of silently restoring partial state.

## Tests

`go test ./...` covers adversarial field combinations (encoding
markers, separators, NUL bytes, unicode, 4 KiB values), encoding
injectivity for 1–3 field keys, parse round-trips, malformed input
rejection, all three conflict strategies, checkpoint round-trip
recovery (replaying historical adds dedups identically after restart),
and corruption detection.
