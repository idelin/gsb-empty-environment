"""Verification over the explored finite state model.

Verdicts are only about the given finite state model:
  SAFE    - the model was fully explored and contains no fault state;
  UNSAFE  - a fault state is reachable; a shortest counterexample trace is
            returned and independently replayed by replay.interpret;
  UNKNOWN - a budget cut the exploration short, so the model was not fully
            searched.  Budget termination is never reported as success.
"""

from collections import deque
from dataclasses import dataclass

from .explore import explore
from .replay import replay_counterexample
from .state import summarize

SAFE = "SAFE"
UNSAFE = "UNSAFE"
UNKNOWN = "UNKNOWN"


@dataclass(frozen=True)
class TraceStep:
    loc: str
    kind: str
    desc: str
    state: str


@dataclass
class Verdict:
    result: str
    trace: tuple = None        # tuple[TraceStep] for UNSAFE
    replay_ok: object = None   # True/False for UNSAFE, None otherwise
    fault_kind: str = None
    explored_states: int = 0
    explored_steps: int = 0
    detail: str = ""


def _shortest_trace(g, target):
    parent = {g.start: None}
    queue = deque([g.start])
    while queue:
        s = queue.popleft()
        if s is target:
            break
        for d, ev in g.edges.get(s, ()):
            if d not in parent:
                parent[d] = (s, ev)
                queue.append(d)
    rev = []
    s = target
    while parent[s] is not None:
        p, ev = parent[s]
        rev.append(TraceStep(ev.loc, ev.kind, ev.desc, summarize(s)))
        s = p
    return tuple(reversed(rev))


def verify(program, dom, max_states=10_000, max_steps=100_000, check_replay=True):
    g = explore(program, dom, max_states=max_states, max_steps=max_steps)
    if g.fault_states:
        # BFS over the (deterministic) graph yields a shortest counterexample.
        parent = {g.start: None}
        queue = deque([g.start])
        target = None
        while queue and target is None:
            s = queue.popleft()
            for d, _ in g.edges.get(s, ()):
                if d not in parent:
                    parent[d] = s
                    if d.fault is not None:
                        target = d
                        break
                    queue.append(d)
        trace = _shortest_trace(g, target)
        ok = (
            replay_counterexample(program, dom, trace, target.fault.kind)
            if check_replay
            else None
        )
        return Verdict(
            UNSAFE,
            trace=trace,
            replay_ok=ok,
            fault_kind=target.fault.kind,
            explored_states=len(g.states),
            explored_steps=g.steps,
            detail=f"fault reachable: {target.fault}",
        )
    if not g.complete:
        return Verdict(
            UNKNOWN,
            explored_states=len(g.states),
            explored_steps=g.steps,
            detail="budget exhausted; model not fully explored (undecided)",
        )
    return Verdict(
        SAFE,
        explored_states=len(g.states),
        explored_steps=g.steps,
        detail="model fully explored; no fault state",
    )
