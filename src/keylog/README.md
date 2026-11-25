# Document key admission

Importable TypeScript component that decides which key handles a subject may
claim for an object, even when replicas are offline, reconnect with stale
backups, or deliver events out of order. It builds on the persistent event id
and byte representation in `../dot.ts`.

## Import

```ts
import { OnlineMerger, OfflineVerifier, HmacSigner } from "./keylog/index.ts";
```

The package also exposes `"./keylog"` via `package.json` exports.

## Event identity and causality

- Every event carries a signed author, a permanent `objectId`, an encryption
  `generation`, a local `dot` and a sparse `observed` prefix.
- The persistent event id is the dot rendered by `formatDot`; sequences are
  `bigint`, so full uint64 values stay exact past `Number.MAX_SAFE_INTEGER`.
- Causality is derived only from the `observed` version-vector prefix.
  `compareDots` is a stable storage/byte order and is never treated as causal.
- An event is *grounded* when every observed predecessor is present and itself
  grounded. Missing predecessors keep dependent decisions `pending`.

## Signature permissions

The event `kind` is inside the domain-tagged signed bytes, so a signature made
for one permission can never authenticate another:

- `rebuild` — owner only; enables a generation (genesis or post-delete).
- `delete` — owner only; closes exactly the named generation.
- `grant` — the owner, or an actor reachable through a live delegation chain.
- `delegate` — same authority requirement, capped by `maxDepth`.
- `revoke` — the owner, or an actor with delegated authority in that generation.

Hosts supply a `Verifier` (typically asymmetric signatures); `HmacSigner` is
provided for tests and per-actor MAC deployments.

## Delete and rebuild semantics

- A grounded owner `delete` closes only its own generation. Grants in that
  generation, including envelopes concurrent with the delete or replayed late
  from an old backup, are firm `closed` denials and can never restore access.
- A delete stays effective even if its author later equivocates; deny evidence
  is never erased by a fork.
- A new generation is enabled solely by an owner-signed `rebuild`. It is a
  distinct generation, so an old delete cannot affect it.

## Equivocation and gaps

- Two envelopes with the same dot but different canonical signed content mark
  the author as equivocating. That author's privilege events (`grant`/
  `delegate`) are quarantined (`tainted`); their `revoke`/`delete` denies still
  hold, so a fork cannot be used to revoke-proof an actor.
- Compressed summaries carry an authoritative per-actor `frontier`. A missing
  sequence below the frontier is reported through `gaps()` as an explicit dot
  id; it is never interpreted as "the actor wrote nothing". Evidence that
  depends on a gap stays `pending` rather than being allowed or hard-denied.

## Two entry points

- `OnlineMerger` keeps only the evidence that can still change a decision:
  compact fingerprint records, the frontier, equivocation marks and gaps.
  Signatures and full bodies are dropped after one verification. `summary()`
  emits the self-contained compressed form.
- `OfflineVerifier` retains the uncompressed signed log and recomputes
  admission from scratch.

Both run the same confluent, set-based reducer. Arrival order never mutates
the result, so for any fixed set of logical events they return identical
`claimable`/`pending`/`decidedDenials` reports under every legal delivery
order (covered by the randomized ordering test).

## When an old revoke may be discarded

`canDiscardRevoke(id)` is true exactly when the revoke's generation can never
again admit the edges it suppressed:

1. the generation is durably closed by a grounded owner `delete`, or
2. the generation is no longer enabled by any grounded `rebuild`.

An open generation never qualifies. A replica that never comes online may
still replay the original grant, so discarding the revoke would wrongly
re-admit it. The predicate needs no quorum or liveness assumption.
