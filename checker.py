"""Liveness checker for "every request is eventually answered".

Property checked (LTL): G(req -> F resp) over state labels, i.e. once a
state labelled "req" is entered, a state labelled "resp" must eventually
follow. A violation is a reachable non-fulfilling cycle: a lasso whose
loop keeps a request pending forever without ever reaching "resp".

Key design decisions:

* The search space is the product (state, pending) where "pending"
  records whether a request is still unanswered. A cycle made only of
  pending=True nodes is exactly a non-fulfilling cycle. Self-loops
  qualify as cycles.
* Fairness is weak fairness over explicitly given action sets only:
  for each set F, an infinite run is unfair if F is continuously
  enabled but never taken. A loop is excluded only when, for some
  declared set F, every state on the loop enables F and no loop step
  takes an action from F. Actions not listed in any set get no
  fairness assumption.
* Exploration is bounded by max_nodes. If the bound is hit before the
  augmented graph is fully explored, the verdict stays PENDING: a
  finite truncation that simply has not seen a response yet is not a
  violation. Only a genuine cycle proves VIOLATED, and only a complete
  search proves SATISFIED.
* Counterexamples are replayable traces (prefix + loop) that replay()
  re-executes step by step against the model.
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass, field
from enum import Enum

from fsm import Model

REQ = "req"
RESP = "resp"

DEFAULT_MAX_NODES = 100_000


class Verdict(Enum):
    SATISFIED = "satisfied"
    VIOLATED = "violated"
    PENDING = "pending"


@dataclass(frozen=True)
class Step:
    """One replayable step: taking `action` lands in `state`."""

    action: str
    state: str
    pending: bool


@dataclass
class Counterexample:
    """A lasso: `prefix` leads from the initial state to the loop entry,
    `loop` is a non-empty closed walk back to the loop entry."""

    prefix: list[Step]
    loop: list[Step]

    @property
    def steps(self) -> list[Step]:
        return self.prefix + self.loop

    def format(self) -> str:
        lines = []
        for step in self.prefix:
            lines.append(f"  prefix --{step.action}--> {step.state} "
                         f"(pending={step.pending})")
        for step in self.loop:
            lines.append(f"  loop   --{step.action}--> {step.state} "
                         f"(pending={step.pending})")
        return "\n".join(lines)


@dataclass
class IgnoredComponent:
    """A pending cycle that was excluded solely because of the declared
    fairness sets; recorded so fairness assumptions show up in evidence."""

    states: list[str]
    missing_fairness: list[frozenset[str]]
    sample_loop: list[Step]


@dataclass
class Evidence:
    explored_nodes: int = 0
    explored_edges: int = 0
    truncated: bool = False
    fairness_sets: list[frozenset[str]] = field(default_factory=list)
    ignored_unfair_components: list[IgnoredComponent] = field(default_factory=list)
    counterexample: Counterexample | None = None


@dataclass
class Result:
    verdict: Verdict
    evidence: Evidence


def pending_after(pending: bool, labels) -> bool:
    """Update the outstanding-request flag when entering a state.
    A response clears it; otherwise a request raises it."""
    if RESP in labels:
        return False
    if REQ in labels:
        return True
    return pending


def _tarjan_sccs(nodes, adjacency) -> list[list]:
    """Iterative Tarjan SCC over the induced adjacency (action, node)."""
    index_of: dict = {}
    lowlink: dict = {}
    on_stack: set = set()
    stack: list = []
    sccs: list[list] = []
    counter = 0
    for root in nodes:
        if root in index_of:
            continue
        index_of[root] = lowlink[root] = counter
        counter += 1
        stack.append(root)
        on_stack.add(root)
        work = [(root, iter(adjacency.get(root, [])))]
        while work:
            node, edges = work[-1]
            descended = False
            for _, nxt in edges:
                if nxt not in index_of:
                    index_of[nxt] = lowlink[nxt] = counter
                    counter += 1
                    stack.append(nxt)
                    on_stack.add(nxt)
                    work.append((nxt, iter(adjacency.get(nxt, []))))
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
                    member = stack.pop()
                    on_stack.discard(member)
                    scc.append(member)
                    if member == node:
                        break
                sccs.append(scc)
    return sccs


def _bfs_path(start, goal, allowed, adjacency):
    """Shortest path from start to goal through `allowed` nodes.
    Returns a list of (action, node) steps, [] when start == goal."""
    previous = {start: None}
    queue = deque([start])
    while queue:
        node = queue.popleft()
        if node == goal:
            break
        for action, nxt in adjacency.get(node, []):
            if nxt in allowed and nxt not in previous:
                previous[nxt] = (node, action)
                queue.append(nxt)
    if goal not in previous:
        return None
    path = []
    cursor = goal
    while previous[cursor] is not None:
        before, action = previous[cursor]
        path.append((action, cursor))
        cursor = before
    path.reverse()
    return path


def check(model: Model, fairness_sets=(), max_nodes: int = DEFAULT_MAX_NODES) -> Result:
    """Check G(req -> F resp) on `model`.

    fairness_sets: iterable of iterables of action names; weak fairness
    is assumed for exactly these sets and nothing else.
    max_nodes: bound on augmented (state, pending) nodes explored; if
    the bound cuts the search short the verdict is PENDING.
    """
    fairness = [frozenset(group) for group in fairness_sets]
    num_sets = len(fairness)
    full_mask = (1 << num_sets) - 1

    def labels_of(state):
        return model.labels.get(state, frozenset())

    def node_mask(state):
        """Bit i is set when fairness set i is disabled at `state`."""
        enabled = model.actions_enabled(state)
        mask = 0
        for i, group in enumerate(fairness):
            if not (enabled & group):
                mask |= 1 << i
        return mask

    def edge_mask(action):
        """Bit i is set when `action` belongs to fairness set i."""
        mask = 0
        for i, group in enumerate(fairness):
            if action in group:
                mask |= 1 << i
        return mask

    root = (model.initial, pending_after(False, labels_of(model.initial)))
    parent: dict = {root: None}
    adjacency: dict = {}
    order = [root]
    queue = deque([root])
    explored_edges = 0
    truncated = False
    while queue:
        if len(order) > max_nodes:
            truncated = True
            break
        node = queue.popleft()
        state, pending = node
        for transition in model.outgoing(state):
            explored_edges += 1
            nxt = (transition.dst, pending_after(pending, labels_of(transition.dst)))
            adjacency.setdefault(node, []).append((transition.action, nxt))
            if nxt not in parent:
                parent[nxt] = (node, transition.action)
                order.append(nxt)
                queue.append(nxt)

    evidence = Evidence(
        explored_nodes=len(order),
        explored_edges=explored_edges,
        truncated=truncated,
        fairness_sets=list(fairness),
    )

    pending_nodes = [node for node in order if node[1]]
    pending_set = set(pending_nodes)
    sub_adjacency = {
        node: [(action, nxt) for action, nxt in adjacency.get(node, [])
               if nxt in pending_set]
        for node in pending_nodes
    }

    def prefix_to(node) -> list[Step]:
        steps = []
        cursor = node
        while parent[cursor] is not None:
            before, action = parent[cursor]
            steps.append(Step(action, cursor[0], cursor[1]))
            cursor = before
        steps.reverse()
        return steps

    def build_loop(entry, scc_members, elements) -> list[Step]:
        """Closed walk from `entry` back to `entry` visiting every
        element; elements are ("node", n) or ("edge", u, action, v)."""
        allowed = set(scc_members)
        walk: list = []
        cursor = entry
        for element in elements:
            if element[0] == "node":
                target = element[1]
                walk += _bfs_path(cursor, target, allowed, sub_adjacency)
                cursor = target
            else:
                _, src, action, dst = element
                walk += _bfs_path(cursor, src, allowed, sub_adjacency)
                walk.append((action, dst))
                cursor = dst
        walk += _bfs_path(cursor, entry, allowed, sub_adjacency)
        return [Step(action, node[0], node[1]) for action, node in walk]

    def intra_edges(scc_members):
        allowed = set(scc_members)
        for member in scc_members:
            for action, nxt in sub_adjacency.get(member, []):
                if nxt in allowed:
                    yield member, action, nxt

    for scc in _tarjan_sccs(pending_nodes, sub_adjacency):
        edges = list(intra_edges(scc))
        has_cycle = len(scc) > 1 or any(src == dst for src, _, dst in edges)
        if not has_cycle:
            continue
        coverage = 0
        for member in scc:
            coverage |= node_mask(member[0])
        for _, action, _ in edges:
            coverage |= edge_mask(action)
        if coverage & full_mask == full_mask:
            elements = []
            if num_sets == 0:
                src, action, dst = edges[0]
                elements.append(("edge", src, action, dst))
            else:
                for i in range(num_sets):
                    bit = 1 << i
                    element = None
                    for member in scc:
                        if node_mask(member[0]) & bit:
                            element = ("node", member)
                            break
                    if element is None:
                        for src, action, dst in edges:
                            if edge_mask(action) & bit:
                                element = ("edge", src, action, dst)
                                break
                    elements.append(element)
            entry = scc[0]
            evidence.counterexample = Counterexample(
                prefix=prefix_to(entry),
                loop=build_loop(entry, scc, elements),
            )
            return Result(Verdict.VIOLATED, evidence)
        missing = [fairness[i] for i in range(num_sets)
                   if not coverage & (1 << i)]
        src, action, dst = edges[0]
        evidence.ignored_unfair_components.append(IgnoredComponent(
            states=sorted(member[0] for member in scc),
            missing_fairness=missing,
            sample_loop=build_loop(scc[0], scc, [("edge", src, action, dst)]),
        ))

    if truncated:
        return Result(Verdict.PENDING, evidence)
    return Result(Verdict.SATISFIED, evidence)


@dataclass
class ReplayReport:
    ok: bool
    checks: list[tuple[str, bool]]

    def format(self) -> str:
        return "\n".join(
            f"  [{'ok' if passed else 'FAIL'}] {name}"
            for name, passed in self.checks
        )


def replay(model: Model, counterexample: Counterexample, fairness_sets=()) -> ReplayReport:
    """Re-execute a counterexample against the model and confirm it is a
    genuine non-fulfilling lasso under the given fairness sets."""
    fairness = [frozenset(group) for group in fairness_sets]
    checks: list[tuple[str, bool]] = []

    def record(name, passed):
        checks.append((name, bool(passed)))
        return passed

    ok = True
    state = model.initial
    pending = pending_after(False, model.labels.get(state, frozenset()))
    for index, step in enumerate(counterexample.steps):
        legal = any(
            t.action == step.action and t.dst == step.state
            for t in model.outgoing(state)
        )
        ok &= record(f"step {index}: --{step.action}--> {step.state} is a real transition", legal)
        if not legal:
            return ReplayReport(False, checks)
        state = step.state
        pending = pending_after(pending, model.labels.get(state, frozenset()))
        ok &= record(f"step {index}: pending flag is {step.pending}",
                     pending == step.pending)

    loop = counterexample.loop
    ok &= record("loop is non-empty", len(loop) > 0)
    if not loop:
        return ReplayReport(False, checks)

    if counterexample.prefix:
        entry_state = counterexample.prefix[-1].state
        entry_pending = counterexample.prefix[-1].pending
    else:
        entry_state = model.initial
        entry_pending = pending_after(False, model.labels.get(model.initial, frozenset()))
    ok &= record("loop closes back to its entry",
                 state == entry_state and pending == entry_pending)

    loop_states = [entry_state] + [step.state for step in loop]
    loop_pendings = [entry_pending] + [step.pending for step in loop]
    ok &= record("request stays pending on the whole loop",
                 all(loop_pendings))
    ok &= record("no response state occurs on the loop",
                 all(RESP not in model.labels.get(s, frozenset()) for s in loop_states))

    for i, group in enumerate(fairness):
        taken = any(step.action in group for step in loop)
        disabled_somewhere = any(
            not (model.actions_enabled(s) & group) for s in loop_states
        )
        ok &= record(
            f"fairness set {sorted(group)}: taken on loop or disabled somewhere",
            taken or disabled_somewhere,
        )
    return ReplayReport(ok, checks)
