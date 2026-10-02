"""显式状态模型检查器。

- 安全性：BFS 全交错搜索，首个违例即最短反例；完整探索则给出闭包证书；
  预算（状态数）不足时结论保持 PENDING，绝不声称无错误。
- 公平进展：在完整可达图上做 Tarjan SCC 分解，找出无放行、有人持续等待
  且满足逐动作弱公平的坏循环，输出可重放套索（stem + loop），
  而非只查有限前缀。
- 对称归约：参与者不可区分，按规范化代表元去重；小规模下可与全交错
  参考结果互相核验。
- 所有反例均可通过 replay 重放校验。
"""

from __future__ import annotations

import hashlib
from collections import deque
from dataclasses import dataclass

from barrier_model import (
    Model, State, PARTICIPANT_ACTIONS, RELEASE, WAITING,
    canon, enabled_actions, fmt_action, step, transitions, violations,
)

SAFE = "SAFE"
VIOLATION = "VIOLATION"
PENDING = "PENDING"
LIVE = "LIVE"
LASSO = "LASSO"


class ReplayError(Exception):
    pass


def all_action_instances(model: Model):
    return [(k, p) for k in PARTICIPANT_ACTIONS for p in range(model.n)] + [(RELEASE,)]


def replay(model: Model, trace, start: State | None = None) -> State:
    """从初始状态（或给定状态）重放动作序列；动作不可用时抛 ReplayError。"""
    s = model.initial_state() if start is None else start
    for a in trace:
        for a2, s2 in transitions(model, s):
            if a2 == a:
                s = s2
                break
        else:
            raise ReplayError(f"action {fmt_action(a)} not enabled during replay")
    return s


def replay_violation(model: Model, trace):
    """重放反例轨迹，返回 (末状态, 最后一步违反的性质列表)。"""
    s = model.initial_state()
    bad = []
    for a in trace:
        for a2, s2 in transitions(model, s):
            if a2 == a:
                bad = violations(model, s, a, s2)
                s = s2
                break
        else:
            raise ReplayError(f"action {fmt_action(a)} not enabled during replay")
    return s, bad


def _trace_to(parent, s):
    acts = []
    while parent[s] is not None:
        prev, a = parent[s]
        acts.append(a)
        s = prev
    acts.reverse()
    return acts


@dataclass
class SafetyResult:
    status: str
    states: int
    transitions: int
    trace: list | None = None
    violated: list | None = None
    certificate: str | None = None
    explored: frozenset | None = None


def check_safety(model: Model, max_states: int = 200_000, reduced: bool = False) -> SafetyResult:
    """BFS 搜索 P1-P4 违例。BFS 保证首个反例为最短；队列清空且无违例
    才判 SAFE 并给出闭包证书；超出 max_states 预算则判 PENDING。"""
    init = model.initial_state()
    key = canon if reduced else (lambda s: s)
    seen = {key(init)}
    parent = {init: None}
    queue = deque([init])
    ntrans = 0
    while queue:
        s = queue.popleft()
        for a, s2 in transitions(model, s):
            ntrans += 1
            bad = violations(model, s, a, s2)
            if bad:
                return SafetyResult(VIOLATION, len(seen), ntrans,
                                    trace=_trace_to(parent, s) + [a],
                                    violated=bad,
                                    explored=frozenset(seen))
            k = key(s2)
            if k not in seen:
                seen.add(k)
                parent[s2] = (s, a)
                if len(seen) > max_states:
                    return SafetyResult(PENDING, len(seen), ntrans,
                                        explored=frozenset(seen))
                queue.append(s2)
    digest = hashlib.sha256()
    for rep in sorted(repr(k) for k in seen):
        digest.update(rep.encode())
    cert = f"closure sha256:{digest.hexdigest()} states={len(seen)} transitions={ntrans}"
    return SafetyResult(SAFE, len(seen), ntrans, certificate=cert,
                        explored=frozenset(seen))


def _tarjan_scc(nodes, adj):
    index_of, low = {}, {}
    stack, on_stack, sccs = [], set(), []
    counter = 0
    for root in nodes:
        if root in index_of:
            continue
        index_of[root] = low[root] = counter
        counter += 1
        stack.append(root)
        on_stack.add(root)
        todo = [(root, iter(adj.get(root, ())))]
        while todo:
            node, it = todo[-1]
            nxt = next(it, None)
            if nxt is not None:
                if nxt not in index_of:
                    index_of[nxt] = low[nxt] = counter
                    counter += 1
                    stack.append(nxt)
                    on_stack.add(nxt)
                    todo.append((nxt, iter(adj.get(nxt, ()))))
                elif nxt in on_stack:
                    low[node] = min(low[node], index_of[nxt])
            else:
                todo.pop()
                if todo:
                    parent = todo[-1][0]
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


def _bfs_path(adj, allowed, src, dst):
    """在 allowed 状态集内求 src->dst 的最短动作路径。"""
    if src == dst:
        return []
    prev = {src: None}
    queue = deque([src])
    while queue:
        u = queue.popleft()
        for a, v in adj.get(u, ()):
            if v not in allowed or v in prev:
                continue
            prev[v] = (u, a)
            if v == dst:
                acts = []
                w = v
                while prev[w] is not None:
                    pw, pa = prev[w]
                    acts.append(pa)
                    w = pw
                acts.reverse()
                return acts
            queue.append(v)
    return None


def _loop_states(model: Model, start: State, loop):
    states = [start]
    s = start
    for a in loop:
        s = step(model, s, a)
        states.append(s)
    return states


def _unfair_instances(model: Model, states, loop, instances):
    return [ai for ai in instances
            if all(ai in enabled_actions(model, u) for u in states)
            and ai not in loop]


def _fair_loop(model: Model, scc_set, adj, start, instances, max_iters=64):
    """在 SCC 内贪心构造弱公平循环：从简单循环出发，持续把
    “全程可用却从未执行”的动作以绕道方式插入，直到公平。"""
    first = adj[start][0]
    back = _bfs_path(adj, scc_set, first[1], start)
    if back is None:
        return None
    loop = [first[0]] + back
    for _ in range(max_iters):
        states = _loop_states(model, start, loop)
        unfair = _unfair_instances(model, states, loop, instances)
        if not unfair:
            return loop
        done = False
        for ai in unfair:
            for idx, u in enumerate(states[:-1]):
                if ai not in enabled_actions(model, u):
                    continue
                v = step(model, u, ai)
                if v not in scc_set:
                    continue
                back = _bfs_path(adj, scc_set, v, u)
                if back is None:
                    continue
                loop = loop[:idx] + [ai] + back + loop[idx:]
                done = True
                break
            if done:
                break
        if not done:
            return None
    return None


def _edge_covering_walk(adj, scc_set, internal_edges, start):
    """覆盖 SCC 全部内部边的闭合游走（公平的兜底见证）。"""
    loop = []
    current = start
    for (u, a, v) in internal_edges:
        path = _bfs_path(adj, scc_set, current, u)
        if path is None:
            return None
        loop += path
        loop.append(a)
        current = v
    back = _bfs_path(adj, scc_set, current, start)
    if back is None:
        return None
    return loop + back


@dataclass
class LivenessResult:
    status: str                 # LIVE | LASSO | PENDING
    states: int = 0
    stem: list | None = None    # 到达循环起点的有限前缀
    loop: list | None = None    # 可无限重复的公平坏循环


def check_liveness(model: Model, max_states: int = 200_000) -> LivenessResult:
    """公平进展检查：若存在可达的、无放行、有人等待且弱公平的循环，
    输出可重放套索；图未探索完（预算不足）则 PENDING。"""
    init = model.initial_state()
    parent = {init: None}
    depth = {init: 0}
    adj = {}
    edges = []
    states = {init}
    queue = deque([init])
    while queue:
        u = queue.popleft()
        for a, v in transitions(model, u):
            edges.append((u, a, v))
            adj.setdefault(u, []).append((a, v))
            if v not in states:
                states.add(v)
                parent[v] = (u, a)
                depth[v] = depth[u] + 1
                if len(states) > max_states:
                    return LivenessResult(PENDING, states=len(states))
                queue.append(v)
    nodes = list(states)
    sadj = {u: [v for _, v in adj.get(u, ())] for u in nodes}
    instances = all_action_instances(model)
    for scc in _tarjan_scc(nodes, sadj):
        scc_set = set(scc)
        internal = [(u, a, v) for (u, a, v) in edges if u in scc_set and v in scc_set]
        if not (len(scc) > 1 or any(u == v for (u, _, v) in internal)):
            continue                                   # 无循环
        if any(a[0] == RELEASE for (_, a, _) in internal):
            continue                                   # 循环内有放行，非反例
        waiting_states = [u for u in scc if any(st == WAITING for st in u.status)]
        if not waiting_states:
            continue                                   # 无人等待，非反例
        if all(u.rnd >= model.max_round for u in scc):
            continue                                   # 轮次上限造成的假象
        fair = all(
            not all(ai in enabled_actions(model, u) for u in scc)
            or any(a == ai for (_, a, _) in internal)
            for ai in instances
        )
        if not fair:
            continue                                   # 循环本身不满足弱公平
        start = min(waiting_states, key=lambda u: depth[u])
        stem = _trace_to(parent, start)
        loop = _fair_loop(model, scc_set, adj, start, instances)
        if loop is None:
            loop = _edge_covering_walk(adj, scc_set, internal, start)
        if loop is None:
            continue
        return LivenessResult(LASSO, states=len(states), stem=stem, loop=loop)
    return LivenessResult(LIVE, states=len(states))


def verify_lasso(model: Model, stem, loop) -> bool:
    """独立重放校验套索：stem 可达循环起点；loop 回到起点、无放行、
    有人等待、且满足逐动作弱公平。任何一项不满足都抛 ReplayError。"""
    base = replay(model, stem)
    states = [base]
    s = base
    for a in loop:
        if a[0] == RELEASE:
            raise ReplayError("lasso loop contains a release")
        s = replay(model, [a], start=s)
        states.append(s)
    if s != base:
        raise ReplayError("lasso loop does not return to its start state")
    if not any(any(st == WAITING for st in u.status) for u in states):
        raise ReplayError("lasso loop has no waiting participant")
    unfair = _unfair_instances(model, states, loop, all_action_instances(model))
    if unfair:
        raise ReplayError(f"lasso loop is not weakly fair: {fmt_action(unfair[0])}")
    return True
