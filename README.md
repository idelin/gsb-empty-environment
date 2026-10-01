# error-digest

Aggregates large numbers of similar JSON validation errors into a compact,
lossless summary. Requires Node.js >= 23.6 (runs TypeScript natively, no
dependencies, no build step).

```sh
npm run demo   # runnable sample: compression, retract, conservation, space report
npm test       # node:test suite (unit + mutation + randomized property tests)
```

## Validator (self-contained, no third-party validator)

`validate(instance, schema, schemaVersion)` implements **only** this schema
subset — nothing else is promised:

- `type`: `string | number | integer | boolean | object | array | null` (or an array of these)
- `enum`, `const`
- objects: `properties`, `required`, `additionalProperties: false`
- arrays: `items` (single schema), `minItems`, `maxItems`
- strings: `minLength`, `maxLength`, `pattern`
- numbers: `minimum`, `maximum`, `multipleOf`

Every error carries **both** paths:

- `instancePath`: array of segments, e.g. `[12, "name"]` (array indices are numbers)
- `schemaPath`: JSON-pointer-like rule path, e.g. `#/items/properties/name/minLength`

## Digest (src/digest.ts)

- Errors are grouped by `(schemaVersion, keyword, schemaPath, message)` —
  different rule reasons and different schema versions are never merged, and
  multiple errors at the same path are preserved (duplicates included).
- Inside a group, paths whose last numeric segment forms a consecutive run
  share a template, so `items 1..100` failing the same rule is one range
  `[1, 100]`. A missing middle item splits the run — continuity is never faked.
- `add` / `remove(id)` are online: they touch only the affected group and
  template, never rebuild the index.
- Random-access expansion: `expandByRule`, `expandByPrefix`, and
  `expandAll('insertion' | 'path')`.
- Conservation invariant: `digest.count` == sum of summary counts == number of
  expanded errors == size of the original error multiset.

## Wire format (src/serialize.ts)

`encode(digest)` produces a JSON document of `format: "error-digest/1"` with
per-group `count` and templates:

- `{ "kind": "range", "prefix": [...], "suffix": [...], "runs": [...] }` where a
  run is `{ start, end, idStart }` (compact, consecutive insertion ids) or
  `{ start, end, ids: [[...], ...] }` (one id list per value, keeps duplicates)
- `{ "kind": "literal", "path": [...], "ids": [...] }` for paths without a
  numeric segment

`decode(doc)` expands back to the exact original error list and **rejects
tampered documents**: fake counts (group/total counts must match the actual
ranges), illegal ranges (`start > end`, negative, non-integer, overlapping or
unsorted runs), malformed id lists, and duplicate ids.

## Tests

- `test/validator.test.ts` — schema-subset behavior and both error paths
- `test/digest.test.ts` — range compression, gap splitting, grouping rules,
  online retract, expansion orders
- `test/serialize.test.ts` — round trip plus 18 mutation cases that must be
  rejected
- `test/property.test.ts` — 4000 random add/retract operations cross-checked
  against an independent plain-list reference model, plus 50 random
  serialize/decompress round trips
