# Durable Action Authorization Protocol

This package authorizes one confirmation action against a signed grant and
executes its interdependent sub-operations on top of a storage backend whose
only transactional guarantee is that a single compare-and-swap (CAS) write is
atomic. There are no multi-key transactions.

## 1. What the signature binds

`signGrant` produces an HMAC-SHA256 over a canonical payload containing:

- `subject` — the acting principal;
- `windowSessionId` — the browser window/session the grant belongs to;
- `objectVersion` — the optimistic object/version token;
- `formDigest` — SHA-256 over the canonical field paths and values;
- every action's `meaning`, `maxActionConsumptions` and sub-operation list;
- every sub-operation's `meaning`, `dependsOn` and `maxSubConsumptions`.

Field aliases are resolved to canonical paths **before** hashing, so alias and
canonical forms of the same form produce the same digest. Tampering with any
bound attribute invalidates the signature.

Policy concerns (field revocation, shrunk action caps) are deliberately not
part of the signature: they are re-read on every submission and before every
pending sub-operation, so an asynchronous validation that started before a
revocation is never reused as evidence of current authorization.

## 2. Durable records and key layout

| Record | Key | Meaning |
| --- | --- | --- |
| Snapshot proof | `proof/snapshot/<grant>/<action>/<idem>` | independently readable authorization snapshot, written before any business write |
| Writer pointer | `ledger/active-claim/<grant>/<action>` | the single current writer slot; create-only CAS |
| Claim ticket | `ledger/claim/<grant>/<action>/<idem>` | immutable audit record of a winning confirmation |
| Sub counter | `counter/sub/<grant>/<action>/<subId>` | reserved ordinals and the idemKey that owns each |
| Sub proof | `proof/sub/<grant>/<action>/<idem>/<subId>` | proof that one business effect actually happened |
| Decision | `ledger/decision/<grant>/<action>/<idem>` | the final SUCCESS/FAILED decision, written once |

All sub-operation keys include both grant and parent action. Because a sub ID
is only unique within its parent action, two parent actions that reuse the same
`subId` never collide.

## 3. Preparation phase (the mutual-exclusion point)

1. Verify signature, expiry, subject, window session, object version and the
   canonical form digest.
2. Re-read the current policy; reject revoked fields and shrunk action caps.
3. Persist the snapshot proof (create-only CAS; a conflict is a benign replay).
4. Compete for the writer pointer with a create-only CAS:
   - the winner proceeds and writes its immutable claim ticket, after
     enforcing the action cap by counting existing tickets;
   - the loser gets `CONFLICT` and never writes business data.

Because the pointer create CAS is atomic, two windows racing the same
authorization cannot both obtain the writer slot. The action-level cap is
enforced only for winners, so the number of claim tickets never exceeds the
effective cap.

## 4. Decision phase and sub-operation proofs

Sub-operations are topologically sorted from `dependsOn` (request order is
ignored). For each sub-operation:

1. If a sub proof already exists for this confirmation, skip it (replay).
2. Re-read policy; a late field revocation terminates remaining work.
3. Determine the ordinal:
   - reuse an ordinal this same `idemKey` reserved when recovering a crash;
   - a different confirmation always gets a fresh ordinal;
   - enforce `maxSubConsumptions` against genuinely new effects.
4. Reserve the ordinal with a counter CAS (no business effect yet).
5. Apply the idempotent business effect keyed by
   `<grant>:<action>:<subId>:<ordinal>`.
6. Write an independently readable sub-operation consumption proof.

A reserved counter or an occupied claim is never evidence that a business
sub-operation completed. Completion is established solely by a sub proof plus a
matching gateway effect, which `reconcile` verifies independently.

## 5. Final decision

The decision record is written once with a create-only CAS after all required
sub-operations finish:

- `SUCCESS` only when every required sub-operation has a proof;
- `FAILED` otherwise, with executed proofs retained. If at least one required
  sub-operation is still missing, the service reports `PARTIAL` and never
  claims full success.

The writer pointer is deleted after the terminal decision so the next
confirmation can acquire the slot; the immutable ticket remains for audit.

## 6. Retry classification

| Situation | Status | Retry meaning |
| --- | --- | --- |
| Decision exists and all proofs present | `SUCCESS` | already executed; idempotent replay |
| Some proofs, mandatory work pending | `PARTIAL` | resume the same confirmation |
| Snapshot/claim fault before durable ownership | `RETRYABLE` | nothing executed; safe to retry |
| Fault after a write whose outcome is unknown | `UNKNOWN` | reconcile before retrying |
| Another window owns the writer pointer | `CONFLICT` | this attempt wrote nothing |
| Policy, signature, quota or validation error | `FAILED` | permanent for this attempt |

Ambiguous gateway timeouts are keyed by effect id: a retry first reads the
effect back, so an effect that did apply is recovered rather than duplicated.
