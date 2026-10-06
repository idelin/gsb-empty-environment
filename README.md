# testsel — change-aware test selection for Go

`testsel` stores a versioned snapshot of a Go codebase — a content hash per
function, the static call graph, a conservative dynamic-dispatch model, and
the functions each test was observed to cover — and, given a fresh snapshot,
selects exactly the tests that must run again.

## Selection rules

- **Modified function** → every test whose recorded coverage reaches it is
  selected, following the **union of the old and new call graphs** (so a
  changed call edge can never disconnect a test from a function it depends
  on, whether the edge was added or removed).
- **New function (unknown coverage)** → the whole suite is marked (`RunAll`).
- **Deleted function** → the full reverse-dependency closure over the union
  graph finds every impacted test.
- **Dynamic dispatch** → calls through interfaces / function values are
  recorded per method name and over-approximated by all known
  implementations; when an implementation set or version changes, every
  dynamic caller becomes an impact root.
- **Stale coverage** → coverage referencing functions absent from the new
  snapshot selects the affected tests for re-recording.

`Verify(old, new)` audits a snapshot pair with the same dependency closure
and reports deleted-but-still-depended-on functions, changed dynamic
targets, stale coverage, dangling edges and ghost edge changes.

## Usage

```go
// Record (after a full green run):
snap, idx, _ := testsel.Analyze("./", "example.com/mod")
cov, _ := testsel.RecordCoverage("./pkg", "example.com/mod/pkg", idx)
snap.Coverage = cov // merge per package
testsel.SaveSnapshot("testsel.json", snap)

// Select (on a change):
old, _ := testsel.LoadSnapshot("testsel.json")
cur, _, _ := testsel.Analyze("./", "example.com/mod")
dec := testsel.Select(old, cur)
if dec.RunAll {
    // run everything
} else {
    // run dec.Selected
}
for _, issue := range testsel.Verify(old, cur) {
    log.Println(issue)
}
```

## Layout

- `snapshot.go` — snapshot model + JSON store (`SaveSnapshot`/`LoadSnapshot`)
- `diff.go` — snapshot diff: modified/added/deleted funcs, edge and
  dynamic-target changes
- `graph.go` — union-graph reverse dependency closure
- `select.go` — the selection decision
- `verify.go` — consistency audit via the full dependency closure
- `analyze.go` — type-free AST analysis: function hashes, static edges,
  dynamic-call model
- `cover.go` — `go test -coverprofile` parsing and block→function mapping
- `record.go` — per-test coverage recording helper

## Limitations

- The static analysis is type-free: package-qualified calls are resolved,
  everything else is treated as dynamic dispatch by method name
  (over-approximation: safe, occasionally over-selects).
- Reflection-based calls (`reflect.Value.Call`) are not modeled; treat
  reflection-heavy changes with a full run.
- `RecordCoverage` runs one `go test` invocation per test — fine for small
  and medium suites; larger suites should shard recording.
