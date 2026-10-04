# causal-gap-handler

Causal gap handling for message delivery with predecessor dependencies.
A successor whose predecessors expired (TTL) or were deleted must not hang
forever — but skipping a predecessor is only allowed with a **gap proof**
inside an installed **reset boundary** snapshot.

Requires Node.js >= 22.6 (runs TypeScript via native type stripping).

## Protocol

1. **Receive** — `receive(msg)` delivers a message once every predecessor in
   `msg.deps` is delivered; otherwise the message is buffered as pending.
2. **Expire / delete** — `expire(id)` / `delete(id)` records a `GapProof`
   tombstone: the message is permanently gone, not merely in flight.
3. **Snapshot** — `createResetBoundary()` captures the delivered frontier and
   all gap proofs at a watermark (logical time).
4. **Install** — `installBoundary(boundary)` validates the snapshot, then
   releases every pending message whose remaining missing predecessors are
   all covered by the boundary's gap proofs. Released messages cascade to
   unblock their own successors.

## Skip rules (safety)

A pending predecessor is skipped **only** when all of the following hold:

- a reset boundary is installed,
- the boundary carries a gap proof for that exact predecessor,
- the proof's message clock is **older than the boundary watermark**, and
- the removal was recorded at or before the watermark.

Consequences:

- No boundary installed → nothing is ever skipped (audit tells you to install one).
- A merely-missing predecessor (no proof) → never skipped.
- A dependency created by events that **interleave with the reset** (clock at
  or after the watermark, or expired after the snapshot was taken) → never
  skipped; the message stays pending until the dependency arrives or a newer
  boundary covers it.

## Recovery output

`installBoundary()` and the read-only `audit()` both return a
`RecoveryReport` listing every affected message and its recovery step:

- `released` — all remaining predecessors proven gone; message delivered.
- `still-pending` — some predecessors skipped, but the listed
  `remainingDeps` have no proof and must still be awaited.
- `delivered` — unblocked by a cascading delivery.

## Usage

```ts
import { CausalGapHandler } from "./src/index.ts";

const h = new CausalGapHandler<string>();
h.receive({ id: "b", deps: ["a"], clock: 2, payload: "..." }); // pending: a missing
h.expire("a");                                                 // tombstone for a
const report = h.installBoundary(h.createResetBoundary());     // releases b
console.log(report.steps);
```

## Tests

```sh
npm test
```

Covers causal buffering, hang recovery via reset boundary, proof-less
predecessors, reset/new-event interleaving (post-watermark dependencies are
not skipped), boundary validation, cascade delivery, and audit output.
