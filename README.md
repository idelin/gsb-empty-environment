# flagstone

TypeScript feature-flag library: byte-polynomial hash bucketing, versioned
config snapshots, and flag dependencies.

## Bucketing hash

Custom byte-polynomial hash (FNV-1a, 32-bit) over the **UTF-8 bytes** of the
key:

```
h = 2166136261
for each UTF-8 byte b: h = (h XOR b) * 16777619  (mod 2^32)
bucket = h mod 10000            // bucket in [0, 9999]
key    = [salt:]flagId:userId
```

A group rollout of `percentage: p` passes iff `bucket < p * 100`
(`p = 0` lets nobody in, `p = 100` lets everybody in). Unicode input is
encoded as UTF-8 before hashing, so NFC/NFD forms of the "same" string hash
differently. Public FNV-1a vectors and cross-checked UTF-8 vectors are
verified in `test/hash.test.ts`.

## Evaluation precedence

1. **Explicit user override** (`overrides[userId]`) — always wins.
2. **Group rules** — conditions first, then rollout bucket. Groups are
   ordered by **priority descending, then id ascending**; the first group
   whose conditions all match *and* whose rollout passes wins. A group whose
   conditions match but whose rollout fails falls through to the next group.
3. **Default value**.

Every result explains itself: `reason`, `matchedGroupId`, `bucket`,
`bucketKey`, per-dependency outcomes, and the `snapshotVersion` it came from.

### Missing attributes

A missing attribute is **not** the empty string: it fails every condition
operator (`eq`, `neq`, `in`). An explicitly present `''` is a real value and
matches `eq ''`.

## Versioned snapshots

`store.update(flags)` validates the *entire* config set and appends it as a
new immutable snapshot; old snapshots are retained and remain evaluable via
`evaluate(flag, user, { version })`. Bucketing parameters (rollout
percentage, salt) live inside the snapshot, so changing them only affects
the new snapshot — pinned evaluation of an old version reproduces the old
buckets exactly.

## Flag dependencies

A flag may require another flag to hold a specific value:

```ts
{ id: 'child', defaultValue: 'off', fallbackValue: 'blocked',
  dependencies: [{ flagId: 'base', requiredValue: 'on' }] }
```

Dependencies are evaluated recursively **on the same snapshot**, so a result
never mixes config versions. If any dependency is unmet, the flag yields its
`fallbackValue` (default: `defaultValue`) with reason `dependency_unmet`.
Updates are atomic: a dependency cycle (or dangling reference, or duplicate
id) rejects the **entire** update and the store keeps serving the previous
snapshot.

## Evaluation cache

Results are cached under a key containing:

```
snapshotVersion | flagId | userId | attr:<attrVersion> | deps:<closureDigest>
```

- `attrVersion` is supplied by the caller in `UserContext`; bump it whenever
  the user's attributes change, otherwise a stale result may be served.
- `closureDigest` summarizes the flag's transitive dependency closure
  (member ids + each member's config digest) within the snapshot, so a
  config change to any dependency invalidates dependent results.

## Development

```
npm run build   # tsc -> dist/
npm test        # build + run the suite in dist/test/
```

TypeScript is a dev-only dependency; the library itself has zero runtime
dependencies.
