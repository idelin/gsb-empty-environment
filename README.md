# Conditional Influence Propagation

Given changed seed nodes and conditional dependency edges, compute the
affected set with a shortest explanation chain per node. Conditions are
evaluated against an explicit fact set; fact changes re-derive explanations
incrementally (no stale appends).

## Files

- `influence.py` — core library (Python 3.11, standard library only)
- `test_influence.py` — unittest suite (18 tests)
- `demo.py` — runnable sample

## Model

- Vertices and edges have string IDs; isolated vertices are preserved and
  parallel edges (same endpoints, different IDs/conditions) are allowed.
- `Condition(all_of=..., any_of=...)`: holds when every `all_of` fact is
  present and at least one `any_of` fact is present.
- Edge time intervals are half-open: active for `start <= t < end`;
  `None` means unbounded. `affected(at=None)` ignores time.
- Explanation chains are shortest (fewest edges) paths grounded in a
  changed seed, found by BFS. Each step records the edge ID and the facts
  that witnessed the condition.

## Semantics

- `add_fact` / `remove_fact` take effect on the next `affected()` query,
  which re-derives the whole affected set from the live fact set.
- Revoking a fact that made an edge hold removes every node that loses its
  last piece of evidence (and its descendants); nodes reachable through
  another edge (alternative evidence) stay affected, and their explanation
  is updated to cite the surviving evidence.
- Cycles cannot self-prove: a pure cycle with no seed stays unaffected;
  a cycle grounded by a seed is explained through the seed, never through
  itself.

## Usage

```python
from influence import Condition, InfluenceGraph

g = InfluenceGraph()
g.add_vertex("isolated")                                   # kept, never auto-affected
g.add_edge("e1", "a", "b", Condition(all_of={"f1"}))
g.add_edge("e2", "b", "c", Condition(any_of={"f2", "f3"}), start=10, end=20)

g.mark_changed("a")
g.add_fact("f1", "f2")

for node, expl in g.affected(at=15).items():
    print(expl.render())   # a (changed seed) / a -[e1]-> b / a -[e1]-> b -[e2]-> c

g.remove_fact("f1")        # b and c lose all evidence and exit the affected set
```

## Run

```sh
python3.11 -m unittest -v   # 18 tests, OK
python3.11 demo.py          # step-by-step sample scenario
```
