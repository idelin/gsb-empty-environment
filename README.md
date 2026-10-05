# cfg-taint-analyzer

Path-sensitive taint analyzer over a control-flow graph (CFG), written in
TypeScript with zero runtime dependencies. Given a CFG and a set of
problem-defined taint rules, it finds the **shortest feasible path** from a
taint source to a dangerous call, or proves that none exists.

## Taint rules (defined by the problem input)

All rules live in the input JSON under `rules`; nothing is hardcoded.

| Rule | Meaning |
| --- | --- |
| `sources` | `{id, type}` — a `source` node taints its variable and tags it with `type`. |
| `sanitizers` | `{id, types}` — a `sanitize` node cleans its variable **only if** the variable's current type is in `types`; otherwise it has no effect. |
| `sinks` | `{id, dangerTypes?}` — a `sink` node is dangerous when some argument is tainted and its type is in `dangerTypes` (any taint if omitted). |

## Transfer rules

- **assign** `x = f(y1..yn)`: `taint(x) = ∪ taint(yi)`; the type is the
  declared `type` or propagated from the first typed operand. Any previous
  sanitization certificate of `x` is dropped.
- **merge**: the analysis is path-sensitive — states from different
  predecessors coexist at join points (union of states, no widening), so
  per-path sanitization is never confused.
- **sanitize**: on a type match the taint set is cleared and the variable
  receives a sanitization certificate; on a mismatch the node is a no-op
  (the witness explains why).
- **alias**: `p = &x` records a points-to edge (`pointsToAny` models a
  pointer merged from several branches). `*p = e` (**store**) writes through
  every possible target and **invalidates the sanitization certificate** of
  each target: a unique target gets a strong update (taint replaced), a
  may-alias target gets a weak update (taint joined). `x = *p` (**load**)
  reads through every possible target.
- **sink**: dangerous per the sink rule above.

## Path feasibility (finite boolean conditions)

Edges may carry guards: conjunctions (`&&`) of `IDENT == LIT` /
`IDENT != LIT` with `LIT ∈ {true, false, number, "string"}`. Each guarded
variable has a finite domain (the literals it is compared against). The
analyzer tracks the allowed-value set per variable and **prunes branches
whose guards are mutually contradictory** (empty allowed set). Assigning to
a variable forgets its condition knowledge.

## Shortest path

The engine BFS-searches the product space `(node × data state × condition
state)`; the first dangerous sink reached yields a witness with the fewest
edges. The state space is finite (finite sources, types, aliases and
condition domains), so the search always terminates; a `--max-states` bound
guards against pathological inputs.

## Input format

```json
{
  "name": "demo",
  "entry": "n0",
  "rules": {
    "sources":    [{ "id": "user_input", "type": "string" }],
    "sanitizers": [{ "id": "escape_str", "types": ["string"] }],
    "sinks":      [{ "id": "exec", "dangerTypes": ["string"] }]
  },
  "nodes": [
    { "id": "n0", "op": "source",   "source": "user_input", "var": "u" },
    { "id": "n1", "op": "assign",   "var": "q", "expr": ["u"] },
    { "id": "n2", "op": "sanitize", "sanitizer": "escape_str", "var": "q" },
    { "id": "n3", "op": "alias",    "var": "p", "pointsTo": "q" },
    { "id": "n4", "op": "store",    "pointer": "p", "expr": ["u"] },
    { "id": "n5", "op": "load",     "var": "r", "pointer": "p" },
    { "id": "n6", "op": "sink",     "sink": "exec", "args": ["q"] }
  ],
  "edges": [
    { "from": "n0", "to": "n1" },
    { "from": "n1", "to": "n2", "cond": "flag == true && step != 2" }
  ]
}
```

Node ops: `nop`, `source`, `assign`, `sanitize`, `alias`, `store`, `load`,
`sink`. See `examples/` for complete graphs.

## Usage

```sh
npm run build                       # compile TypeScript to dist/
node dist/src/cli.js analyze examples/03-alias-invalidation.json
node dist/src/cli.js analyze examples/04-contradictory-branches.json --max-states 50000
npm test                            # build + run the test suite
```

Exit code is `0` when a dangerous feasible path is found (witness printed),
`1` when no feasible tainted path exists, `2` on usage/input errors.

## Layout

- `src/model.ts` — CFG + rule schema and validation
- `src/conditions.ts` — finite-domain boolean feasibility
- `src/state.ts` — abstract state and transfer functions
- `src/analyzer.ts` — shortest-feasible-path BFS engine
- `src/cli.ts` — command-line interface
- `examples/` — runnable example graphs
- `tests/run.ts` — dependency-free test suite
