# versioned-workflow

Event-sourced TypeScript workflow engine with safe, batch-atomic graph
version upgrades.

## Concepts

- **Graph** (`defineGraph`): states, transitions and terminal states, tagged
  with a `version`. Multiple versions coexist in a `GraphRegistry`.
- **Event log** (`InMemoryEventStore`): every instance is a single
  append-only sequence — `created`, then `transition` events, and later a
  `migration` event per upgrade. State is never mutated in place; it is
  derived by `replay`.
- **Commands** (`engine.dispatch`): carry `expectedVersion`. A command
  issued against a superseded version (an in-flight request from the old
  deploy) is rejected with `StaleVersionError`, so late commands can never
  interleave with the new-version event stream.

## Upgrading

`migrateBatch(engine, ids, fromVersion, toVersion, mapping)` runs in two
phases:

1. **Plan** — every instance must sit on `fromVersion`; every *active*
   (non-terminal) state must appear in `mapping`, and every target must
   exist in the new graph. Any violation aborts the whole batch before a
   single event is written (`UnmappedStateError` lists all offenders).
   Terminal instances need no mapping and are skipped.
2. **Commit** — one `migration` event is appended per instance. Because it
   shares the per-instance sequence with ordinary transitions, the upgrade
   is atomically ordered with respect to any new command.

Several old states may map onto one new state (merge). Historical events
are never rewritten, so pre-merge history stays fully replayable.

## Verifying an upgrade

`auditUpgrade(engine, id)` replays the log twice — up to the migration
event under the old graph, and the full log under the new one — returning
`before`/`after` snapshots with `state` and `availableActions`, so you can
diff executability across the upgrade.

## Restoring old instances

`engine.restoreInstance(id, exportedLog)` rehydrates a log persisted by an
older deploy (`store.export(id)`), validating sequence continuity and that
every referenced graph version is still registered.

## Develop

```sh
npm test   # builds with toolchain/typescript (offline tsc 5.2) and runs tests
```
