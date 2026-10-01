"""Fairness model checker for finite-state transition systems (Python 3.11+, stdlib only).

For user-specified weak-fair and strong-fair actions, searches the reachable
state space of a finite model for infinite-loop witnesses (lassos:
prefix + cycle) that violate the fairness constraint.

Terminology is kept strictly distinct throughout:
  * CONTINUOUS enablement: the action is enabled in *every* state of a loop.
  * INTERMITTENT enablement: the action is enabled in *some but not all* loop states.

Rules:
  * Weak fairness is violated by a loop where the action is continuously
    enabled but never executed.
  * Strong fairness is violated by a loop where the action is enabled at all
    (continuously or intermittently) but never executed.
  * If the state space is not fully explored (search limit hit), the verdict
    stays PENDING instead of SATISFIED.
  * Every counterexample is a replayable witness: replay() re-executes the
    prefix and iterates the loop, re-deriving enablement and the violation.
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from enum import Enum
from typing import Hashable, Optional

State = Hashable
Action = Hashable
Step = tuple  # (source, action, target)


class Verdict(Enum):
    SATISFIED = "satisfied"
    VIOLATED = "violated"
    PENDING = "pending"


class FairnessKind(Enum):
    WEAK = "weak"
    STRONG = "strong"


class Enablement(Enum):
    """How a fair action is enabled along a loop. Never conflate these."""
    CONTINUOUS = "continuous"      # enabled in every loop state
    INTERMITTENT = "intermittent"  # enabled in some, but not all, loop states
    NEVER = "never"                # enabled in no loop state


class TransitionSystem:
    """A finite-state model: initial state plus labelled transitions.

    transitions: mapping state -> iterable of (action, target) pairs.
    """

    def __init__(self, initial: State, transitions: dict):
        self.initial = initial
        self._transitions = {s: tuple(ts) for s, ts in transitions.items()}
        self._transitions.setdefault(initial, ())

    def transitions(self, state: State) -> tuple:
        return self._transitions.get(state, ())

    def enabled(self, state: State) -> set:
        return {a for a, _ in self.transitions(state)}


@dataclass(frozen=True)
class Witness:
    """A replayable counterexample: prefix from the initial state, then a loop."""
    action: Action
    kind: FairnessKind
    prefix: tuple  # of Step
    loop: tuple    # of Step; last target == first source, so it repeats forever
    enablement: Enablement


@dataclass(frozen=True)
class ActionResult:
    action: Action
    kind: FairnessKind
    verdict: Verdict
    witness: Optional[Witness] = None
    detail: str = ""


@dataclass(frozen=True)
class CheckResult:
    results: tuple  # of ActionResult
    explored: int
    complete: bool  # False iff exploration was truncated -> PENDING verdicts

    @property
    def verdict(self) -> Verdict:
        if any(r.verdict is Verdict.VIOLATED for r in self.results):
            return Verdict.VIOLATED
        if any(r.verdict is Verdict.PENDING for r in self.results):
            return Verdict.PENDING
        return Verdict.SATISFIED

    def for_action(self, action: Action, kind: FairnessKind) -> ActionResult:
        for r in self.results:
            if r.action == action and r.kind is kind:
                return r
        raise KeyError((action, kind))


@dataclass(frozen=True)
class ReplayReport:
    ok: bool                    # every step is a legal transition and the loop closes
    action: Action = None
    kind: Optional[FairnessKind] = None
    enablement: Optional[Enablement] = None
    executed_in_loop: Optional[bool] = None
    violation_confirmed: bool = False
    iterations_checked: int = 0
    error: str = ""


# ---------------------------------------------------------------------------
# Graph internals
# ---------------------------------------------------------------------------

def _sorted(nodes) -> list:
    return sorted(nodes, key=repr)


def _explore(model: TransitionSystem, max_states: Optional[int]):
    """BFS from the initial state. Returns (reachable_states, truncated)."""
    seen = {model.initial}
    queue = deque([model.initial])
    while queue:
        s = queue.popleft()
        for _, t in model.transitions(s):
            if t not in seen:
                if max_states is not None and len(seen) >= max_states:
                    return seen, True
                seen.add(t)
                queue.append(t)
    return seen, False


def _sccs(nodes, edges) -> list:
    """Iterative Tarjan SCC. edges: node -> iterable of (action, target)."""
    index_of, lowlink, on_stack, stack, out = {}, {}, set(), [], []
    counter = [0]

    for root in _sorted(nodes):
        if root in index_of:
            continue
        index_of[root] = lowlink[root] = counter[0]
        counter[0] += 1
        stack.append(root)
        on_stack.add(root)
        work = [(root, iter(edges.get(root, ())))]
        while work:
            node, it = work[-1]
            descended = False
            for _, nxt in it:
                if nxt not in nodes:
                    continue
                if nxt not in index_of:
                    index_of[nxt] = lowlink[nxt] = counter[0]
                    counter[0] += 1
                    stack.append(nxt)
                    on_stack.add(nxt)
                    work.append((nxt, iter(edges.get(nxt, ()))))
                    descended = True
                    break
                if nxt in on_stack:
                    lowlink[node] = min(lowlink[node], index_of[nxt])
            if descended:
                continue
            work.pop()
            if work:
                parent = work[-1][0]
                lowlink[parent] = min(lowlink[parent], lowlink[node])
            if lowlink[node] == index_of[node]:
                scc = []
                while True:
                    w = stack.pop()
                    on_stack.discard(w)
                    scc.append(w)
                    if w == node:
                        break
                out.append(scc)
    return out


def _is_cyclic(scc, edges) -> bool:
    if len(scc) > 1:
        return True
    s = scc[0]
    return any(t == s for _, t in edges.get(s, ()))


def _cycle_through(start, nodes, edges) -> Optional[tuple]:
    """A concrete cycle (tuple of Steps) from start back to start within nodes."""
    for a, t in edges.get(start, ()):
        if t == start:
            return ((start, a, start),)
    prev = {start: None}
    queue = deque([start])
    while queue:
        node = queue.popleft()
        for a, t in edges.get(node, ()):
            if t not in nodes:
                continue
            if t == start and node != start:
                steps = []
                cur = node
                while cur != start:
                    p, act = prev[cur]
                    steps.append((p, act, cur))
                    cur = p
                steps.reverse()
                steps.append((node, a, start))
                return tuple(steps)
            if t not in prev:
                prev[t] = (node, a)
                queue.append(t)
    return None


def _path_to(edges, initial, target) -> tuple:
    """BFS path (tuple of Steps) from initial to target; empty if equal."""
    if target == initial:
        return ()
    prev = {initial: None}
    queue = deque([initial])
    while queue:
        s = queue.popleft()
        for a, t in edges.get(s, ()):
            if t in prev:
                continue
            prev[t] = (s, a)
            if t == target:
                steps = []
                cur = t
                while prev[cur] is not None:
                    p, act = prev[cur]
                    steps.append((p, act, cur))
                    cur = p
                steps.reverse()
                return tuple(steps)
            queue.append(t)
    raise ValueError(f"no path to {target!r}")


def _classify(flags) -> Enablement:
    if all(flags):
        return Enablement.CONTINUOUS
    if any(flags):
        return Enablement.INTERMITTENT
    return Enablement.NEVER


# ---------------------------------------------------------------------------
# Fairness check
# ---------------------------------------------------------------------------

def _check_action(model, edges, cyclic_sccs, action, kind) -> ActionResult:
    for scc in cyclic_sccs:
        scc_set = set(scc)
        if kind is FairnessKind.WEAK:
            # Continuous enablement: restrict to states where the action is enabled.
            cand = {s for s in scc_set if action in model.enabled(s)}
        else:
            # Intermittent suffices: keep all states, require one enabled state later.
            cand = set(scc_set)
        if not cand:
            continue
        # The action must never be executed on the loop: drop its edges.
        sub_edges = {
            s: [(a, t) for a, t in edges.get(s, ()) if t in cand and a != action]
            for s in cand
        }
        for sub in _sccs(cand, sub_edges):
            if not _is_cyclic(sub, sub_edges):
                continue
            if kind is FairnessKind.STRONG:
                enabled_here = _sorted(s for s in sub if action in model.enabled(s))
                if not enabled_here:
                    continue
                start = enabled_here[0]
            else:
                start = _sorted(sub)[0]
            loop = _cycle_through(start, set(sub), sub_edges)
            if loop is None:
                continue
            enablement = _classify(
                [action in model.enabled(s) for (s, _, _) in loop]
            )
            if kind is FairnessKind.WEAK and enablement is not Enablement.CONTINUOUS:
                continue
            prefix = _path_to(edges, model.initial, loop[0][0])
            witness = Witness(action, kind, prefix, loop, enablement)
            adverb = {
                Enablement.CONTINUOUS: "continuously",
                Enablement.INTERMITTENT: "intermittently",
                Enablement.NEVER: "never",
            }[enablement]
            detail = (
                f"loop found where {action!r} is {adverb} enabled "
                f"but never executed"
            )
            return ActionResult(action, kind, Verdict.VIOLATED, witness, detail)
    return ActionResult(
        action, kind, Verdict.SATISFIED,
        detail="no violating loop; state space fully explored",
    )


def check_fairness(model: TransitionSystem,
                   weak=(), strong=(),
                   max_states: Optional[int] = None) -> CheckResult:
    """Check weak/strong fairness of the given actions on a finite model.

    max_states bounds exploration; if the bound cuts the search short, every
    action's verdict is PENDING (never SATISFIED) because loops may remain
    unexamined.
    """
    seen, truncated = _explore(model, max_states)
    actions = ([(a, FairnessKind.WEAK) for a in weak]
               + [(a, FairnessKind.STRONG) for a in strong])
    if truncated:
        return CheckResult(
            tuple(ActionResult(
                a, k, Verdict.PENDING,
                detail=f"state space not fully explored (limit {max_states} reached)",
            ) for a, k in actions),
            explored=len(seen),
            complete=False,
        )
    edges = {
        s: [(a, t) for a, t in model.transitions(s) if t in seen]
        for s in seen
    }
    cyclic_sccs = [scc for scc in _sccs(seen, edges) if _is_cyclic(scc, edges)]
    cyclic_sccs.sort(key=lambda scc: repr(_sorted(scc)[0]))
    return CheckResult(
        tuple(_check_action(model, edges, cyclic_sccs, a, k) for a, k in actions),
        explored=len(seen),
        complete=True,
    )


# ---------------------------------------------------------------------------
# Replay
# ---------------------------------------------------------------------------

def replay(model: TransitionSystem, witness: Witness, iterations: int = 2) -> ReplayReport:
    """Re-execute a witness on the model and re-verify the violation.

    The prefix is replayed once; the loop is replayed `iterations` times to
    demonstrate it closes and can repeat forever. Enablement along the loop
    is re-derived from the model, not trusted from the witness.
    """
    def fail(msg):
        return ReplayReport(ok=False, action=witness.action, kind=witness.kind,
                            error=msg)

    state = model.initial
    for (s, a, t) in witness.prefix:
        if s != state:
            return fail(f"prefix step source {s!r} != current state {state!r}")
        if (a, t) not in model.transitions(s):
            return fail(f"prefix step {(s, a, t)!r} is not a transition")
        state = t
    if not witness.loop:
        return fail("witness has no loop")
    loop_start = witness.loop[0][0]
    if state != loop_start:
        return fail(f"prefix ends at {state!r}, loop starts at {loop_start!r}")

    flags, executed = [], False
    for it in range(iterations):
        for (s, a, t) in witness.loop:
            if s != state:
                return fail(f"loop step source {s!r} != current state {state!r}")
            if (a, t) not in model.transitions(s):
                return fail(f"loop step {(s, a, t)!r} is not a transition")
            if it == 0:
                flags.append(witness.action in model.enabled(s))
                executed = executed or (a == witness.action)
            state = t
        if state != loop_start:
            return fail("loop does not close; cannot repeat forever")

    enablement = _classify(flags)
    if witness.kind is FairnessKind.WEAK:
        violated = not executed and enablement is Enablement.CONTINUOUS
    else:
        violated = not executed and enablement is not Enablement.NEVER
    return ReplayReport(
        ok=True,
        action=witness.action,
        kind=witness.kind,
        enablement=enablement,
        executed_in_loop=executed,
        violation_confirmed=violated,
        iterations_checked=iterations,
    )


# ---------------------------------------------------------------------------
# Demo
# ---------------------------------------------------------------------------

def _format_steps(steps) -> str:
    if not steps:
        return "(none)"
    out = [repr(steps[0][0])]
    for _, a, t in steps:
        out.append(f"-{a}->")
        out.append(repr(t))
    return " ".join(out)


def _demo(title, model, weak, strong, max_states=None):
    print(f"== {title} ==")
    result = check_fairness(model, weak=weak, strong=strong, max_states=max_states)
    print(f"explored states: {result.explored}, exploration complete: {result.complete}")
    for r in result.results:
        print(f"[{r.kind.value:6}] action {r.action!r}: {r.verdict.value} -- {r.detail}")
        if r.witness is not None:
            w = r.witness
            print(f"         enablement in loop: {w.enablement.value}")
            print(f"         prefix: {_format_steps(w.prefix)}")
            print(f"         loop:   {_format_steps(w.loop)}   (repeats forever)")
            rep = replay(model, w)
            print(f"         replay: ok={rep.ok}, "
                  f"violation confirmed={rep.violation_confirmed}, "
                  f"loop iterations checked={rep.iterations_checked}")
    print(f"overall verdict: {result.verdict.value}")
    print()


def main():
    # Demo 1: 'a' is continuously enabled on the t0 self-loop but never taken.
    model_wf = TransitionSystem("t0", {
        "t0": [("b", "t0"), ("a", "t1")],
        "t1": [("b", "t0")],
    })
    _demo("Weak fairness violated (continuous enablement)",
          model_wf, weak=("a",), strong=())

    # Demo 2: 'a' is enabled only in s0, so on the s0<->s1 loop it is
    # intermittently enabled: weak fairness holds, strong fairness fails.
    model_sf = TransitionSystem("s0", {
        "s0": [("b", "s1"), ("a", "s2")],
        "s1": [("b", "s0")],
        "s2": [("b", "s2")],
    })
    _demo("Intermittent enablement: weak holds, strong violated",
          model_sf, weak=("a",), strong=("a",))

    # Demo 3: every loop executes 'a', so both fairness constraints hold.
    model_ok = TransitionSystem("u0", {
        "u0": [("a", "u1"), ("b", "u1")],
        "u1": [("a", "u0")],
    })
    _demo("Fairness satisfied (action executed in every loop)",
          model_ok, weak=("a",), strong=("a",))

    # Demo 4: exploration limit hit before the state space is exhausted.
    _demo("Truncated search stays pending",
          model_wf, weak=("a",), strong=(), max_states=1)


if __name__ == "__main__":
    main()
