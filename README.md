# limited-schema-compat

A limited schema compatibility checker and atomic message converter in
TypeScript (no dependencies; runs on Node >= 23.6 via native type
stripping).

## Type system

Exactly these types, nothing else:

| kind     | constraints (all explicit, all required)            |
|----------|-----------------------------------------------------|
| `bool`   | —                                                   |
| `int64`  | `min`, `max` (decimal strings or integers)          |
| `uint64` | `min`, `max`                                        |
| `string` | `maxLength` in Unicode code points (UTF-8 payload)  |
| `bytes`  | `maxLength` in bytes                                |
| `array`  | `items`, `minLength`, `maxLength`                   |
| `record` | `fields`: `{id, name?, required, default?, type}`   |

- Fields are identified by **stable numeric IDs**; `name` is display-only
  and never used for matching (renaming a field is always compatible).
- **Recursive types are impossible**: type nodes are inline trees and any
  reference mechanism (`$ref`, `definitions`, `types`, ...) is rejected
  as `unsupported`.
- There is **no `null`**: a missing field is never treated as null.
- A field's *absence is accepted* iff it has a valid default or is not
  required. A default that violates its own (narrowed) type therefore
  makes absence unacceptable — this is how "default invalidation" is
  detected.

## Compatibility check

`check(old, new)` decides whether the new schema accepts **every** value
the old schema accepts, and returns one of:

- `compatible` — proven inclusion.
- `incompatible` — with a concrete **minimal counterexample**: a value
  that satisfies the old schema and violates the new one, minimal under
  1. total UTF-8 byte count of the canonical JSON encoding, then
  2. canonical-JSON lexicographic order, with strings compared by
     Unicode code point sequence.
  The report includes the reason and the deep error path (field IDs +
  display names, `items` for array elements).
- `unsupported` — any rule the checker cannot reason about (unknown
  kinds/keywords, missing explicit bounds, references, structurally
  invalid schemas). It **never defaults to compatible**.

Canonical JSON: record keys are decimal field IDs sorted as strings, no
whitespace, minimal string escaping, integers in plain decimal, bytes as
lowercase hex.

## Converter

`convertMessage(msg, fromSchema, toSchema)`:

- maps fields by numeric ID;
- preserves unknown fields (unknown to the source schema **or** dropped
  by the target schema) in a separate `unknown` region keyed by path;
- applies target defaults for absent fields, omits absent optional
  fields, fails on absent required fields;
- is **atomic**: the whole message is validated and converted as one
  unit — any failure yields an error and no partial record.

## Versioned registry & hot swap

`SchemaRegistry` binds schemas to their explicit `version` and caches
converters per `(from, to)` pair against immutable snapshots.
Re-registering a version swaps the schema and invalidates the cache, but
a conversion started with `beginConversion()` before the swap keeps
executing against the versions captured at its start.

## Finite-domain exhaustive verification

`verify(old, new, domain?)` enumerates every old-valid value inside a
finite domain (small integer ranges in full; strings/bytes over a
configurable alphabet up to their length cap; arrays/records as
products) and cross-checks the verdict and the counterexample's
minimality. `exhaustive: true` means the check is a proof within that
domain. The test suite sweeps all 784 ordered pairs of integer ranges in
`[-3, 3]` this way.

## CLI

```
node src/cli.ts check    <old.json> <new.json>              # verdict
node src/cli.ts convert  <old.json> <new.json> <msg.json>   # atomic conversion
node src/cli.ts verify   <old.json> <new.json> [--budget N] # finite-domain check
node src/cli.ts validate <schema.json>                      # schema lint
```

Exit codes: `0` compatible/success, `1` incompatible/conversion failed,
`2` unsupported or invalid schema, `3` usage or I/O error.

Message JSON: records are objects keyed by decimal field IDs; integers
may be numbers or decimal strings (for the full int64/uint64 range);
bytes are hex strings.

## Tests

```
node --test test/*.test.ts    # 60 tests
```

Covers field renames, numeric/string/bytes/array narrowing, optional →
required, default invalidation, deep error paths, unknown-field
preservation, missing ≠ null, conversion atomicity, cache binding,
hot-swap in-flight semantics, unsupported rules, and exhaustive
finite-domain verification of verdicts and counterexample minimality.
