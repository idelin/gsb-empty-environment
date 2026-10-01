"""Explicit-state model checker for the barrier model.

* ``bfs_safety``     -- breadth-first exploration of the full interleaving;
                        shortest counterexamples; optional symmetry reduction.
* ``check_liveness`` -- SCC-based fair-cycle detection; produces a replayable
                        lasso (prefix + cycle), not just a finite prefix.
* ``replay`` / ``replay_lasso`` -- deterministic re-execution of witnesses.

Verdict discipline: PASS means the *whole* reachable state space was
explored (complete closure certificate, reported as state/transition
counts).  If the state budget is exhausted the verdict is UNKNOWN --
never PASS.
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from itertools import permutations
from typing import Optional

from barrier_model import BarrierModel, State, WAITING, RELEASED


# --------------------------------------------------------------------- #
# Symmetry reduction: canonical representative under participant renaming
# --------------------------------------------------------------------- #
def canonical_key(model: BarrierModel, s: State):
    n = model.n
    best = None
    for perm in permutations(range(n)):
        status = [None] * n
        wait = [None] * n
        arr = [None] * n
        rel = [None] * n
        for i in range(n):
            status[perm[i]] = s.status[i]
            wait[perm[i]] = s.wait_round[i]
            arr[perm[i]] = s.arr_msg[i]
            rel[perm[i]] = s.rel_msg[i]
        key = (s.round,
               tuple(sorted(perm[i] for i in s.members)),
               tuple(sorted(perm[i] for i in s.arrived)),
               tuple(sorted(perm[i] for i in s.pending_join)),
               tuple(status), tuple(wait), tuple(arr), tuple(rel))
        if best is None or key < best:
            best = key
    return best


# --------------------------------------------------------------------- #
# Safety
# --------------------------------------------------------------------- #
@dataclass
class SafetyResult:
    verdict: str                 # PASS | FAIL | UNKNOWN
    states: int
    transitions: int
    counterexample: Optional[list] = None   # shortest action sequence (BFS)
    violation: Optional[list] = None
    reduced: bool = False


def _reconstruct(parent, key):
    acts = []
    while parent[key] is not None:
        pk, a = parent[key]
        acts.append(a)
        key = pk
    acts.reverse()
    return acts


def bfs_safety(model: BarrierModel, max_states: int = 200_000,
               use_symmetry: bool = False) -> SafetyResult:
    key_of = (lambda s: canonical_key(model, s)) if use_symmetry else (lambda s: s)
    start = model.initial_state()
    sk = key_of(start)
    actual = {sk: start}
    parent = {sk: None}
    queue = deque([sk])
    expanded = set()
    ntrans = 0
    inv0 = model.invariants(start)
    if inv0:
        return SafetyResult("FAIL", 1, 0, [], inv0, use_symmetry)
    while queue:
        k = queue.popleft()
        if k in expanded:
            continue
        expanded.add(k)
        s = actual[k]
        for a in model.actions(s):
            s2 = model.step(s, a)
            ntrans += 1
            viol = model.transition_assertions(s, a, s2) or model.invariants(s2)
            k2 = key_of(s2)
            if k2 not in parent:
                parent[k2] = (k, a)
                actual[k2] = s2
                if len(parent) > max_states:
                    return SafetyResult("UNKNOWN", len(parent), ntrans,
                                        reduced=use_symmetry)
                queue.append(k2)
            if viol:
                path = _reconstruct(parent, k) + [a]
                return SafetyResult("FAIL", len(parent), ntrans, path, viol,
                                    use_symmetry)
    return SafetyResult("PASS", len(parent), ntrans, reduced=use_symmetry)


# --------------------------------------------------------------------- #
# Replay
# --------------------------------------------------------------------- #
@dataclass
class ReplayReport:
    ok: bool                     # every action was enabled and executed
    violation: list              # assertions/invariants hit on the last edge
    states_visited: int
    error: Optional[str] = None


def replay(model: BarrierModel, actions) -> ReplayReport:
    s = model.initial_state()
    visited = 1
    for a in actions:
        if a not in model.actions(s):
            return ReplayReport(False, [], visited,
                                f"action {a} not enabled in state {s}")
        s2 = model.step(s, a)
        viol = model.transition_assertions(s, a, s2) or model.invariants(s2)
        s = s2
        visited += 1
        if viol:
            return ReplayReport(True, viol, visited)
    return ReplayReport(True, [], visited)


# --------------------------------------------------------------------- #
# Liveness: fair acceptance cycles -> replayable lassos
# --------------------------------------------------------------------- #
@dataclass
class LivenessResult:
    verdict: str                 # PASS | FAIL | UNKNOWN
    states: int
    transitions: int
    victim: Optional[int] = None
    prefix: Optional[list] = None
    cycle: Optional[list] = None


def _in_group(action, group):
    kind, p = group
    if kind == "commit":
        return action[0] == "commit"
    return action[0] != "commit" and action[1] == p


def _tarjan_scc(adj):
    n = len(adj)
    idx = [-1] * n
    low = [0] * n
    onstack = [False] * n
    stack = []
    sccs = []
    counter = 0
    for root in range(n):
        if idx[root] != -1:
            continue
        work = [(root, 0)]
        while work:
            v, pi = work[-1]
            if pi == 0:
                idx[v] = low[v] = counter
                counter += 1
                stack.append(v)
                onstack[v] = True
            descended = False
            i = pi
            while i < len(adj[v]):
                w = adj[v][i][1]
                if idx[w] == -1:
                    work[-1] = (v, i + 1)
                    work.append((w, 0))
                    descended = True
                    break
                elif onstack[w]:
                    if idx[w] < low[v]:
                        low[v] = idx[w]
                i += 1
            if descended:
                continue
            work.pop()
            if work:
                pv = work[-1][0]
                if low[v] < low[pv]:
                    low[pv] = low[v]
            if low[v] == idx[v]:
                scc = []
                while True:
                    w = stack.pop()
                    onstack[w] = False
                    scc.append(w)
                    if w == v:
                        break
                sccs.append(scc)
    return sccs


def _bfs_path(succ, src, dst):
    if src == dst:
        return []
    prev = {src: None}
    dq = deque([src])
    while dq:
        u = dq.popleft()
        for a, v in succ.get(u, ()):
            if v not in prev:
                prev[v] = (u, a)
                if v == dst:
                    acts = []
                    x = v
                    while prev[x] is not None:
                        px, aa = prev[x]
                        acts.append(aa)
                        x = px
                    acts.reverse()
                    return acts
                dq.append(v)
    return None


def _bfs_to_group_edge(succ, src, group):
    """Path from src to an edge whose actor is `group`; returns (actions, dst)."""
    prev = {src: None}
    dq = deque([src])
    while dq:
        u = dq.popleft()
        for a, v in succ.get(u, ()):
            if _in_group(a, group):
                acts = []
                x = u
                while prev[x] is not None:
                    px, aa = prev[x]
                    acts.append(aa)
                    x = px
                acts.reverse()
                return acts + [a], v
            if v not in prev:
                prev[v] = (u, a)
                dq.append(v)
    return None


def check_liveness(model: BarrierModel, max_states: int = 200_000,
                   fair: bool = True) -> LivenessResult:
    """Property: no participant stays WAITING/RELEASED forever.

    Under weak fairness (default): every action group that is continuously
    enabled on a cycle must fire on that cycle.  A bad cycle witnesses an
    infinite fair execution in which some participant is never released.

    For each potential victim p the search restricts to the subgraph of
    states where p is stuck (WAITING/RELEASED) and looks for a fair
    non-trivial SCC there -- i.e. a reachable, repeatable cycle on which p
    is never released.
    """
    start = model.initial_state()
    index = {start: 0}
    states = [start]
    adj = [[]]
    parent = {0: None}
    queue = deque([0])
    ntrans = 0
    while queue:
        i = queue.popleft()
        s = states[i]
        for a in model.actions(s):
            s2 = model.step(s, a)
            ntrans += 1
            j = index.get(s2)
            if j is None:
                if len(states) >= max_states:
                    return LivenessResult("UNKNOWN", len(states), ntrans)
                j = len(states)
                index[s2] = j
                states.append(s2)
                adj.append([])
                parent[j] = (i, a)
                queue.append(j)
            adj[i].append((a, j))

    groups = [("p", p) for p in range(model.n)] + [("commit", None)]
    for victim in range(model.n):
        stuck = [i for i, st in enumerate(states)
                 if st.status[victim] in (WAITING, RELEASED)]
        if not stuck:
            continue
        stuck_set = set(stuck)
        sub_adj = [[] for _ in states]
        for u in stuck:
            for (a, v) in adj[u]:
                if v in stuck_set:
                    sub_adj[u].append((a, v))
        for scc in _tarjan_scc(sub_adj):
            scc_set = set(scc)
            internal = [(u, a, v) for u in scc for (a, v) in sub_adj[u]
                        if v in scc_set]
            if not internal:
                continue                  # trivial SCC without a self-loop
            disabled_state = {}
            has_firing_edge = set()
            for g in groups:
                for u in scc:
                    if not any(_in_group(a, g)
                               for a in model.actions(states[u])):
                        disabled_state[g] = u
                        break
                for (u, a, v) in internal:
                    if _in_group(a, g):
                        has_firing_edge.add(g)
                        break
            if fair and any(g not in disabled_state and g not in has_firing_edge
                            for g in groups):
                continue                  # unfair SCC: some group starved
            # Build a closed walk inside the SCC that is weakly fair: for
            # every group, either pass through a state where it is disabled,
            # or fire one of its transitions.
            succ = {}
            for (u, a, v) in internal:
                succ.setdefault(u, []).append((a, v))
            s0 = scc[0]
            cur = s0
            cycle = []
            for g in groups:
                if g in disabled_state:
                    seg = _bfs_path(succ, cur, disabled_state[g])
                    cycle += seg
                    cur = disabled_state[g]
                elif g in has_firing_edge:
                    seg, cur = _bfs_to_group_edge(succ, cur, g)
                    cycle += seg
                # else: group continuously enabled but never firing -- only
                # possible when fair=False; the cycle is unfair w.r.t. it,
                # which is exactly what an unfair scheduler may do.
            cycle += _bfs_path(succ, cur, s0)
            # Prefix: BFS tree path from the initial state to s0.
            prefix = []
            x = s0
            while parent[x] is not None:
                px, aa = parent[x]
                prefix.append(aa)
                x = px
            prefix.reverse()
            return LivenessResult("FAIL", len(states), ntrans,
                                  victim=victim, prefix=prefix, cycle=cycle)
    return LivenessResult("PASS", len(states), ntrans)


@dataclass
class LassoReplay:
    prefix_ok: bool
    cycle_ok: bool
    closed: bool                 # cycle returns to its entry state
    victim_stuck: bool           # victim WAITING/RELEASED on the whole cycle
    error: Optional[str] = None


def replay_lasso(model: BarrierModel, prefix, cycle, victim) -> LassoReplay:
    s = model.initial_state()
    for a in prefix:
        if a not in model.actions(s):
            return LassoReplay(False, False, False, False,
                               f"prefix action {a} not enabled")
        s = model.step(s, a)
    entry = s
    cyc_states = [entry]
    for a in cycle:
        if a not in model.actions(s):
            return LassoReplay(True, False, False, False,
                               f"cycle action {a} not enabled")
        s = model.step(s, a)
        cyc_states.append(s)
    closed = (s == entry)
    stuck = all(st.status[victim] in (WAITING, RELEASED) for st in cyc_states)
    return LassoReplay(True, True, closed, stuck)
