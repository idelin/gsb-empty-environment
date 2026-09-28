"""有限状态多参与者模型的死锁核查器。

语义约定（全部为显式规则）：

1. 全局状态是按 ``Model.participants`` 顺序排列的局部状态元组。
2. 动作为多方同步动作：动作只在其声明的每个参与者的前置条件同时满足时
   全局可执行（enabled）。某个参与者局部满足前置只表示“局部提供/可邀约”，
   不代表全局可执行。
3. 状态分类：
   - DECLARED_TERMINAL：声明终态且无任何可执行动作（正常终态，不报死锁）；
   - TERMINAL_NOT_STABLE：声明终态仍存在可执行动作，属于声明违例；
   - DEADLOCK：非终态且可执行动作集合为空（异常死锁）；
   - LIVELOCK_CANDIDATE：非终态，仅有自环可执行动作（活锁候选，不是死锁）；
   - ACTIVE：其余存在非自环可执行动作的状态。
4. 阻塞动作：至少被一个参与者局部提供、但全局不可执行的动作。死锁诊断输出
   每个阻塞动作的提供方、未满足的参与者及其要求的局部状态。
5. 等待图：参与者 p 等待参与者 q，当存在阻塞动作 a，p 提供 a 且 q 是 a 尚未
   满足的前置参与者。诊断给出最小阻塞依赖环；若不存在环，则给出最短无提供者
   链（链尾参与者不提供任何阻塞动作）。
6. 搜索按初态分别 BFS，保证每个初态各自最短反例并汇总全局最短；多初态全部
   覆盖。展开预算耗尽且未发现违例时结论为 UNDECIDED，已发现的违例仍然成立
   （FAIL），绝不把尚未展开当作终止。
7. 独立完整转移图核验：在容量上限内从全部初态构造完整可达转移图，独立重算
   最短反例并重放预算搜索给出的每条反例。
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from enum import Enum


class Verdict(str, Enum):
    PASS = "PASS"
    FAIL_DEADLOCK = "FAIL_DEADLOCK"
    FAIL_TERMINAL_VIOLATION = "FAIL_TERMINAL_VIOLATION"
    FAIL_BOTH = "FAIL_BOTH"
    UNDECIDED = "UNDECIDED"


class StateKind(str, Enum):
    DECLARED_TERMINAL = "declared_terminal"
    TERMINAL_NOT_STABLE = "terminal_not_stable"
    DEADLOCK = "deadlock"
    LIVELOCK_CANDIDATE = "livelock_candidate"
    ACTIVE = "active"


_DEFECT_KINDS = {StateKind.DEADLOCK, StateKind.TERMINAL_NOT_STABLE}


@dataclass(frozen=True)
class Action:
    """同步动作：pre 必须覆盖 participants 中的每个参与者。"""

    name: str
    participants: tuple[str, ...]
    pre: dict[str, object]
    eff: dict[str, object]


@dataclass(frozen=True)
class Model:
    name: str
    participants: tuple[str, ...]
    local_states: dict[str, tuple[object, ...]]
    actions: tuple[Action, ...]
    initial_states: tuple[tuple[object, ...], ...]
    terminal_states: frozenset[tuple[object, ...]]


def make_model(name, participants, local_states, actions,
               initial_states, terminal_states):
    model = Model(
        name,
        tuple(participants),
        {p: tuple(s) for p, s in local_states.items()},
        tuple(actions),
        tuple(tuple(s) for s in initial_states),
        frozenset(tuple(s) for s in terminal_states),
    )
    validate_model(model)
    return model


def validate_model(model: Model) -> None:
    if not model.participants:
        raise ValueError("participants 不能为空")
    if len(set(model.participants)) != len(model.participants):
        raise ValueError("participants 存在重复")
    for p in model.participants:
        states = model.local_states.get(p)
        if not states:
            raise ValueError(f"参与者 {p} 缺少局部状态定义")
        if len(set(states)) != len(states):
            raise ValueError(f"参与者 {p} 的局部状态重复")

    width = len(model.participants)

    def check_global(state, label):
        if not isinstance(state, tuple) or len(state) != width:
            raise ValueError(f"{label} {state!r} 长度必须等于参与者数")
        for p, s in zip(model.participants, state):
            if s not in model.local_states[p]:
                raise ValueError(f"{label} {state!r} 中 {p} 的状态 {s!r} 未定义")

    if not model.initial_states:
        raise ValueError("至少需要一个初态")
    if len(set(model.initial_states)) != len(model.initial_states):
        raise ValueError("初态存在重复")
    for state in model.initial_states:
        check_global(state, "初态")
    for state in model.terminal_states:
        check_global(state, "终态")

    names: set[str] = set()
    for action in model.actions:
        if not action.name or action.name in names:
            raise ValueError(f"动作名非法或重复: {action.name!r}")
        names.add(action.name)
        if not action.participants:
            raise ValueError(f"动作 {action.name} 至少需要一个参与者")
        if len(set(action.participants)) != len(action.participants):
            raise ValueError(f"动作 {action.name} 的参与者重复")
        for p in action.participants:
            if p not in model.local_states:
                raise ValueError(f"动作 {action.name} 引用未知参与者 {p}")
        if set(action.pre) != set(action.participants):
            raise ValueError(f"动作 {action.name} 的前置必须覆盖且仅覆盖其参与者")
        if not action.eff or not set(action.eff) <= set(action.participants):
            raise ValueError(f"动作 {action.name} 的效果键必须为其参与者非空子集")
        for p in action.participants:
            if action.pre[p] not in model.local_states[p]:
                raise ValueError(f"动作 {action.name} 的前置状态未定义: {p}")
        for p, s in action.eff.items():
            if s not in model.local_states[p]:
                raise ValueError(f"动作 {action.name} 的效果状态未定义: {p}")


def participant_index(model: Model) -> dict[str, int]:
    return {p: i for i, p in enumerate(model.participants)}


def offered_by(model: Model, state, action: Action) -> tuple[str, ...]:
    """局部提供该动作的参与者（仅其自身前置满足）。"""
    idx = participant_index(model)
    return tuple(p for p in action.participants
                 if state[idx[p]] == action.pre[p])


def missing_preconditions(model: Model, state, action: Action) -> dict[str, object]:
    """未满足的前置：参与者 -> 它被要求处于的局部状态。"""
    idx = participant_index(model)
    return {p: action.pre[p] for p in action.participants
            if state[idx[p]] != action.pre[p]}


def is_enabled(model: Model, state, action: Action) -> bool:
    """全局可执行：动作全部参与者的前置同时满足。"""
    return not missing_preconditions(model, state, action)


def enabled_actions(model: Model, state) -> list[Action]:
    return [a for a in model.actions if is_enabled(model, state, a)]


def apply_action(model: Model, state, action: Action) -> tuple[object, ...]:
    if not is_enabled(model, state, action):
        raise ValueError(f"动作 {action.name} 在状态 {state!r} 不可执行")
    idx = participant_index(model)
    new_state = list(state)
    for p, s in action.eff.items():
        new_state[idx[p]] = s
    return tuple(new_state)


def classify_state(model: Model, state, enabled=None) -> StateKind:
    if enabled is None:
        enabled = enabled_actions(model, state)
    declared = state in model.terminal_states
    if not enabled:
        return StateKind.DECLARED_TERMINAL if declared else StateKind.DEADLOCK
    if declared:
        return StateKind.TERMINAL_NOT_STABLE
    self_only = all(apply_action(model, state, a) == state for a in enabled)
    return StateKind.LIVELOCK_CANDIDATE if self_only else StateKind.ACTIVE


@dataclass(frozen=True)
class BlockingAction:
    name: str
    offered_by: tuple[str, ...]
    missing: dict[str, object]


@dataclass(frozen=True)
class CycleEvidence:
    path: tuple[str, ...]
    edges: tuple[str, ...]


@dataclass(frozen=True)
class NoProviderEvidence:
    path: tuple[str, ...]
    edges: tuple[str, ...]
    sink: str


@dataclass(frozen=True)
class Diagnosis:
    blocking_actions: tuple[BlockingAction, ...]
    wait_graph: dict[str, tuple[str, ...]]
    cycle: CycleEvidence | None
    no_provider: NoProviderEvidence | None


def _canonical_cycle(path: tuple[str, ...]) -> tuple[str, ...]:
    return min(path[i:] + path[:i] for i in range(len(path)))


def diagnose_deadlock(model: Model, state) -> Diagnosis:
    """输出阻塞动作；给最小依赖环，无环时给最短无提供者链。"""
    blocking: list[BlockingAction] = []
    offered_sets: dict[str, set[str]] = {p: set() for p in model.participants}
    edge_actions: dict[tuple[str, str], set[str]] = {}

    for action in model.actions:
        providers = offered_by(model, state, action)
        missing = missing_preconditions(model, state, action)
        if providers and missing:
            blocking.append(BlockingAction(
                action.name, tuple(sorted(providers)), dict(sorted(missing.items()))))
            for p in providers:
                offered_sets[p].add(action.name)
            for p in providers:
                for q in missing:
                    edge_actions.setdefault((p, q), set()).add(action.name)

    wait_graph: dict[str, tuple[str, ...]] = {}
    for p in model.participants:
        targets = sorted({q for (pp, q) in edge_actions if pp == p})
        if targets:
            wait_graph[p] = tuple(targets)

    cycle = _find_minimum_cycle(model, wait_graph, edge_actions)
    no_provider = _find_no_provider_chain(wait_graph, offered_sets, edge_actions)
    return Diagnosis(tuple(blocking), wait_graph, cycle, no_provider)


def _find_minimum_cycle(model, wait_graph, edge_actions):
    best: tuple[str, ...] | None = None
    for start in model.participants:
        queue = deque([(start, (start,))])
        seen = {(start, (start,))}
        found = None
        while queue:
            node, path = queue.popleft()
            for nxt in wait_graph.get(node, ()):
                if nxt == start and len(path) >= 2:
                    found = path
                    break
                if nxt not in path:
                    candidate = path + (nxt,)
                    key = (nxt, candidate)
                    if key not in seen:
                        seen.add(key)
                        queue.append((nxt, candidate))
            if found:
                break
        if found is None:
            continue
        canonical = _canonical_cycle(found)
        if best is None or (len(canonical), canonical) < (len(best), best):
            best = canonical

    if best is None:
        return None
    edges = tuple(min(edge_actions[(best[i], best[(i + 1) % len(best)])])
                  for i in range(len(best)))
    return CycleEvidence(best, edges)


def _find_no_provider_chain(wait_graph, offered_sets, edge_actions):
    best: tuple[str, ...] | None = None
    for start in sorted(wait_graph):
        queue = deque([(start, (start,))])
        found = None
        while queue:
            node, path = queue.popleft()
            if node != start and not offered_sets[node]:
                found = path
                break
            for nxt in wait_graph.get(node, ()):
                if nxt not in path:
                    queue.append((nxt, path + (nxt,)))
        if found is not None and (best is None or
                                  (len(found), found) < (len(best), best)):
            best = found
    if best is None:
        return None
    edges = tuple(min(edge_actions[(best[i], best[i + 1])])
                  for i in range(len(best) - 1))
    return NoProviderEvidence(best, edges, best[-1])


@dataclass(frozen=True)
class TraceStep:
    action: str
    source: tuple[object, ...]
    target: tuple[object, ...]


@dataclass(frozen=True)
class ReplayResult:
    ok: bool
    final_state: tuple[object, ...] | None
    steps: tuple[TraceStep, ...]
    error: str | None


def action_by_name(model: Model, name: str) -> Action:
    for action in model.actions:
        if action.name == name:
            return action
    raise KeyError(name)


def replay_trace(model: Model, start, action_names) -> ReplayResult:
    """从 start 逐步重放动作名序列；任一步不可执行即失败。"""
    state = tuple(start)
    if len(state) != len(model.participants):
        return ReplayResult(False, None, (), "起始状态长度不符")
    steps: list[TraceStep] = []
    for name in action_names:
        try:
            action = action_by_name(model, name)
            nxt = apply_action(model, state, action)
        except (KeyError, ValueError) as exc:
            return ReplayResult(False, state, tuple(steps), f"动作 {name}: {exc}")
        steps.append(TraceStep(name, state, nxt))
        state = nxt
    return ReplayResult(True, state, tuple(steps), None)


@dataclass(frozen=True)
class Finding:
    kind: StateKind
    initial: tuple[object, ...]
    state: tuple[object, ...]
    depth: int
    trace: tuple[str, ...]
    diagnosis: Diagnosis | None = None


@dataclass(frozen=True)
class InitialResult:
    initial: tuple[object, ...]
    covered: bool
    exhausted: bool
    expanded: int
    closest_deadlock: Finding | None
    closest_terminal_violation: Finding | None
    livelock_candidates: tuple[Finding, ...]
    findings: tuple[Finding, ...]


@dataclass(frozen=True)
class FullCheckResult:
    status: str
    reachable_states: int
    edges: int
    closest_deadlock: Finding | None
    closest_terminal_violation: Finding | None
    livelock_candidates: tuple[Finding, ...]
    replay_ok: bool
    replay_errors: tuple[str, ...]
    agreement: bool | None
    note: str | None


@dataclass(frozen=True)
class Report:
    model_name: str
    verdict: Verdict
    budget_verdict: Verdict
    state_budget: int | None
    expanded: int
    budget_exhausted: bool
    initials_total: int
    initials_covered: int
    initial_results: tuple[InitialResult, ...]
    shortest_deadlock: Finding | None
    shortest_terminal_violation: Finding | None
    livelock_candidates: tuple[Finding, ...]
    full_check: FullCheckResult | None
    notes: tuple[str, ...]


def successors(model: Model, state):
    """返回 (动作, 目标状态)；同一目标的不同动作各自成边。"""
    out = []
    for action in enabled_actions(model, state):
        out.append((action, apply_action(model, state, action)))
    return out


def _reconstruct(tail, parent):
    actions: list[str] = []
    state = tail
    while parent[state][0] is not None:
        prev, action_name = parent[state]
        actions.append(action_name)
        state = prev
    return tuple(reversed(actions))


def _shortest_bfs(model: Model, start, budget_state, global_visited):
    """从单个初态 BFS。budget_state 是跨初态共享的剩余展开额度容器。"""
    parent: dict[tuple[object, ...], tuple[tuple[object, ...] | None, str | None]]
    parent = {start: (None, None)}
    queue = deque([start])
    exhausted = False
    if start not in global_visited:
        global_visited.add(start)

    closest: dict[StateKind, Finding] = {}
    livelocks: list[Finding] = []

    def record(kind, state):
        if kind in _DEFECT_KINDS and kind not in closest:
            trace = _reconstruct(state, parent)
            diagnosis = diagnose_deadlock(model, state) \
                if kind is StateKind.DEADLOCK else None
            closest[kind] = Finding(kind, start, state, len(trace), trace,
                                    diagnosis)
        elif kind is StateKind.LIVELOCK_CANDIDATE:
            trace = _reconstruct(state, parent)
            livelocks.append(Finding(kind, start, state, len(trace), trace))

    while queue:
        state = queue.popleft()
        if budget_state[0] is not None and budget_state[0] <= 0:
            exhausted = True
            break
        if budget_state[0] is not None:
            budget_state[0] -= 1

        enabled = enabled_actions(model, state)
        kind = classify_state(model, state, enabled)
        if kind in _DEFECT_KINDS or kind is StateKind.LIVELOCK_CANDIDATE:
            record(kind, state)
            if kind in _DEFECT_KINDS:
                continue
        for action, nxt in successors(model, state):
            if nxt not in parent:
                parent[nxt] = (state, action.name)
                queue.append(nxt)
                if nxt not in global_visited:
                    global_visited.add(nxt)

    covered = not exhausted
    deadlock = closest.get(StateKind.DEADLOCK)
    terminal_v = closest.get(StateKind.TERMINAL_NOT_STABLE)
    findings = tuple(f for f in (deadlock, terminal_v) if f is not None)
    return InitialResult(start, covered, exhausted, len(parent),
                         deadlock, terminal_v, tuple(livelocks), findings)


def _finding_identity(finding: Finding | None):
    if finding is None:
        return None
    return (finding.initial, finding.state, finding.depth)


def verify_full_graph(model: Model, budgeted_findings, *,
                      state_cap: int = 100_000, budget_complete: bool = True):
    """独立构造完整可达转移图并重算最短反例、重放所有缺陷轨迹。"""
    graph: dict[tuple[object, ...], list[tuple[str, tuple[object, ...]]]] = {}
    parent: dict[tuple[object, ...], tuple[tuple[object, ...] | None, str | None,
                                           tuple[object, ...]]] = {}
    queue = deque()
    depths: dict[tuple[object, ...], int] = {}
    for init in model.initial_states:
        if init not in parent:
            parent[init] = (None, None, init)
            depths[init] = 0
            queue.append(init)

    note = None
    while queue:
        state = queue.popleft()
        edges = successors(model, state)
        graph[state] = [(a.name, nxt) for a, nxt in edges]
        if len(graph) > state_cap:
            note = f"可达状态超过容量上限 {state_cap}，完整核验未完成"
            break
        for action, nxt in edges:
            if nxt not in parent:
                parent[nxt] = (state, action.name, state)
                depths[nxt] = depths[state] + 1
                queue.append(nxt)
    complete = note is None

    def trace_to(target):
        actions = []
        state = target
        while parent[state][0] is not None:
            prev, action_name, _ = parent[state]
            actions.append(action_name)
            state = prev
        return tuple(reversed(actions))

    def nearest(kind):
        best = None
        ordered = sorted(graph, key=lambda x: (depths[x], x)) if complete \
                else list(graph)
        for state in ordered:
            if kind == StateKind.DEADLOCK and state in model.terminal_states:
                continue
            enabled = enabled_actions(model, state)
            if classify_state(model, state, enabled) != kind:
                continue
            trace = trace_to(state)
            init = _origin(state, parent)
            diagnosis = diagnose_deadlock(model, state) \
                if kind is StateKind.DEADLOCK else None
            candidate = Finding(kind, init, state, depths[state], trace,
                                diagnosis)
            if best is None or candidate.depth < best.depth or \
                    (candidate.depth == best.depth and
                     (candidate.trace, candidate.state) <
                     (best.trace, best.state)):
                best = candidate
        return best

    livelocks = []
    if complete:
        for state in graph:
            if state in model.terminal_states:
                continue
            if classify_state(model, state) is StateKind.LIVELOCK_CANDIDATE:
                livelocks.append(Finding(StateKind.LIVELOCK_CANDIDATE,
                                         _origin(state, parent), state,
                                         depths[state], trace_to(state)))
    livelocks = tuple(sorted(livelocks, key=lambda f: (f.depth, f.state,
                                                       f.initial)))

    replay_errors: list[str] = []
    for finding in budgeted_findings:
        result = replay_trace(model, finding.initial, finding.trace)
        if not result.ok or result.final_state != finding.state:
            replay_errors.append(
                f"{finding.kind.value}: {finding.initial} -> "
                f"{finding.state} 重放失败/终点不符")
        elif classify_state(model, finding.state) != finding.kind:
            replay_errors.append(
                f"{finding.kind.value}: {finding.state} 分类复核不一致")

    closest_deadlock = nearest(StateKind.DEADLOCK) if complete else None
    closest_terminal_v = nearest(StateKind.TERMINAL_NOT_STABLE) if complete else None

    agreement: bool | None = None
    if complete and budget_complete:
        agreement = True
        budgeted_deadlocks = sorted(
            (f for f in budgeted_findings
             if f.kind is StateKind.DEADLOCK),
            key=lambda f: (f.depth, f.trace, f.state))
        budgeted_tvs = sorted(
            (f for f in budgeted_findings
             if f.kind is StateKind.TERMINAL_NOT_STABLE),
            key=lambda f: (f.depth, f.trace, f.state))
        bd = budgeted_deadlocks[0] if budgeted_deadlocks else None
        btv = budgeted_tvs[0] if budgeted_tvs else None
        if _finding_identity(bd) != _finding_identity(closest_deadlock):
            agreement = False
        if _finding_identity(btv) != _finding_identity(closest_terminal_v):
            agreement = False
    elif complete:
        agreement = None

    return FullCheckResult(
        status="complete" if complete else "incomplete",
        reachable_states=len(graph),
        edges=sum(len(v) for v in graph.values()),
        closest_deadlock=closest_deadlock,
        closest_terminal_violation=closest_terminal_v,
        livelock_candidates=livelocks,
        replay_ok=not replay_errors,
        replay_errors=tuple(replay_errors),
        agreement=agreement if complete else None,
        note=note,
    )


def _origin(state, parent):
    while parent[state][0] is not None:
        state = parent[state][0]
    return state


def analyze(model: Model, *, state_budget: int | None = None,
            full_check_cap: int = 100_000) -> Report:
    """主入口：部分状态展开搜索 + 独立完整转移图核验。"""
    budget_state = [state_budget]
    global_visited: set[tuple[object, ...]] = set()
    results = []
    for init in model.initial_states:
        result = _shortest_bfs(model, init, budget_state, global_visited)
        results.append(result)
        if result.exhausted:
            break

    covered_initials = sum(1 for r in results if r.covered)
    all_initials_covered = covered_initials == len(model.initial_states)
    budget_exhausted = not all_initials_covered

    def pick(attr):
        candidates = [getattr(r, attr) for r in results if getattr(r, attr)]
        if not candidates:
            return None
        return min(candidates, key=lambda f: (f.depth, f.trace, f.state,
                                              f.initial))

    shortest_deadlock = pick("closest_deadlock")
    shortest_terminal_v = pick("closest_terminal_violation")
    livelocks = tuple(sorted(
        (f for r in results for f in r.livelock_candidates),
        key=lambda f: (f.depth, f.state, f.initial)))

    defect_findings = tuple(
        f for r in results for f in r.findings)
    if shortest_deadlock and shortest_terminal_v:
        budget_verdict = Verdict.FAIL_BOTH
    elif shortest_deadlock:
        budget_verdict = Verdict.FAIL_DEADLOCK
    elif shortest_terminal_v:
        budget_verdict = Verdict.FAIL_TERMINAL_VIOLATION
    elif budget_exhausted:
        budget_verdict = Verdict.UNDECIDED
    else:
        budget_verdict = Verdict.PASS

    full_check = verify_full_graph(
        model, defect_findings, state_cap=full_check_cap,
        budget_complete=not budget_exhausted)

    if full_check.status == "complete":
        fd = full_check.closest_deadlock
        ft = full_check.closest_terminal_violation
        if fd and ft:
            verdict = Verdict.FAIL_BOTH
        elif fd:
            verdict = Verdict.FAIL_DEADLOCK
        elif ft:
            verdict = Verdict.FAIL_TERMINAL_VIOLATION
        else:
            verdict = Verdict.PASS
    else:
        verdict = budget_verdict

    notes = []
    if budget_exhausted:
        if budget_verdict is Verdict.UNDECIDED:
            notes.append("展开预算耗尽且尚未发现违例：未展开的状态不得视为"
                         "终止，预算阶段结论保持未决")
        else:
            notes.append("展开预算耗尽：尚未展开的状态不影响已发现反例，"
                         "但未覆盖区域不再下结论")
    if full_check.status == "incomplete":
        notes.append(full_check.note or "完整转移图核验未完成")
    if full_check.status == "complete" and full_check.agreement is False:
        notes.append("预算搜索与完整图核验的最短反例不一致")
    if full_check.status == "complete" and budget_exhausted:
        notes.append("预算搜索未完成（阶段结论 UNDECIDED）；"
                     "独立完整图已穷尽，综合结论以完整图为准")
    if full_check.replay_errors:
        notes.append("存在反例重放失败: " + "; ".join(full_check.replay_errors))

    return Report(
        model_name=model.name,
        verdict=verdict,
        budget_verdict=budget_verdict,
        state_budget=state_budget,
        expanded=len(global_visited),
        budget_exhausted=budget_exhausted,
        initials_total=len(model.initial_states),
        initials_covered=covered_initials,
        initial_results=tuple(results),
        shortest_deadlock=shortest_deadlock,
        shortest_terminal_violation=shortest_terminal_v,
        livelock_candidates=livelocks,
        full_check=full_check,
        notes=tuple(notes),
    )


def format_report(report: Report) -> str:
    lines = []
    lines.append(f"模型: {report.model_name}")
    lines.append(f"结论: {report.verdict.value}"
                 + (f"（预算阶段: {report.budget_verdict.value}）"
                    if report.budget_verdict != report.verdict else ""))
    lines.append(
        f"展开状态: {report.expanded}（预算: "
        f"{'无限' if report.state_budget is None else report.state_budget}），"
        f"初态覆盖: {report.initials_covered}/{report.initials_total}")

    def render_finding(label, finding):
        if finding is None:
            return
        lines.append(f"- {label}: 状态 {finding.state}，深度 {finding.depth}，"
                     f"初态 {finding.initial}")
        if finding.trace:
            lines.append(f"  最短证据: {' -> '.join(finding.trace)}")
        else:
            lines.append("  最短证据: <空序列，初态即违例>")
        if finding.diagnosis is not None:
            diagnosis = finding.diagnosis
            for blocked in diagnosis.blocking_actions:
                missing = ", ".join(
                    f"{p} 需={s!r}" for p, s in blocked.missing.items())
                lines.append(f"  阻塞动作 {blocked.name}: 提供方="
                             f"[{', '.join(blocked.offered_by)}]，未满足: {missing}")
            if diagnosis.wait_graph:
                wait_text = "; ".join(
                    f"{p} -> {', '.join(qs)}"
                    for p, qs in diagnosis.wait_graph.items())
                lines.append(f"  等待图: {wait_text}")
            if diagnosis.cycle is not None:
                cyc = diagnosis.cycle
                lines.append("  最小阻塞依赖环: "
                             + " -> ".join(cyc.path + (cyc.path[0],))
                             + " （边: " + ", ".join(cyc.edges) + "）")
            if diagnosis.no_provider is not None:
                npv = diagnosis.no_provider
                lines.append("  无提供者证据: "
                             + " -> ".join(npv.path)
                             + f"；终点 {npv.sink} 不提供任何阻塞动作"
                             + "（边: " + ", ".join(npv.edges) + "）")
            if not diagnosis.blocking_actions:
                lines.append("  无阻塞动作（模型中没有可邀约的动作）")

    render_finding("最短死锁(预算搜索)", report.shortest_deadlock)
    render_finding("最短声明终态违例(预算搜索)", report.shortest_terminal_violation)
    if check := report.full_check:
        if check.status == "complete" and report.shortest_deadlock is None \
                and check.closest_deadlock is not None:
            render_finding("最短死锁(完整图核验)", check.closest_deadlock)
        if check.status == "complete" and report.shortest_terminal_violation is None \
                and check.closest_terminal_violation is not None:
            render_finding("最短声明终态违例(完整图核验)",
                           check.closest_terminal_violation)
    for finding in report.livelock_candidates:
        lines.append(f"- 活锁候选(非死锁): 状态 {finding.state}，"
                     f"深度 {finding.depth}，仅可执行自环")

    check = report.full_check
    if check is not None:
        lines.append(
            f"独立完整图核验: {check.status}，可达状态 {check.reachable_states}，"
            f"边 {check.edges}，反例重放 {'成功' if check.replay_ok else '失败'}，"
            f"最短性一致 {('是' if check.agreement else '否') if check.agreement is not None else '不适用(预算未搜完)'}")
        if check.note:
            lines.append(f"  说明: {check.note}")
    for note in report.notes:
        lines.append(f"注: {note}")
    return "\n".join(lines)
