"""有限状态模型检查器。

对每个已接受请求对应的协议实例:
- 可能完成(may-complete): 存在一条到达终态的路径;
- 公平必完成(must-complete): 所有公平路径都到达终态;
- 阻塞(blocked): 不存在到达终态的路径;
- 未决(pending): 状态空间未搜索完(达到 max_states 上限)。

公平性采用弱公平: 一条无限执行不公平,当且仅当某个公平组的迁移
从某时刻起持续可用却从未被采用。公平性只作用于模型声明的
fair_groups,客户端动作(如 cancel)不在其列。

反例以套索(lasso)轨迹给出: 前缀 + 环,可用 replay() 在模型上
逐步重放验证。
"""

from __future__ import annotations

import enum
from collections import deque
from dataclasses import dataclass

from model import ProtocolModel


class Verdict(enum.Enum):
    MUST_COMPLETE = "must_complete"
    MAY_COMPLETE = "may_complete"
    BLOCKED = "blocked"
    PENDING = "pending"


@dataclass(frozen=True)
class Step:
    source: str
    label: str
    target: str


@dataclass(frozen=True)
class Trace:
    """可重放轨迹; loop_start 非空时为套索,环可无限重复。"""

    steps: tuple[Step, ...] = ()
    loop_start: int | None = None

    @property
    def is_lasso(self) -> bool:
        return self.loop_start is not None


@dataclass
class AnalysisResult:
    model: str
    initial: str
    verdict: Verdict
    explored_fully: bool
    reachable_states: frozenset
    terminals_reached: frozenset
    blocking_states: dict  # state -> "dead" | "livelock"
    counterexample: Trace | None


@dataclass
class ReplayResult:
    ok: bool
    error: str | None = None
    final_state: str | None = None
    is_terminal: bool = False
    loop_closed: bool | None = None
    loop_fair: bool | None = None


def analyze(model: ProtocolModel, initial: str | None = None,
            max_states: int | None = None) -> AnalysisResult:
    """分析从 initial 出发的完成性。max_states 截断时判定为 PENDING。"""
    initial = initial if initial is not None else model.initial
    seen, raw_adj, complete = _explore(model, initial, max_states)
    adjacency = {s: [t for t in raw_adj.get(s, []) if t.target in seen]
                 for s in seen}

    terminals_here = {s for s in seen if model.is_terminal(s)}
    pre_terminal = _backward_reachable(adjacency, seen, terminals_here)
    dead = seen - pre_terminal
    livelock_sccs = _fair_livelock_sccs(model, seen, adjacency)

    blocking: dict[str, str] = {}
    for s in sorted(dead):
        blocking[s] = "dead"
    for scc in livelock_sccs:
        for s in sorted(scc):
            blocking.setdefault(s, "livelock")

    if not complete:
        verdict = Verdict.PENDING
    elif initial in dead:
        verdict = Verdict.BLOCKED
    elif dead or livelock_sccs:
        verdict = Verdict.MAY_COMPLETE
    else:
        verdict = Verdict.MUST_COMPLETE

    counterexample = None
    if complete and verdict in (Verdict.BLOCKED, Verdict.MAY_COMPLETE):
        counterexample = _build_counterexample(
            model, initial, adjacency, dead, livelock_sccs)

    return AnalysisResult(
        model=model.name,
        initial=initial,
        verdict=verdict,
        explored_fully=complete,
        reachable_states=frozenset(seen),
        terminals_reached=frozenset(terminals_here),
        blocking_states=blocking,
        counterexample=counterexample,
    )


def replay(model: ProtocolModel, trace: Trace,
           initial: str | None = None) -> ReplayResult:
    """在模型上逐步重放轨迹,验证每步迁移真实存在;套索额外校验闭环与公平性。"""
    initial = initial if initial is not None else model.initial
    cur = initial
    for i, step in enumerate(trace.steps):
        if step.source != cur:
            return ReplayResult(
                ok=False,
                error=f"第 {i} 步源状态 {step.source!r} 与当前状态 {cur!r} 不符")
        if not any(t.label == step.label and t.target == step.target
                   for t in model.transitions_from(cur)):
            return ReplayResult(
                ok=False,
                error=f"第 {i} 步 {step.source!r} --{step.label}--> "
                      f"{step.target!r} 在模型中不存在")
        cur = step.target

    result = ReplayResult(ok=True, final_state=cur,
                          is_terminal=model.is_terminal(cur))
    if trace.loop_start is not None:
        loop_head = initial
        for step in trace.steps[: trace.loop_start]:
            loop_head = step.target
        result.loop_closed = loop_head == cur
        if result.loop_closed:
            loop_states = set()
            taken_groups = set()
            state = loop_head
            for step in trace.steps[trace.loop_start:]:
                loop_states.add(state)
                trans = next(
                    t for t in model.transitions_from(state)
                    if t.label == step.label and t.target == step.target)
                taken_groups.add(trans.group)
                state = step.target
            enabled_fair = set()
            for s in loop_states:
                for t in model.transitions_from(s):
                    if t.group in model.fair_groups:
                        enabled_fair.add(t.group)
            result.loop_fair = enabled_fair <= taken_groups
    return result


def _explore(model, initial, max_states):
    seen = {initial}
    adjacency = {}
    frontier = deque([initial])
    complete = True
    while frontier:
        state = frontier.popleft()
        trans = model.transitions_from(state)
        adjacency[state] = trans
        for t in trans:
            if t.target not in seen:
                if max_states is not None and len(seen) >= max_states:
                    complete = False
                    continue
                seen.add(t.target)
                frontier.append(t.target)
    return seen, adjacency, complete


def _backward_reachable(adjacency, states, targets):
    reverse = {s: [] for s in states}
    for s, trans in adjacency.items():
        for t in trans:
            reverse[t.target].append(s)
    seen = set(targets)
    queue = deque(targets)
    while queue:
        state = queue.popleft()
        for prev in reverse[state]:
            if prev not in seen:
                seen.add(prev)
                queue.append(prev)
    return seen


def _tarjan(states, adjacency):
    index_of: dict[str, int] = {}
    lowlink: dict[str, int] = {}
    on_stack: set[str] = set()
    stack: list[str] = []
    sccs: list[set[str]] = []
    counter = 0
    for root in states:
        if root in index_of:
            continue
        index_of[root] = lowlink[root] = counter
        counter += 1
        stack.append(root)
        on_stack.add(root)
        work = [(root, iter(adjacency.get(root, [])))]
        while work:
            node, it = work[-1]
            descended = False
            for t in it:
                w = t.target
                if w not in index_of:
                    index_of[w] = lowlink[w] = counter
                    counter += 1
                    stack.append(w)
                    on_stack.add(w)
                    work.append((w, iter(adjacency.get(w, []))))
                    descended = True
                    break
                elif w in on_stack:
                    lowlink[node] = min(lowlink[node], index_of[w])
            if descended:
                continue
            work.pop()
            if work:
                parent = work[-1][0]
                lowlink[parent] = min(lowlink[parent], lowlink[node])
            if lowlink[node] == index_of[node]:
                scc = set()
                while True:
                    w = stack.pop()
                    on_stack.discard(w)
                    scc.add(w)
                    if w == node:
                        break
                sccs.append(scc)
    return sccs


def _enabled_fair_groups(model, adjacency, states):
    groups = set()
    for s in states:
        for t in adjacency.get(s, []):
            if t.group in model.fair_groups:
                groups.add(t.group)
    return groups


def _fair_livelock_sccs(model, states, adjacency):
    """非终态强连通分量,且内部存在覆盖所有已启用公平组的环。"""
    result = []
    for scc in _tarjan(states, adjacency):
        if any(model.is_terminal(s) for s in scc):
            continue
        internal = [t for s in scc for t in adjacency.get(s, [])
                    if t.target in scc]
        has_cycle = len(scc) > 1 or any(t.source == t.target for t in internal)
        if not has_cycle:
            continue
        internal_groups = {t.group for t in internal}
        enabled = _enabled_fair_groups(model, adjacency, scc)
        if enabled <= internal_groups:
            result.append(scc)
    return result


def _shortest_path(adjacency, start, goal, within=None):
    if start == goal:
        return []
    prev = {start: None}
    queue = deque([start])
    while queue:
        state = queue.popleft()
        for t in adjacency.get(state, []):
            if within is not None and t.target not in within:
                continue
            if t.target in prev:
                continue
            prev[t.target] = (state, Step(t.source, t.label, t.target))
            if t.target == goal:
                steps = []
                cur = goal
                while prev[cur] is not None:
                    parent, step = prev[cur]
                    steps.append(step)
                    cur = parent
                steps.reverse()
                return steps
            queue.append(t.target)
    return None


def _path_to_set(adjacency, start, targets):
    if start in targets:
        return []
    prev = {start: None}
    queue = deque([start])
    while queue:
        state = queue.popleft()
        for t in adjacency.get(state, []):
            if t.target in prev:
                continue
            prev[t.target] = (state, Step(t.source, t.label, t.target))
            if t.target in targets:
                steps = []
                cur = t.target
                while prev[cur] is not None:
                    parent, step = prev[cur]
                    steps.append(step)
                    cur = parent
                steps.reverse()
                return steps
            queue.append(t.target)
    return None


def _build_counterexample(model, initial, adjacency, dead, livelock_sccs):
    # 1) 有限反例: 到达死锁状态(非终态且无出边)
    dead_ends = sorted(s for s in dead if not adjacency.get(s))
    if dead_ends:
        return Trace(tuple(_shortest_path(adjacency, initial, dead_ends[0])))
    # 2) 死区套索: 无法到达终态的环
    if dead:
        for scc in _tarjan(set(dead), adjacency):
            internal = [t for s in scc for t in adjacency.get(s, [])
                        if t.target in scc]
            if len(scc) > 1 or any(t.source == t.target for t in internal):
                return _lasso_to_cycle(adjacency, initial, scc, set())
    # 3) 公平活锁套索: 环覆盖所有已启用公平组
    if livelock_sccs:
        scc = livelock_sccs[0]
        required = _enabled_fair_groups(model, adjacency, scc)
        return _lasso_to_cycle(adjacency, initial, scc, required)
    return None


def _lasso_to_cycle(adjacency, initial, scc, required_groups):
    internal = [t for s in scc for t in adjacency.get(s, [])
                if t.target in scc]
    chosen = []
    for group in sorted(required_groups):
        chosen.append(next(t for t in internal if t.group == group))
    if not chosen:
        chosen = [internal[0]]
    start = chosen[0].source
    prefix = _path_to_set(adjacency, initial, {start})
    loop = []
    cur = start
    for t in chosen:
        loop.extend(_shortest_path(adjacency, cur, t.source, within=scc))
        loop.append(Step(t.source, t.label, t.target))
        cur = t.target
    loop.extend(_shortest_path(adjacency, cur, start, within=scc))
    return Trace(tuple(prefix + loop), loop_start=len(prefix))
