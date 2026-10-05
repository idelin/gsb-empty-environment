# pkg-dep-solver

A package dependency solver for repositories with **integer versions**,
**version ranges**, **optional features**, and **cross-package conflicts**.
Given a repository, the currently installed state, and a set of requests, it
computes an install plan that:

1. satisfies every constraint,
2. performs the **fewest changes** relative to the installed state
   (upgrade / downgrade / new install / removal each count as one change),
3. breaks ties by **preferring higher versions** (requested packages first,
   in request order, then the rest by name — lexicographic maximization).

When no plan exists, it returns a **reviewable conflict core**: an
irreducible subset of the request constraints that is already unsatisfiable
together with the repository facts (removing any element makes it
satisfiable), plus the human-readable facts needed to verify the conflict by
hand. It never reports just "the last dependency that failed".

Designed for problems of up to ~20 packages; a hard cap of 100 guards
against accidental blowups.

## Usage

```sh
npm run build          # compile TypeScript to dist/
node dist/src/cli.js input.json     # or: cat input.json | node dist/src/cli.js
npm test               # unit tests + randomized brute-force cross-check
```

Exit codes: `0` plan found, `1` conflict (core printed), `2` input error.

## Input format

```jsonc
{
  "repository": [
    {
      "name": "web",
      "versions": [
        {
          "version": 3,                          // integer, unique per package
          "dependencies": [                      // required whenever web@3 is selected
            { "name": "http", "range": ">=2 <5", "features": ["tls"] }
          ],
          "features": {                          // optional features
            "tls": [{ "name": "cert", "range": ">=1" }]
          },
          "conflicts": [                         // may not co-occur in a plan
            { "name": "proxy", "range": "<2" }
          ]
        }
      ]
    }
  ],
  "installed": { "web": 2, "http": 2 },          // or [{"name":"web","version":2}]
  "request": [
    { "name": "web", "range": ">=2", "features": ["tls"] }
  ]
}
```

- **Ranges**: space/comma-separated comparators (`>=`, `<=`, `>`, `<`, `=`,
  `==`), a bare integer for an exact version, `*`/empty/omitted for any.
- **Features** are additive: enabling a feature (via the request or via a
  dependency's `features` list) activates the extra dependencies of that
  feature for the selected version. A feature not defined by the selected
  version is a no-op. A feature can only be on when its package is installed.
- **Conflicts** are symmetric in effect: the two versions may not appear in
  the same plan.
- Packages not reachable from the request are left out (installing them
  would cost a change, and changes are minimized).

## Output

Success (`exit 0`):

```jsonc
{
  "status": "ok",
  "plan": [
    { "name": "web", "version": 3, "features": ["tls"], "change": "upgrade" }
    // change: keep | upgrade | downgrade | new
  ],
  "removed": ["oldpkg"],   // installed packages no longer in the plan
  "changes": 1             // number of packages whose state differs
}
```

Conflict (`exit 1`):

```jsonc
{
  "status": "conflict",
  "core": [ { "name": "app" }, { "name": "legacy" } ],
  "explanation": [
    "require app *",
    "require legacy *",
    "app@1 depends on db >=2",
    "legacy@1 depends on db <2"
  ]
}
```

`core` is minimal in the sense that dropping any single constraint makes the
remaining set satisfiable, so each element is genuinely part of the
conflict. `explanation` lists the involved requirements, installed versions,
available versions, and the dependency/conflict facts needed to review it.

## How it works

- The problem is encoded to CNF (`src/solver.ts`): one Boolean per
  (package, version) and per (package, feature); clauses for at-most-one
  version, request ranges, dependencies, feature implications, and
  conflicts.
- Solving uses a small built-in CDCL SAT solver (`src/sat.ts`) with watched
  literals, first-UIP learning, VSIDS activity, phase saving, and
  assumption literals.
- Each request constraint is guarded by an assumption literal. If the
  conjunction is UNSAT, a deletion-based minimization loop extracts an
  irreducible unsatisfiable subset of the requests (the conflict core).
- The change count is minimized with a guarded Sinz sequential-counter
  cardinality constraint (`at most k changes`), solved for increasing `k`.
- Ties are broken by greedily forcing the highest feasible version per
  package (requested packages first), which keeps the minimum-cost optimum.
- Correctness is checked by `test/fuzz.ts`, which compares the solver
  against exhaustive enumeration on hundreds of random problems, verifying
  optimality of the change count and irreducibility of every conflict core.

## Development notes

- TypeScript only, no runtime dependencies. If `tsc` is not installed, any
  TypeScript 5.x compiler works, e.g.:
  `node /path/to/typescript/bin/tsc -p tsconfig.json`.
- `PKGSOLVER_DEBUG=1` prints per-step solver timings to stderr.
- Examples: `examples/upgrade.json` (minimal-change plan),
  `examples/conflict.json` (conflict core).
