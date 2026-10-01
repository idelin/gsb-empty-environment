"""Unrolling a program into a finite state graph.

Breadth-first exploration over normalized states.  Every edge carries the
`Evidence` (source location + description) of the step that produced it, and
`loc_index` keeps the source-location -> states mapping.  Exploration is
bounded by explicit budgets; hitting a budget marks the graph incomplete,
which downstream verification must report as undecided, never as success.
"""

from collections import deque
from dataclasses import dataclass, field

from .executor import initial_state, step
from .state import normalize


@dataclass
class Graph:
    states: dict = field(default_factory=dict)       # State -> State
    edges: dict = field(default_factory=dict)        # State -> [(State, Evidence)]
    loc_index: dict = field(default_factory=dict)    # loc -> [State] reached by it
    start: object = None
    complete: bool = True
    steps: int = 0

    @property
    def fault_states(self):
        return [s for s in self.states if s.fault is not None]


def explore(program, dom, max_states=10_000, max_steps=100_000):
    start = normalize(initial_state(program))
    g = Graph(start=start)
    g.states[start] = start
    queue = deque([start])
    while queue:
        if len(g.states) >= max_states or g.steps >= max_steps:
            g.complete = False  # budget exhausted: exploration is incomplete
            break
        src = queue.popleft()
        out = step(src, dom)
        if out is None:
            continue
        dst, ev = out
        dst = normalize(dst)
        g.steps += 1
        g.edges.setdefault(src, []).append((dst, ev))
        bucket = g.loc_index.setdefault(ev.loc, [])
        if dst not in bucket:
            bucket.append(dst)
        if dst not in g.states:
            g.states[dst] = dst
            queue.append(dst)
    return g
