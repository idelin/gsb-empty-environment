# capmig — multi-hop transactional capability migration

`capmig` is an importable Go package for permission hosts that must migrate
signed capability tokens across an endpoint-action split and an object
namespace merge, while proving global causal consistency over a handover
window that contains lost-confirmation transfers and late revocation receipts.

## Install

```go
import "formal.local/w3b-a9f48525a0/capmig"
```

## Inputs

Build a `capmig.Input`:

- `AliasBindings` — cross-namespace aliases collapsed to physical object IDs.
  Conflicting bindings for one alias are a hard error.
- `ResourceMaps` — many-to-many old → new object edges; reasoning uses the
  reflexive-transitive closure, so multi-hop namespace merges are supported.
- `ActionSplits` — coarse actions (for example `manage`) expanded into
  mutually non-entailing fine actions; cycles are rejected.
- `Grants` — signed genesis grant per transfer token.
- `Revocations` — signed revocation records; a revoked token has no holder.
- `Handoffs` — multi-hop delegation receipts. `LostConfirmation: true` turns
  a hop into one global Boolean variable. At most
  `capmig.MaxAmbiguousHandoffs` (18) are allowed, bounding the enumeration at
  2^18 histories. `ReceiptEpoch` (defaulting to `Epoch`) older than
  `BarrierEpoch` makes the receipt inadmissible.
- `Tickets` — migration requests, including thousands of derived child
  tickets that may share upstream ancestor tokens.

## Solver semantics

1. Aliases are resolved to physical IDs before any entitlement reasoning, so
   a token on an alias denoting another physical object cannot impersonate a
   different holder.
2. Every confirmation-lost hop becomes one Boolean variable, and every
   possible assignment (a "history") is enumerated jointly. The same
   assignment drives every ticket, so one ambiguous hop can never be resolved
   differently for different derived tickets.
3. Each token is replayed as a state machine per history. A transfer only
   fires when the token is live and the sender is the current exclusive
   holder; otherwise it is recorded as a causal failure. Revocations sort
   before same-epoch handoffs (fail-closed).
4. Confirmed (non-ambiguous, barrier-admitted) receipts that can never fire
   prune histories. When no history honours every confirmed receipt the
   result is flagged `Inconsistent` and every ticket denies rather than
   inventing a holder.
5. A ticket is `migrate` only when its subject holds a live covering token
   for every target `(new physical object, split action)` pair in **every**
   admissible history. It is `deny` when no history supports it, otherwise
   `reauth`.
6. The minimum forced re-authentication subject set is an exact minimum
   hitting set, solved with branch-and-bound plus a disjoint-set packing
   lower bound.

The hot enumeration is vectorised: per-token holder/revocation bitsets and
per-handoff success bitsets are computed 64 histories at a time, so the full
2^18 sweep with 1,200 derived tickets runs in about a millisecond of core
solver time.

## Usage

```go
res, err := capmig.Solve(in)
if err != nil { /* malformed input */ }

fmt.Println(capmig.RenderText(res))   // human-auditable report
// res is plain data and can also be json.Marshal'ed.
```

`Result.TicketReports` carries, per ticket: support counts, the object/action
coverage paths, the full multi-hop chain with per-hop success counts,
barrier rejections, revocations, causal failures, effective ambiguity
variables, re-authentication candidates, and up to three concrete
counterexample histories with their variable assignments and failure reason.

## Demo and tests

```sh
go run -buildvcs=false ./cmd/capmig-demo
go test -buildvcs=false ./...
```

The test suite covers multi-alias impersonation, chained lost confirmations,
late revocation receipts, conflicting confirmed receipts (global
inconsistency), exclusive-handle double-spend protection, exactness of the
minimum hitting set against brute force, and a 1,200-ticket / 18-variable
full-enumeration scale case.
