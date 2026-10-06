# bytebucket-flags

A TypeScript feature-flag library built around a byte-polynomial hash
(FNV-1a, 32-bit, over UTF-8 bytes) reduced modulo 10000 into stable buckets.

## Layout

- `src/` — canonical TypeScript sources.
- `dist/` — compiled CommonJS, kept in sync with `src/` (regenerate with
  `npm run build`, which needs the `typescript` dev dependency).
- `test/` — runnable test suites (`npm test`, plain Node, no dependencies).

## Hashing and bucketing

`hashString` computes FNV-1a 32-bit over the UTF-8 encoding of the input
(never UTF-16 code units), and `bucketOf` reduces it mod 10000, yielding a
bucket in `[0, 9999]`. Rollouts are expressed in basis points
(`0..10000`) and map 1:1 onto buckets: a user is included iff
`bucket < basisPoints`. Bucket keys join `salt / flagId / [groupId] / userId`
with a unit separator so key parts can never blur together.

The hash implementation is verified against the published FNV-1a 32-bit
test vectors (`""`, `"a"`, `"foobar"`, …) plus UTF-8 vectors for
multi-byte and astral-plane characters.

## Evaluation order

For a flag, a user, and one config snapshot:

1. **Dependencies** — every `dependsOn` entry is evaluated recursively in
   the *same* snapshot; any unmet dependency yields `fallbackValue`
   (default `false`) with reason `dependency-unmet`.
2. **Explicit user override** — `overrides[userId]` wins over everything.
3. **Group rules** — sorted by `priority` descending, ties broken by
   ascending rule `id`; the first group whose conditions all match (and
   whose own rollout, if any, includes the user) wins.
4. **Flag rollout** — gates `defaultValue`; excluded users get
   `fallbackValue`.

A **missing attribute is not the empty string**: every value comparison
(`eq`, `neq`, `in`, `gt`, …) fails when the attribute is absent. Only
`exists` / `notExists` inspect presence, so `{tier: ""}` and `{}` behave
differently.

## Config snapshots

`FlagService.updateConfig(flags)` validates the whole configuration and
publishes it as the next immutable, deep-frozen snapshot version. Old
snapshots are retained and remain evaluable via
`evaluate(flagId, user, { version })`. Changing bucketing parameters
(rollout basis points, salt) therefore only affects the new snapshot.

Validation is atomic: duplicate flag ids, invalid rollouts, dependencies
on unknown flags, and **dependency cycles** (including self-dependencies)
reject the entire update — the current configuration is left untouched.

## Evaluation cache

Cached evaluations are keyed by

```
v<snapshotVersion> | <flagId> | <userId> | attrs@<attributesVersion> | <closureDigest>
```

- `attributesVersion` is part of `UserContext`; bump it (or use
  `withAttributes`) whenever attributes change, otherwise cached results
  are intentionally stale.
- `closureDigest` is a hash of the canonical serialization of the flag's
  transitive dependency closure, so a config change to any dependency
  invalidates the dependent's cached entries — while unrelated flag
  changes leave the digest untouched.

`evaluateAll(user, { useCache: false })` performs a full, uncached
evaluation; the test suite uses it to verify hot-switching rollout
configuration: after a config swap, cached evaluation must equal full
evaluation for every user, and the previous snapshot must answer exactly
as before.

## Example

```js
const { FlagService, createUser } = require("bytebucket-flags");

const svc = new FlagService([
  { id: "checkout", defaultValue: true, fallbackValue: false,
    rollout: { basisPoints: 2500, salt: "v1" } },
  { id: "recommend", defaultValue: "algo-v2", fallbackValue: "algo-stable",
    dependsOn: [{ flagId: "checkout", equals: true }] },
]);

svc.evaluate("recommend", createUser("user-1", { tier: "gold" }));
// { flagId: "recommend", value: "algo-stable", reason: "dependency-unmet", ... }
```

## Development

```sh
npm test          # run the test suites (no dependencies required)
npm run build     # recompile src/ -> dist/ (requires the typescript dev dep)
```
