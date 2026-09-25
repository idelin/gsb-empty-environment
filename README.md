# evidence-replay

Tamper-evident policy evaluation records with **offline replay verification**.
Each decision persists a rule summary, an attribute snapshot, and a full
evaluation trace; a verifier later replays the trace offline, branch by branch
and read by read, and locates the first divergence if anything was tampered
with.

Requires Node.js >= 22.6 (developed on 23.9; TypeScript via native type
stripping, zero dependencies, tests use the built-in `node:test` runner).

```sh
npm test     # run the built-in test suite
npm run demo # run the end-to-end sample (writes records/*.json)
```

## Design

- **Restricted policy DSL** (`src/rule.ts`): integer comparisons, finite-set
  membership, and boolean combinators (`and`/`or`/`not`) only. Rules read
  explicitly named attributes; there is no external identity service.
  `summarizePolicy` binds `id + version + canonical rule` into one SHA-256
  rule summary.
- **Attribute snapshot** (`src/snapshot.ts`): the explicit attributes captured
  at decision time, hashed for integrity. Sensitive fields can be
  independently sealed into commitment placeholders (`sealAttribute`); the
  plaintext never enters the record.
- **Evaluation trace** (`src/evaluate.ts`, `src/trace.ts`): a DAG of
  subproofs keyed by structural rule hash. Identical sub-expressions are
  evaluated once and share one compressed subproof; `children` preserves
  actual evaluation order, `skipped` records short-circuited branches, and a
  flattened read set logs every actual attribute read in order. Outcomes are
  three-valued: a sealed attribute yields `unknown`.
- **Evidence record** (`src/evidence.ts`, `src/store.ts`): rule summary +
  snapshot + snapshot hash + trace + conclusion, persisted as JSON.
- **Replay verification** (`src/replay.ts`): uses only the record and the
  verifier's own policy copy — live attribute sources are never consulted, so
  later real-time corrections cannot change a historical verdict. The
  verifier checks, in order: rule summary (id/version/hash), snapshot hash,
  sealed-field claims, then re-evaluates offline and compares **every branch
  and the actual read set** (never just the final boolean), and finally the
  claimed conclusion.

## Verdict semantics

- `verified` — trace, reads, and conclusion all reproduce exactly.
- `unverifiable` — sealed attributes block the conclusion; the placeholder is
  kept but the verdict cannot be confirmed.
- `rejected` — first divergence located: `rule-summary-mismatch`,
  `snapshot-tampered`, `sealed-claim-rejected` (source claims about sealed
  fields are never accepted), `missing-evidence`, `trace-mismatch`,
  `read-set-mismatch`, or `conclusion-mismatch`.

## Sample run (`npm run demo`, abridged)

```
=== 1. evidence record saved ===        conclusion "true", 5 subproofs, 3 reads
=== 2. live attributes corrected ===    score 712 -> 100, region 2 -> 9
=== 3. offline replay ===               { "status": "verified", "conclusion": true }
=== 4. tampered rule version ===        rejected: rule-summary-mismatch (1.0.0 vs 9.9.9)
=== 5. one read item deleted ===        rejected: read-set-mismatch at index 1
=== 6. short-circuit order swapped ===  rejected: trace-mismatch at $ children[0]
=== 7. sealed sensitive field ===       unverifiable: sealedAttributes ["deposit"]
=== 8. forged claim about sealed field  rejected: sealed-claim-rejected (deposit)
```

## Test results (`npm test`, Node v23.9.0)

```
✔ round-trip: saved record verifies offline
✔ live attribute corrections do not affect the historical verdict
✔ tampered rule version is rejected at the summary check
✔ same trace paired with a different rule summary is rejected
✔ deleting one read item is located at its exact index
✔ omitting actual read fields cannot replay successfully
✔ swapped short-circuit order is located at the first differing child
✔ missing subproof is reported as missing evidence with its path
✔ sealed sensitive field: placeholder kept, conclusion unverifiable
✔ source claims about sealed fields are not accepted
✔ shared subproofs are compressed: stored once, referenced twice
✔ short-circuit skips are recorded and verified
✔ flipped conclusion claim is rejected
✔ tampered historical snapshot is detected via its hash
✔ not combinator and unknown propagation verify correctly
✔ rule DSL rejects non-integer conditions
ℹ tests 16  ℹ pass 16  ℹ fail 0
```
