"""Matching rules for weak and branching simulation.

Relations are sets of ``(impl_state, spec_state)`` pairs.  The *specification*
moves must be matched by the *implementation*: a relation ``R`` is a
simulation pre-fixpoint when, for every ``(t, s) in R``, every move of ``s``
has a response from ``t`` through ``R``.  Internal ``tau`` moves of the
implementation never have to be matched by a visible specification move.

Weak simulation uses plain tau closure (``t =tau*=>``):

    s -tau-> s'   is answered by  t =tau*=> t'  with (t', s') in R
    s -a-> s'     is answered by  t =tau*=> . -a-> . =tau*=> t' with (t', s') in R

Branching simulation uses *related* tau paths: every intermediate state of
the stuttering tau path has to stay related to the current specification
state (before the visible move) or to its successor (after it).  This is
strictly stronger than weak simulation.
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from typing import Callable

from .lts import LTS, TAU


@dataclass(frozen=True)
class Attack:
    """A specification move that the implementation state cannot match.

    kind == "visible"    : spec did ``action`` and no weak/branching answer exists
    kind == "stutter"    : spec did an internal tau move that cannot be stutter-matched
    kind == "divergence" : implementation may run away in an infinite tau
                           computation while the specification cannot
    """

    kind: str
    action: str | None
    spec_next: str | None

    def to_dict(self) -> dict:
        return {"kind": self.kind, "action": self.action, "spec_next": self.spec_next}

    @classmethod
    def from_dict(cls, data: dict) -> "Attack":
        return cls(data["kind"], data["action"], data["spec_next"])


@dataclass(frozen=True)
class Response:
    """One concrete implementation answer to an attack.

    ``endpoint`` is the implementation state after the matching move and
    ``path`` is a replayable concrete path from the challenged state as a
    list of ``(label, state)`` steps (tau steps first, then the visible move,
    then tau steps), possibly empty for a zero-step stutter answer.
    """

    endpoint: str
    path: tuple[tuple[str, str], ...]

    def to_dict(self) -> dict:
        return {"endpoint": self.endpoint, "path": [[label, state] for label, state in self.path]}


def tau_closure(lts: LTS, seeds: set[str] | str) -> set[str]:
    """States reachable through zero or more tau steps (BFS).

    The visited set makes tau loops terminate immediately instead of looping.
    """
    seen = set(seeds)
    queue: deque[str] = deque(seen)
    while queue:
        cur = queue.popleft()
        for nxt in lts.tau_successors(cur):
            if nxt not in seen:
                seen.add(nxt)
                queue.append(nxt)
    return seen


def branching_reachable(
    lts: LTS, relation: set[tuple[str, str]], spec_state: str, impl_state: str
) -> set[str]:
    """States reachable from ``impl_state`` via tau paths on which every state
    *after the starting state* is related to ``spec_state``.

    This is the stuttering region used by branching simulation:

    * before a visible ``a`` move, the implementation state that performs the
      ``a`` must itself be related to the current specification state;
    * after the move, every intermediate and the endpoint must be related to
      the specification successor.

    The starting state is always included (the empty tau path); whether the
    *endpoint* may answer is still checked by the caller (e.g. the post-action
    endpoint must be related to the specification successor).  Only BFS over a
    finite visited set keeps tau loops from unfolding forever.
    """
    seen = {impl_state}
    queue: deque[str] = deque([impl_state])
    while queue:
        cur = queue.popleft()
        for nxt in lts.tau_successors(cur):
            if nxt in seen:
                continue
            if (nxt, spec_state) not in relation:
                continue
            seen.add(nxt)
            queue.append(nxt)
    return seen


def _tau_path_with_pred(
    lts: LTS,
    src: str,
    dst: str,
    may_pass: Callable[[str], bool],
) -> tuple[tuple[str, str], ...] | None:
    """BFS for a tau path ``src -> dst``, only expanding states that may be
    intermediate states according to ``may_pass``.  Returns the concrete step
    sequence (labels, states) or None when unreachable.
    """
    if src == dst:
        return ()
    parent: dict[str, tuple[str, str] | None] = {src: None}
    queue: deque[str] = deque([src])
    while queue:
        cur = queue.popleft()
        for nxt in lts.tau_successors(cur):
            if nxt in parent:
                continue
            if nxt != dst and not may_pass(nxt):
                continue
            parent[nxt] = (cur, TAU)
            if nxt == dst:
                steps: list[tuple[str, str]] = []
                node = dst
                while parent[node] is not None:
                    prev, label = parent[node]  # type: ignore[misc]
                    steps.append((label, node))
                    node = prev
                return tuple(reversed(steps))
            queue.append(nxt)
    return None


def _tau_sccs(lts: LTS) -> list[list[str]]:
    """SCCs of the tau subgraph computed with *iterative* Kosaraju so that
    large tau cycles cannot hit Python recursion limits.
    """
    states = sorted(lts.states())
    visited: set[str] = set()
    order: list[str] = []
    for start in states:
        if start in visited:
            continue
        visited.add(start)
        stack: list[tuple[str, object]] = [(start, iter(lts.tau_successors(start)))]
        while stack:
            node, iterator = stack[-1]
            advanced = False
            for nxt in iterator:  # type: ignore[union-attr]
                if nxt not in visited:
                    visited.add(nxt)
                    stack.append((nxt, iter(lts.tau_successors(nxt))))
                    advanced = True
                    break
            if not advanced:
                order.append(node)
                stack.pop()

    predecessors: dict[str, list[str]] = {state: [] for state in states}
    for src in states:
        for dst in lts.tau_successors(src):
            predecessors[dst].append(src)

    assigned: set[str] = set()
    components: list[list[str]] = []
    for start in reversed(order):
        if start in assigned:
            continue
        component: list[str] = []
        stack2 = [start]
        assigned.add(start)
        while stack2:
            node = stack2.pop()
            component.append(node)
            for prev in predecessors.get(node, []):
                if prev not in assigned:
                    assigned.add(prev)
                    stack2.append(prev)
        components.append(component)
    return components


def divergent_states(lts: LTS) -> set[str]:
    """States that can engage in an infinite tau computation.

    On a finite graph this is exactly the set of states that can reach, via
    tau steps, a tau cycle: a non-trivial SCC of the tau subgraph or a tau
    self-loop.  Computed once from SCCs plus backward tau reachability.
    """
    cyclic: set[str] = set()
    for component in _tau_sccs(lts):
        if len(component) > 1:
            cyclic.update(component)
        else:
            only = component[0]
            if only in lts.tau_successors(only):
                cyclic.add(only)

    predecessors: dict[str, list[str]] = {state: [] for state in lts.states()}
    for src in lts.states():
        for dst in lts.tau_successors(src):
            predecessors[dst].append(src)

    divergent = set(cyclic)
    queue: deque[str] = deque(cyclic)
    while queue:
        cur = queue.popleft()
        for prev in predecessors.get(cur, []):
            if prev not in divergent:
                divergent.add(prev)
                queue.append(prev)
    return divergent


# ---------------------------------------------------------------------------
# Attack computation (what the specification can do that the implementation
# cannot answer).  An empty attack list means the pair is good.
# ---------------------------------------------------------------------------


def _divergence_attack(
    t: str,
    s: str,
    impl_divergent: set[str] | None,
    spec_divergent: set[str] | None,
) -> Attack | None:
    if impl_divergent is None or spec_divergent is None:
        return None
    if t in impl_divergent and s not in spec_divergent:
        return Attack("divergence", None, None)
    return None


def weak_attacks(
    impl: LTS,
    spec: LTS,
    relation: set[tuple[str, str]],
    impl_state: str,
    spec_state: str,
    impl_divergent: set[str] | None = None,
    spec_divergent: set[str] | None = None,
) -> list[Attack]:
    """Spec moves from ``spec_state`` without a weak-match answer through ``relation``."""
    attacks: list[Attack] = []
    post = tau_closure(impl, {impl_state})
    for action, spec_next in spec.successors(spec_state):
        if action == TAU:
            if any((impl_next, spec_next) in relation for impl_next in post):
                continue
            attacks.append(Attack("stutter", TAU, spec_next))
            continue
        matched = False
        for before in sorted(post):
            for label, after_a in impl.successors(before, action):
                if any((impl_next, spec_next) in relation for impl_next in tau_closure(impl, {after_a})):
                    matched = True
                    break
            if matched:
                break
        if not matched:
            attacks.append(Attack("visible", action, spec_next))
    divergence = _divergence_attack(impl_state, spec_state, impl_divergent, spec_divergent)
    if divergence is not None:
        attacks.append(divergence)
    return attacks


def branching_attacks(
    impl: LTS,
    spec: LTS,
    relation: set[tuple[str, str]],
    impl_state: str,
    spec_state: str,
    impl_divergent: set[str] | None = None,
    spec_divergent: set[str] | None = None,
) -> list[Attack]:
    """Spec moves from ``spec_state`` without a branching-match answer through ``relation``."""
    attacks: list[Attack] = []
    for action, spec_next in spec.successors(spec_state):
        if action == TAU:
            # A zero-step answer (t, s') in R always stutter-matches s -tau-> s'.
            if (impl_state, spec_next) in relation:
                continue
            endpoints = branching_reachable(impl, relation, spec_state, impl_state)
            if any((impl_next, spec_next) in relation for impl_next in endpoints):
                continue
            attacks.append(Attack("stutter", TAU, spec_next))
            continue
        matched = False
        pre = branching_reachable(impl, relation, spec_state, impl_state)
        for before in sorted(pre):
            for label, after_a in impl.successors(before, action):
                post = branching_reachable(impl, relation, spec_next, after_a)
                if any((impl_next, spec_next) in relation for impl_next in post):
                    matched = True
                    break
            if matched:
                break
        if not matched:
            attacks.append(Attack("visible", action, spec_next))
    divergence = _divergence_attack(impl_state, spec_state, impl_divergent, spec_divergent)
    if divergence is not None:
        attacks.append(divergence)
    return attacks


ATTACK_FUNCTIONS = {
    "weak": weak_attacks,
    "branching": branching_attacks,
}


# ---------------------------------------------------------------------------
# Response enumeration: every concrete implementation answer to an attack.
# Used to build the counterexample strategy graph; the graph replays by
# re-enumerating and comparing endpoint sets.
# ---------------------------------------------------------------------------


def _compose(
    prefix: tuple[tuple[str, str], ...],
    action: str | None,
    middle: str | None,
    suffix: tuple[tuple[str, str], ...],
) -> tuple[tuple[str, str], ...]:
    if action is None:
        return prefix + suffix
    return prefix + ((action, middle),) + suffix  # type: ignore[arg-type]


def weak_responses(impl: LTS, impl_state: str, attack: Attack) -> list[Response]:
    """All weak answers: visible attack -> t =tau* a tau*=> endpoint;
    stutter attack -> t =tau*=> endpoint (including zero steps)."""
    responses: dict[str, Response] = {}
    post = tau_closure(impl, {impl_state})
    if attack.kind == "stutter":
        for endpoint in sorted(post):
            if endpoint not in responses:
                path = _tau_path_with_pred(impl, impl_state, endpoint, lambda _state: True)
                responses[endpoint] = Response(endpoint, path or ())
        return list(responses.values())
    if attack.kind != "visible":
        return []
    action = attack.action
    for before in sorted(post):
        prefix = _tau_path_with_pred(impl, impl_state, before, lambda _state: True) or ()
        for label, after_a in impl.successors(before, action):
            for endpoint in sorted(tau_closure(impl, {after_a})):
                if endpoint in responses:
                    continue
                suffix = _tau_path_with_pred(impl, after_a, endpoint, lambda _state: True) or ()
                responses[endpoint] = Response(endpoint, _compose(prefix, action, after_a, suffix))
    return list(responses.values())


def branching_responses(
    impl: LTS,
    relation: set[tuple[str, str]],
    impl_state: str,
    spec_state: str,
    attack: Attack,
) -> list[Response]:
    """All branching answers; tau paths may only traverse states related to
    the specification state active for that segment of the move."""
    responses: dict[str, Response] = {}
    if attack.kind == "stutter":
        spec_next = attack.spec_next
        may = lambda state: (state, spec_state) in relation  # noqa: E731
        endpoints = branching_reachable(impl, relation, spec_state, impl_state)
        for endpoint in sorted(endpoints):
            if endpoint not in responses:
                path = _tau_path_with_pred(impl, impl_state, endpoint, may)
                responses[endpoint] = Response(endpoint, path or ())
        return list(responses.values())
    if attack.kind != "visible":
        return []
    action = attack.action
    spec_next = attack.spec_next
    pre = branching_reachable(impl, relation, spec_state, impl_state)
    may_pre = lambda state: (state, spec_state) in relation  # noqa: E731
    may_post = lambda state: (state, spec_next) in relation  # noqa: E731
    for before in sorted(pre):
        prefix = _tau_path_with_pred(impl, impl_state, before, may_pre) or ()
        for label, after_a in impl.successors(before, action):
            post = branching_reachable(impl, relation, spec_next, after_a)
            for endpoint in sorted(post):
                if endpoint in responses:
                    continue
                suffix = _tau_path_with_pred(impl, after_a, endpoint, may_post) or ()
                responses[endpoint] = Response(endpoint, _compose(prefix, action, after_a, suffix))
    return list(responses.values())

RESPONSE_FUNCTIONS = {
    "weak": weak_responses,
    "branching": branching_responses,
}
