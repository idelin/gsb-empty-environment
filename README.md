# Membership Switch Simulator (Joint Consensus)

Simulates a cluster membership change from an old config to a new config
via an intermediate **joint** stage that requires majorities of *both*
groups for every commit — including the config entries themselves.

## Model

```
stable(old) --commit(C_old,new)--> joint(old, new) --commit(C_new)--> stable(new)
```

- `stable(c)`: an entry commits with a majority of `c`.
- `joint(o, n)`: an entry commits only with a majority of `o` **and** a
  majority of `n`.
- `C_old,new` is a normal log entry committed under the **old** rules;
  `C_new` is committed under the **joint** rules. There is no direct
  `stable(old) -> stable(new)` edge.

## Counterexample

With disjoint groups old={A,B,C}, new={D,E,F}: if `C_new` could commit
with only the old majority {A,B}, the new group {D,E,F} activates without
the old group's entries, and the disjoint quorums {A,B} and {D,E} commit
different values at the same log index (split brain). The joint stage
blocks exactly this quorum: acks {A,B,C} (full old group, zero new
members) cannot commit `C_new`.

## Layout

- `src/consensus.ts` — commit decision (`isCommitted`) + `ConfigStateMachine`
- `src/simulator.ts` — deterministic leader simulator + `SafetyTracker`
- `src/counterexample.ts` — runnable unsafe-vs-safe scenario
- `test/consensus.test.ts` — unit + scenario tests

## Run

Requires Node >= 22.6 (uses native TypeScript type stripping).

```sh
npm test    # node --test "test/*.test.ts"
npm run demo
```
