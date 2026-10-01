"""“请求后最终响应”活性检查器。

性质：G(request -> F response)，即任何被置位的 request 命题最终都应被
response 命题满足。反例形状为“lasso”：一条从初始状态出发的有限前缀，
接一个**可达的、不履行循环**——循环中存在 request 置位的状态，且整个
循环上没有任何 response 置位的状态。

公平性只按显式动作集合定义（FairnessSet），支持弱公平与强公平：
- 弱公平：若某动作集合在循环的每个状态上都持续可执行，则循环中必须
  至少执行一次该集合中的动作；
- 强公平：若某动作集合在循环的某个状态上可执行（无限次经过），则循环
  中必须至少执行一次该集合中的动作。

算法：BFS 探索可达子图后，先剔除所有 response 置位状态，再对剩余
子图做 Tarjan SCC 分解；任何含 request 置位状态的非平凡 SCC 即对应一
个不履行循环候选，再按公平性约束筛选并缝合出具体闭合回路。

判定规则：
- 找到可达公平不履行循环 -> VIOLATED（反例可重放）；
- 状态空间搜索完毕且未发现 -> SATISFIED；
- 搜索被截断（max_states / max_depth）且未发现 -> PENDING（未决）。
  有限截断内未见到响应本身绝不构成违反。
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from enum import Enum
from typing import Optional

from .model import Model, Transition


class Verdict(str, Enum):
    SATISFIED = "satisfied"
    VIOLATED = "violated"
    PENDING = "pending"


@dataclass(frozen=True)
class FairnessSet:
    """按显式动作集合定义的公平性约束。"""

    name: str
    actions: frozenset
    kind: str = "weak"  # "weak" | "strong"

    def __init__(self, name, actions, kind="weak"):
        if kind not in ("weak", "strong"):
            raise ValueError(f"未知公平性类型: {kind!r}")
        object.__setattr__(self, "name", name)
        object.__setattr__(self, "actions", frozenset(actions))
        object.__setattr__(self, "kind", kind)


@dataclass(frozen=True)
class FairnessEvidence:
    """反例/重放中针对单个公平集合的判定证据。"""

    set_name: str
    kind: str
    detail: str


@dataclass(frozen=True)
class Counterexample:
    """可重放的 lasso 形反例：prefix 接入 cycle，cycle 首尾闭合。"""

    prefix: tuple
    cycle: tuple
    fairness: tuple  # tuple[FairnessEvidence, ...]

    def render(self) -> str:
        lines = ["prefix:"]
        if self.prefix:
            lines.extend(f"  {t}" for t in self.prefix)
        else:
            lines.append("  <空>")
        lines.append("cycle（无限重复）:")
        lines.extend(f"  {t}" for t in self.cycle)
        if self.fairness:
            lines.append("公平性证据:")
            lines.extend(f"  [{e.kind}] {e.set_name}: {e.detail}" for e in self.fairness)
        return "\n".join(lines)


@dataclass(frozen=True)
class CheckResult:
    verdict: Verdict
    complete: bool
    explored_states: int
    explored_transitions: int
    counterexample: Optional[Counterexample]
    fairness_sets: tuple  # 回显公平假设，假设变化必须体现在证据中
    notes: tuple


def _explore(model: Model, max_states: int, max_depth: int):
    """BFS 探索可达子图；返回 (邻接表, 深度, 已探索数, 是否截断)。"""
    adj = {}
    depth = {model.initial: 0}
    queue = deque([model.initial])
    truncated = False
    explored = 0
    while queue:
        state = queue.popleft()
        if explored >= max_states:
            truncated = True
            break
        explored += 1
        outs = list(model.outgoing(state))
        adj[state] = outs
        if depth[state] >= max_depth:
            if outs:
                truncated = True
            continue
        for t in outs:
            if t.dst not in depth:
                depth[t.dst] = depth[state] + 1
                queue.append(t.dst)
    if queue:
        truncated = True
    return adj, explored, truncated


def _tarjan_scc(nodes, succ):
    """迭代式 Tarjan 强连通分量分解。"""
    index_of, low, on_stack, stack, sccs = {}, {}, set(), [], []
    counter = 0
    for root in nodes:
        if root in index_of:
            continue
        index_of[root] = low[root] = counter
        counter += 1
        stack.append(root)
        on_stack.add(root)
        work = [(root, iter(succ(root)))]
        while work:
            node, it = work[-1]
            descended = False
            for nxt in it:
                if nxt not in index_of:
                    index_of[nxt] = low[nxt] = counter
                    counter += 1
                    stack.append(nxt)
                    on_stack.add(nxt)
                    work.append((nxt, iter(succ(nxt))))
                    descended = True
                    break
                if nxt in on_stack:
                    low[node] = min(low[node], index_of[nxt])
            if descended:
                continue
            work.pop()
            if work:
                parent = work[-1][0]
                low[parent] = min(low[parent], low[node])
            if low[node] == index_of[node]:
                scc = []
                while True:
                    w = stack.pop()
                    on_stack.discard(w)
                    scc.append(w)
                    if w == node:
                        break
                sccs.append(scc)
    return sccs


def _bfs_path(adj, start, goal, allowed=None):
    """在（可选限制的）已探索子图中求 start 到 goal 的迁移序列。"""
    if start == goal:
        return []
    prev = {start: None}
    queue = deque([start])
    while queue:
        state = queue.popleft()
        for t in adj.get(state, []):
            if allowed is not None and t.dst not in allowed:
                continue
            if t.dst in prev:
                continue
            prev[t.dst] = (state, t)
            if t.dst == goal:
                path = []
                node = goal
                while prev[node] is not None:
                    pstate, pt = prev[node]
                    path.append(pt)
                    node = pstate
                path.reverse()
                return path
            queue.append(t.dst)
    raise LookupError(f"{start!r} 到 {goal!r} 不可达")


def _stitch_cycle(adj, scc_set, start, witnesses, internal_edges):
    """把公平性见证（状态或边）缝合成 SCC 内的闭合回路。"""
    cycle = []
    current = start
    for kind, obj in witnesses:
        target = obj if kind == "state" else obj.src
        cycle.extend(_bfs_path(adj, current, target, allowed=scc_set))
        current = target
        if kind == "edge":
            cycle.append(obj)
            current = obj.dst
    cycle.extend(_bfs_path(adj, current, start, allowed=scc_set))
    if not cycle:
        # 单状态自循环 SCC：见证均为状态见证时回路为空，补一条内部边。
        edge = internal_edges[0]
        cycle = (
            _bfs_path(adj, start, edge.src, allowed=scc_set)
            + [edge]
            + _bfs_path(adj, edge.dst, start, allowed=scc_set)
        )
    return cycle


def _analyze_scc(model, scc, adj, fairness, request_prop, response_prop):
    """若 SCC 含可达公平不履行循环，返回 (cycle, 公平性证据)，否则 None。"""
    scc_set = set(scc)
    internal = [t for s in scc for t in adj.get(s, []) if t.dst in scc_set]
    has_self_loop = any(t.src == t.dst for t in internal)
    if len(scc) == 1 and not has_self_loop:
        return None  # 平凡 SCC 无循环
    if not any(request_prop in model.props(s) for s in scc):
        return None  # 循环中没有未决请求
    if any(response_prop in model.props(s) for s in scc):
        return None  # 循环内已有响应，性质成立

    witnesses, evidence = [], []
    for fs in fairness:
        internal_f = [t for t in internal if t.action in fs.actions]
        if internal_f:
            taken = internal_f[0]
            witnesses.append(("edge", taken))
            evidence.append(FairnessEvidence(
                fs.name, fs.kind,
                f"循环内执行了集合动作 '{taken.action}'（{taken}），满足{fs.kind}公平",
            ))
            continue
        enabling = [s for s in scc if model.enabled_actions(s) & fs.actions]
        if fs.kind == "weak":
            disabled = [s for s in scc if not (model.enabled_actions(s) & fs.actions)]
            if not disabled:
                return None  # 持续可执行却永不执行：循环不公平，不构成反例
            witnesses.append(("state", disabled[0]))
            evidence.append(FairnessEvidence(
                fs.name, fs.kind,
                f"状态 '{disabled[0]}' 上集合动作均不可执行，"
                f"集合未持续可执行，弱公平不要求执行",
            ))
        else:  # strong
            if enabling:
                return None  # 无限次可执行却永不执行：循环不公平
            evidence.append(FairnessEvidence(
                fs.name, fs.kind,
                "循环经过的所有状态上集合动作均不可执行，强公平不要求执行",
            ))

    pending = next(s for s in scc if request_prop in model.props(s))
    cycle = _stitch_cycle(adj, scc_set, pending, witnesses, internal)
    return cycle, evidence


def check(model: Model, request_prop="req", response_prop="resp",
          fairness=(), max_states=100_000, max_depth=100_000) -> CheckResult:
    """在给定有限状态模型上检查 G(request -> F response)。"""
    fairness = tuple(fairness)
    adj, explored, truncated = _explore(model, max_states, max_depth)
    n_transitions = sum(len(ts) for ts in adj.values())
    notes = []
    if truncated:
        notes.append(
            f"搜索被截断（max_states={max_states}, max_depth={max_depth}），"
            f"结论保持未决，除非已找到反例"
        )

    # 不履行循环必须整体避开 response 置位状态：先在子图中剔除它们，
    # 再分解 SCC。含 request 的非平凡 SCC 即“永不响应”的循环候选。
    resp_states = {s for s in model.states if response_prop in model.props(s)}
    nodes = set(adj)
    for ts in adj.values():
        for t in ts:
            nodes.add(t.dst)
    nodes -= resp_states
    sccs = _tarjan_scc(
        nodes,
        lambda s: [t.dst for t in adj.get(s, []) if t.dst in nodes],
    )

    for scc in sccs:
        found = _analyze_scc(model, scc, adj, fairness, request_prop, response_prop)
        if found is None:
            continue
        cycle, evidence = found
        prefix = _bfs_path(adj, model.initial, cycle[0].src)
        cex = Counterexample(tuple(prefix), tuple(cycle), tuple(evidence))
        return CheckResult(Verdict.VIOLATED, not truncated, explored,
                           n_transitions, cex, fairness, tuple(notes))

    verdict = Verdict.PENDING if truncated else Verdict.SATISFIED
    return CheckResult(verdict, not truncated, explored, n_transitions,
                       None, fairness, tuple(notes))
