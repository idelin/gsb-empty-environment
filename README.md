# updater

A Go lock-file updater. Given an old lock file, a finite set of candidate
versions per package, per-version dependency constraints, and a security
blocklist, it computes the optimal new lock file.

## Objectives (lexicographic order)

1. **Feasibility (hard)** — no banned versions; every requirement of
   every selected version is satisfied, including requirements of newly
   added dependencies. An old version is never kept by violating a new
   dependency.
2. **Minimize changes** — old-lock packages whose version changed. Newly
   added dependencies do not count as changes.
3. **Minimize total packages** — new dependencies count here, so they are
   only added when required.
4. **Maximize the version vector** — compared lexicographically by
   package name, higher versions preferred.

Returns no solution when the blocklist and constraints cannot all be
satisfied.

## Usage

```sh
go run ./cmd/updater problem.json   # or pipe JSON on stdin
```

See `types.go` for the JSON schema (`Problem` / `Solution`).

## Verification

`BruteForceSolve` independently enumerates every valid assignment
(each candidate package absent or pinned to a candidate version) and
returns the optimum under the same objectives. `TestSolveMatchesBruteForce`
cross-checks `Solve` against full enumeration on randomized instances.

```sh
go test ./...
```
