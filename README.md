# netsec: trigger execution and definer-escalation search

`netsec` is an in-memory trigger execution model for Go database test hosts.
It models INSERT/UPDATE/DELETE triggers, security-invoker/security-definer
execution, definer writable-table whitelists, statement-scoped recursion
guards, and full statement rollback. A separate BFS explorer compares a
correct model with a pending-review model and prints the shortest trigger
chain that writes a table the origin subject is not allowed to write.

## Quick start

```go
docs := netsec.NewTable("docs")
docs.Policy = netsec.SubjectPolicy{
    "clerk": netsec.Events(netsec.EventUpdate),
    "admin": netsec.AllEvents(),
}
docs.AddRow("r1", netsec.Row{"status": "public"})

_ = docs.AddBefore(&netsec.Trigger{
    ID:    "validate",
    Event: netsec.EventUpdate,
    Modify: func(ctx *netsec.TriggerContext, row netsec.Row) (netsec.Row, error) {
        if row["status"] == "secret" {
            return nil, netsec.ErrPolicyDenied
        }
        row["reviewed"] = true
        return row, nil
    },
})

e := netsec.NewEngine("clerk")
_ = e.AddTable(docs)
res, err := e.Execute(netsec.DML{
    Event: netsec.EventUpdate,
    Table: "docs",
    RowID: "r1",
    Row:   netsec.Row{"title": "new"},
})
_, _ = res, err
```

`NewEngine` uses `StrictOptions`. `NewEngineWithOptions` accepts explicit
options, and `NewPendingEngine` uses the flawed `PendingOptions` preset.
Tables fail closed: a `nil` policy denies writes, so permissive fixtures must
set `Policy: netsec.AllowAll` explicitly.

## Execution model

For every DML the engine performs these steps:

1. Check the table policy under the current effective subject.
2. Run the table's ordered BEFORE triggers. A BEFORE trigger may return a
   modified row or an error. The base-table write happens only after all
   BEFORE triggers accept the row.
3. Push a frame for each trigger invocation (`TriggerID`, event table,
   `RowID`, `EffectiveSubject`, `InvokerSubject`, `OriginSubject`,
   `StatementID`, depth, timing, security context, and definer whitelist).
4. Apply the base write, then run the ordered AFTER triggers.
5. If anything fails, restore every row touched by the statement, including
   rows written by triggers to audit or other tables.

Nested DML issued through `TriggerContext.Exec/Insert/Update/Delete` belongs to
the same statement and uses the same statement ID. Triggers cannot change the
session subject or commit/roll back the statement; the corresponding context
methods return `ErrTriggerCannotChangeSession` and `ErrTriggerCannotCommit`.

### Security context

- An `INVOKER` trigger runs with the effective subject of its caller.
- A `DEFINER` trigger runs with `Trigger.Definer`, but only for tables in
  `Trigger.WritableTables`.
- With the strict default, a definer write outside the whitelist returns
  `ErrDefinerTable`. `DefinerScopeInvokerFallback` instead retries as the
  invoker, and `DefinerScopeUnrestricted` models the pending-review bug.
- `OriginSubject` is the subject that started the statement. It is preserved
  through arbitrarily nested invoker/definer calls in the strict model.
- `InvokerSubject` is the effective subject of the DML that fired the trigger.
  A definer calling an invoker trigger therefore makes that invoker trigger
  run with the definer's effective identity, while the origin is unchanged.

### Re-entrancy and recursion

- A nested DML that repeats the *same event* on the original
  `(root event table, row ID, statement ID)` is rejected with `ErrReentrant`.
- A different event on that row is allowed by the re-entrancy rule, but remains
  subject to the global trigger depth and to the per-statement rule that each
  `triggerID` may run at most once for a given table row.
- `Options.MaxDepth` is the engine-wide nesting limit; `Trigger.MaxDepth` is a
  trigger-specific nesting limit. `0` in `Trigger.MaxDepth` means only the
  engine limit applies.
- Trigger frames are appended to `Result.Frames` even when a later trigger or
  AFTER action fails; `Result.RolledBack` tells the host that the writes were
  undone.

## Differential BFS

```go
correct := netsec.NewEngineWithOptions("clerk", netsec.StrictOptions())
pending := netsec.NewEngineWithOptions("clerk", netsec.PendingOptions())
// register the same tables/triggers on both models ...

start := netsec.DML{
    Event: netsec.EventInsert,
    Table: "notes",
    RowID: "n1",
    Row:   netsec.Row{"body": "hello"},
}
finding, err := netsec.DifferentialBFS(correct, pending, "clerk", []netsec.DML{start}, 8)
if err != nil { /* ... */ }
if finding.Found {
    fmt.Println(finding) // prints every frame's context
}
```

`BFS` searches one model for the shortest chain that writes a table the origin
subject is not allowed to write. `DifferentialBFS` returns a pending-model
finding only if replaying the same root DML against the correct model is
rejected or does not produce that unauthorized target write. `Finding` reports
the pending chain (`Frames`, `Steps`, `Writes`, `Target`) and the correct-model
replay (`CorrectFrames`, `CorrectWrites`, `CorrectErr`).

`SearchConfig`/`SearchBFS`/`DifferentialSearch` provide the configurable form
with state and finding caps. The explorer works on a snapshot of the current
model rows; use fresh engines when you need to search from an empty database.

## Test scenarios covered

The suite in `engine_test.go` and `search_test.go` covers two-table trigger
cycles, same-table different-row copies, BEFORE row changes that alter the
policy decision, AFTER failure rollback including audit rows, duplicate trigger
IDs, forbidden session/transaction operations, nested definer/invoker frames,
definer whitelist escapes, and shortest differential BFS chains.
