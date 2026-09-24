# Dominator Tree with Incremental Edge Updates

Python 3.11, standard library only. Computes immediate dominators of all
vertices reachable from an entry over a directed multigraph with timed
edges, supports single-edge changes with affected-region reporting,
dominance queries and path counterexamples.

## Files

- `dominator.py` — `Graph` (string vertex/edge ids, parallel edges,
  isolated vertices, edges active on `[start, end)`) and `DominatorTree`
  (dataflow fixed point, never DFS parents; unreachable vertices kept
  out of the tree).
- `test_dominator.py` — unittest suite.
- `example.py` — runnable sample.

## API sketch

```python
g = Graph()
g.add_edge("e1", "S", "A")                 # interval defaults to always
g.add_edge("e2", "B", "T", start=10, end=20)  # active on [10, 20)
dt = DominatorTree(g, "S", time=10.0)

dt.idom("T")                # immediate dominator (ValueError if unreachable)
dt.unreachable              # frozenset of unreachable vertices
dt.dominates("A", "T")      # dominance query
dt.counterexample("A", "T") # path S->...->T avoiding A, or None
affected = dt.remove_edge("e1")   # recompute; returns affected region
affected = dt.add_edge("e3", "S", "T")
affected = dt.set_time(20.0)
```

## Run

```
python3 -m unittest -v
python3 example.py
```

## Test results

```
Ran 9 tests in 0.000s
OK
```

Sample output highlights:

- Diamond `S->A/B->T`: `idom(T) = S`; after removing `B->T`:
  `idom(T) = A`, affected region `{T}`.
- Unreachable cycle `X<->Y` and isolated `Z` stay in `unreachable`,
  never in the dominator tree.
- Timed edge on `[10, 20)`: `idom(T)` is `S` at `t=10`, `A` at `t=20`.
