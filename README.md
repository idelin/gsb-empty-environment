# coverage-migrate

Migrate statement-level coverage data across refactors. Old coverage data
identifies statements by **statement id + source range**; the new version may
**move, split, or merge** statements. This tool only lets coverage flow along
**provable correspondences** — it never shifts line numbers to fake a match.

## Model

- **Old data** (`old.json`): statements with `id`, `file`, `range`, `hits`,
  and optional `text` (the statement's source text).
- **New model** (`new.json`): statements with `id`, `file`, `range`, optional
  `text` (no coverage yet).
- **Rules** (`rules.json`): explicit, human-authored mappings.

## How correspondence is proven

In priority order:

1. **Explicit rules** (`explicit:move` / `explicit:split` / `explicit:merge`)
   — declared by whoever did the refactor.
2. **Identical range** (`auto:identical-range`) — same file and exactly the
   same source range on both sides; the statement did not move.
3. **Content hash** (`auto:content-hash`) — whitespace-normalized statement
   text matches uniquely on both sides, even if the statement moved.

Anything that cannot be proven is **not** inherited:

- ambiguous matches (e.g. the same text appears twice on the new side) leave
  the old statement **uncertain**, with candidate ids listed;
- conflicting explicit rules (one statement referenced by two rules) block
  the involved statements and emit diagnostics;
- old statements with no proven counterpart are reported **uncertain**;
- new statements with no provenance are reported **added**.

Line numbers are used only for exact-equality proof and for display — never
for offset-based guessing.

## Many-to-one merges keep sources separately

A merge produces a single inherited entry whose `sources` array preserves
each originating statement with its own `hits` — sources are never summed
into one number, so downstream tooling can decide how to combine them.

## Output

`MigrationReport` with four parts:

- `inherited` — new statements with proven sources (`sources[]` keeps every
  origin with its own hits; `provenance` records which rule proved it);
- `added` — new statements without provenance;
- `uncertain` — old statements without a proven target, with `reason` and
  optional `candidates`;
- `diagnostics` — rule conflicts, dangling references, ambiguous keys.

## Usage

```sh
npm install
npm test          # type-check, build, run tests
npm run migrate -- --old examples/old.json --new examples/new.json \
  --rules examples/rules.json
# JSON output:
npm run migrate -- --old examples/old.json --new examples/new.json \
  --rules examples/rules.json --format json --out report.json
```

Exit code is `1` when any statement remains uncertain, so CI can gate on
unresolved coverage.

### Explicit rule shapes

```json
{ "kind": "move",  "from": "s1", "to": "n9", "reason": "extracted to helper" }
{ "kind": "split", "from": "s1", "to": ["n1", "n2"] }
{ "kind": "merge", "from": ["s1", "s2"], "to": "n1" }
```

## Layout

- `src/types.ts` — data model and report types
- `src/ranges.ts` — range equality / text normalization helpers
- `src/rules.ts` — explicit rule accessors
- `src/migrate.ts` — migration engine (`migrateCoverage`)
- `src/cli.ts` — command-line interface
- `test/migrate.test.ts` — engine tests (no framework, plain assertions)
