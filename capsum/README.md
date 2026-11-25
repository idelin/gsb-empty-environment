# capsum — Go-host permission summaries for captured protection domains

This module implements an importable, DFA-backed permission summary engine for
a Go host that runs Java-style plugin tasks. When a plugin submits work to a
host thread pool, the access check still passes through the protection domains
captured at submission time.

## Packages

- `pathlang` — finite DFAs over normalized path-component words. A resource is
  the word `<root|tenant-alias> ::= c1 ::= ... ::= ck`. Three grant shapes are
  first-class: the directory itself (`self`), exactly its direct children
  (`child`), and any strict descendant (`desc`). Union, intersection,
  complement, containment (via DFA emptiness) and minimization are provided;
  decisions never evaluate only sample paths.
- `capsum` — the program model, monotone fixpoint analysis and certificates.
- `certcheck` — an independent finite checker that re-runs the fixpoint and
  verifies initial conditions, call/return pairing and every async boundary.

## Semantics

- Grants inside one domain are unioned; stack frames intersect.
- A synchronous call intersects the new frame domains with every frame below.
- A privileged call truncates only the synchronous stack (`S`); the
  task-carried restriction (`F`) is always preserved.
- A post freezes the current effective language `S ∩ F` onto the handle.
- Consumption intersects that freeze with the consumer's current effective
  language, clears the handle bit (one-shot), and starts the task with
  `S = universal`, `F = freeze ∩ consumer-effective`.
- A task nested inside a task posts against an `F` that already contains the
  parent freeze, so a child task can never escape the parent's bound.
- At most four distinct pending handles are allowed; queue state is a 16-bit
  mask. There is no artificial stack-depth bound: distinct entry DNFs are
  interned as finite labels and summaries converge by monotone fixpoint.

## Verdicts

Each reachable program point reports `may` (union over reaching contexts) and
`must` (intersection over reaching contexts) as explicit DFAs. Points with no
reaching context are reported `unreachable`; if the iteration budget is
exhausted, affected points stay `unknown` and no settled language is claimed.

## Certificates

`Result.Certificate` binds the exact program, domains and tenant-root
versions through SHA-256 hashes and embeds generator DFAs, per-point verdicts,
handle freezes and fixpoint evidence. `certcheck.Check` recomputes everything
independently and returns a structured failure list on any mismatch.
