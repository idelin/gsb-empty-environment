# access-audit

Audit a tool's **declared** file-access manifest against its **actual**
accesses. Every access is recorded with its full symlink expansion chain,
undeclared dependencies are reported, and a minimal supplementary manifest
is generated.

## Commands

```sh
npm run build          # compile TypeScript to dist/
npm test               # build + run the test suite (needs Node >= 18)
sh examples/demo.sh    # self-contained end-to-end demo
```

```
access-audit run    --workload ops.json [--base DIR] [--out access-log.json]
access-audit audit  --manifest decl.json --log access-log.json
                    [--case sensitive|insensitive|auto]
                    [--report report.json] [--supplement-out supplement.json]
access-audit check  --manifest decl.json --workload ops.json
                    [--base DIR] [--case sensitive|insensitive|auto]
                    [--report report.json] [--supplement-out supplement.json]
```

`run` executes a workload for real (readFile/readdir/stat/exists) and writes
an access log. `audit` compares a log with a manifest. `check` does both.
Exit code is `0` when every actual access is declared, `1` otherwise, so it
can gate CI.

## Manifest

```json
{
  "version": 1,
  "case": "auto",
  "declarations": [
    { "path": "app/config", "type": "dir" },
    { "path": "app/data", "type": "dir", "recursive": false },
    { "path": "app/data/input.csv", "type": "file" }
  ]
}
```

- `file` authorizes exactly that path; `dir` authorizes everything beneath
  it (`recursive: false` limits coverage to direct children).
- Relative paths resolve against `--base` (default: cwd).

## Authorization semantics

- **Symlink expansion chains**: each access records every symlink hop
  (`at` / `target` / `resolved`) plus the fully resolved `final` path.
- **Directory declarations do not follow escapes**: a symlink *inside* a
  declared directory whose target resolves *outside* it is reported as
  `symlink-escape`. The escape target must be declared explicitly — the
  generated supplement names the resolved target, not the link.
- **Declared real targets**: accessing a declared file through a symlink
  located elsewhere is authorized (the resolved target is what matters).
- **Non-existent paths**: recorded with `exists: false` and the first
  missing component (`missingAt`); they are matched lexically, since no
  symlink can be traversed past a missing component. Symlink loops are
  recorded as `ELOOP` errors.
- **Case policy**: `sensitive`, `insensitive`, or `auto` (probes the
  filesystem once). All path comparisons honor the effective policy.
- **Duplicates**: identical accesses (same op, path, and resolved target)
  are collapsed with a `count`; totals are still reported.

## Minimal supplement

With `--supplement-out`, the auditor emits a manifest fragment covering
exactly the undeclared accesses: duplicates removed, entries already covered
by a supplementary directory dropped, and symlink escapes kept explicit at
their resolved targets. Merge it into the main manifest to reach a clean
`check` run.

## Layout

- `src/resolve.ts` — component-wise symlink chain resolution
- `src/tracer.ts` — executable workload checker producing access logs
- `src/auditor.ts` — declaration matching, escape detection, supplement
- `src/cli.ts` — `run` / `audit` / `check` commands
- `test/` — node:test suites (fixtures build real symlink trees)
