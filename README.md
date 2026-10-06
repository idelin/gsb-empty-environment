# cyclecheck

Dependency-graph cycle checker where every edge carries a boolean condition
over at most 10 switches.

## How it works

1. Enumerate all `2^n` switch configurations (`n <= 10`).
2. For each configuration, evaluate every edge's condition under **that single
   configuration only**, build the concrete graph, and run a topological sort
   (Kahn's algorithm). Because configurations are checked independently, edges
   guarded by mutually exclusive conditions (e.g. `x` vs `!x`) can never be
   stitched into a fake cycle.
3. If a configuration makes the graph cyclic, stop at the lexicographically
   smallest configuration bit string and extract one real cycle by DFS over
   the nodes that survive topological elimination.
4. If no configuration cycles, report the full enumeration count (`2^n`).

## Input

JSON file (or `-` for stdin):

```json
{
  "switches": ["fast", "safe"],
  "edges": [
    {"from": "A", "to": "B", "when": "fast && !safe"},
    {"from": "B", "to": "A", "when": "safe"}
  ]
}
```

`when` is optional (empty = edge always present). Expression operators:
`!`, `&&`, `||` (single `&` / `|` accepted), parentheses, `true` / `false`.

## Output

Cyclic case — minimal bit string (switch declaration order) plus a real cycle:

```
cyclic configuration found
config: 10 (fast=1 safe=0)
cycle: A -> B -> A
```

Acyclic case:

```
no configuration produces a cycle
configurations enumerated: 4
```

## Build & test

```sh
go build -o cyclecheck .
go test ./...
./cyclecheck graph.json
```
