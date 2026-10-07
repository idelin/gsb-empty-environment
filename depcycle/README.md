# depcycle

Checks a dependency graph whose edges are guarded by boolean expressions
over at most 10 switches. It enumerates every switch configuration in
bit-string order (first declared switch = most significant bit), evaluates
the guards, and verifies each concrete graph with a per-configuration
topological sort — so edges whose guards are mutually exclusive can never
be stitched into a fake cycle.

## Input

JSON on stdin (or a file path as the first argument):

```json
{
  "switches": ["a", "b"],
  "edges": [
    {"from": "A", "to": "B", "when": "a && !b"},
    {"from": "B", "to": "A"}
  ]
}
```

`when` is optional (omitted = edge always present). Guard grammar:
`&&`, `||`, `!`, parentheses, switch identifiers, `true` / `false`.

## Output

- `CYCLE config=<bitstring> cycle=N0->N1->...->N0` — the lexicographically
  smallest configuration whose concrete graph is cyclic, plus one real
  closed loop extracted from that graph.
- `ACYCLIC count=<n>` — no configuration yields a cycle; `n` is the total
  number of enumerated configurations (`2^len(switches)`).

## Run

```sh
go test ./...
go build -o depcycle . && ./depcycle graph.json
```
