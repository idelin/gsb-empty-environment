# -*- coding: utf-8 -*-
"""有向多重图上的整数最大流：残余网络增量修复 + 原子快照历史。

核心约束
--------
* 顶点 ID、边 ID 均为字符串；平行边（同端点、不同 ID）完整保留，孤立点保留。
* 容量、供需均为整数。节点平衡 ``balances[v] > 0`` 表示供应，``< 0`` 表示
  需求，``= 0`` 为转运点。通过超级源 S* / 超级汇 T* 转化为最大流问题。
* 每次原子批处理（容量增减、边停用、节点供需变化）在**当前流的残余网络**上
  就地修复：容量缩小先尝试残余改道（reroute），不足部分反向撤回（cancel），
  再从残余网络继续增广。绝不全量清零重算，并统计各阶段访问的残余边数。
* 批处理先在克隆状态上执行；参数非法（或 strict 模式下供需不可满足）
  都丢弃克隆，原快照保持不变。默认模式下供需不可满足不是错误：
  提交最大可行流并在报告中给出缺口明细。
* 成功后追加只读快照，版本时间区间为左闭右开 ``[t_k, t_{k+1})``。
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from typing import Sequence

__all__ = [
    "FlowNetwork",
    "CutCertificate",
    "CrossingEdge",
    "UpdateReport",
    "Change",
    "Snapshot",
    "InfeasibleFlowError",
    "InvalidUpdateError",
]

_SUPER_S = "\x00S*"
_SUPER_T = "\x00T*"


class _Arc:
    """残余弧：一条边/终端弧对应一对正向、反向 _Arc。"""

    __slots__ = ("eid", "fwd", "u", "v", "mate")

    def __init__(self, eid: str, fwd: bool, u: str, v: str) -> None:
        self.eid = eid
        self.fwd = fwd
        self.u = u
        self.v = v
        self.mate: _Arc | None = None

    def __repr__(self) -> str:  # pragma: no cover - 调试用
        d = "fwd" if self.fwd else "rev"
        return f"_Arc({self.eid},{d},{self.u}->{self.v})"


class _Edge:
    __slots__ = ("eid", "u", "v", "cap", "active", "fwd_arc", "rev_arc")

    def __init__(self, eid: str, u: str, v: str, cap: int, active: bool,
                 fwd_arc: _Arc, rev_arc: _Arc) -> None:
        self.eid = eid
        self.u = u
        self.v = v
        self.cap = cap
        self.active = active
        self.fwd_arc = fwd_arc
        self.rev_arc = rev_arc


class _Term:
    """超级源/汇终端弧对：供应 S*->node，需求 node->T*。"""

    __slots__ = ("eid", "node", "sign", "cap", "fwd_arc", "rev_arc")

    def __init__(self, eid: str, node: str, sign: int, cap: int,
                 fwd_arc: _Arc, rev_arc: _Arc) -> None:
        self.eid = eid
        self.node = node
        self.sign = sign
        self.cap = cap
        self.fwd_arc = fwd_arc
        self.rev_arc = rev_arc


@dataclass(frozen=True)
class CrossingEdge:
    """割证书中的一条跨割弧。"""

    eid: str
    u: str
    v: str
    flow: int
    capacity: int
    kind: str  # "edge" | "supply" | "demand"


@dataclass(frozen=True)
class CutCertificate:
    """最小割证书：残余可达的源侧顶点（不含超级点）与跨割弧。"""

    source_side: tuple[str, ...]
    crossing: tuple[CrossingEdge, ...]
    capacity: int


@dataclass(frozen=True)
class Change:
    """单条变化原因。kind 取值：
    shrink / reroute / cancel / deactivate / grow /
    balance_down / balance_up / augment。
    """

    kind: str
    eid: str | None
    delta: int
    detail: str
    path: tuple[str, ...] | None = None


@dataclass(frozen=True)
class UpdateReport:
    version: int
    time: int | float
    interval: tuple[int | float, int | float | None]  # 左闭右开
    flow: dict[str, int]
    value: int
    total_supply: int
    total_demand: int
    satisfied: bool
    cut: CutCertificate
    changes: tuple[Change, ...]
    repair_visited_edges: int
    augment_visited_edges: int
    reason: str

    def explain(self) -> str:
        hi = "+∞" if self.interval[1] is None else self.interval[1]
        head = f"[{self.interval[0]}, {hi})"
        lines = [
            f"版本 v{self.version} @ t={self.time}，区间 {head}，流值 {self.value}",
            f"总供应 {self.total_supply}，总需求 {self.total_demand}，"
            f"供需满足: {self.satisfied}",
            f"修复访问残余边 {self.repair_visited_edges} 条，"
            f"增广访问残余边 {self.augment_visited_edges} 条",
        ]
        lines.extend(f"  - {c.detail}" for c in self.changes)
        return "\n".join(lines)


@dataclass(frozen=True)
class Snapshot:
    version: int
    time: int | float
    interval_end: int | float | None
    nodes: tuple[str, ...]
    edges: tuple[tuple[str, str, str, int, bool], ...]
    balances: tuple[tuple[str, int], ...]
    flow: tuple[tuple[str, int], ...]
    value: int
    cut: CutCertificate

    @property
    def interval(self) -> tuple[int | float, int | float | None]:
        return self.time, self.interval_end

    def flow_dict(self) -> dict[str, int]:
        return dict(self.flow)

    def balances_dict(self) -> dict[str, int]:
        return dict(self.balances)


class InvalidUpdateError(ValueError):
    """更新请求本身非法（ID 不存在、容量为负、复活已停用边等）。"""


class InfeasibleFlowError(ValueError):
    """strict 模式下供需不可满足：更新被拒绝、快照不变。"""

    def __init__(self, message: str, *, value: int, required: int,
                 deficits: dict[str, int], reachable: frozenset[str]) -> None:
        super().__init__(message)
        self.value = value
        self.required = required
        self.deficits = deficits
        self.reachable = reachable


def _is_int(x) -> bool:
    return isinstance(x, int) and not isinstance(x, bool)


class FlowNetwork:
    """有向多重图与当前最大流状态（残余网络持续维护）。"""

    def __init__(self, nodes: Sequence[str] = (),
                 edges: Sequence[tuple[str, str, str, int]] = (),
                 balances: dict[str, int] | None = None) -> None:
        self._nodes: list[str] = []
        self._node_set: set[str] = set()
        self._edges: dict[str, _Edge] = {}
        self._terms: dict[str, _Term] = {}
        self._adj: dict[str, list[_Arc]] = {}
        self._edge_flows: dict[str, int] = {}
        self._term_flows: dict[str, int] = {}
        for n in nodes:
            self._add_node(n)
        self._adj.setdefault(_SUPER_S, [])
        self._adj.setdefault(_SUPER_T, [])
        parsed: list[tuple[str, str, str, int]] = []
        for eid, u, v, cap in edges:
            if not isinstance(eid, str) or not eid:
                raise ValueError("边 ID 必须是非空字符串")
            if eid in self._edges:
                raise ValueError(f"重复边 ID: {eid}")
            if u not in self._node_set or v not in self._node_set:
                raise ValueError(f"边 {eid} 的端点未在节点集合中声明")
            if not _is_int(cap) or cap < 0:
                raise ValueError(f"边 {eid} 容量必须是非负整数")
            parsed.append((eid, u, v, cap))
        for eid, u, v, cap in parsed:
            self._make_edge(eid, u, v, cap, True)
        bal = dict(balances or {})
        for n, b in bal.items():
            if n not in self._node_set:
                raise ValueError(f"未知节点的供需: {n}")
            if not _is_int(b):
                raise ValueError(f"节点 {n} 的供需必须是整数")
            if b != 0:
                self._make_term(n, abs(b), 1 if b > 0 else -1)
        self.repair_visited = 0
        self.augment_visited = 0
        self._max_augment(frozenset())

    # ------------------------------------------------------------------ 构建
    def _add_node(self, n: str) -> None:
        if not isinstance(n, str) or not n:
            raise ValueError("节点 ID 必须是非空字符串")
        if n in (_SUPER_S, _SUPER_T):
            raise ValueError("保留的超级点 ID 不可使用")
        if n in self._node_set:
            raise ValueError(f"重复节点 ID: {n}")
        self._node_set.add(n)
        self._nodes.append(n)
        self._adj[n] = []

    def _make_arc_pair(self, eid: str, u: str, v: str) -> tuple[_Arc, _Arc]:
        fwd = _Arc(eid, True, u, v)
        rev = _Arc(eid, False, v, u)
        fwd.mate = rev
        rev.mate = fwd
        return fwd, rev

    def _make_edge(self, eid: str, u: str, v: str, cap: int,
                   active: bool) -> _Edge:
        fwd, rev = self._make_arc_pair(eid, u, v)
        edge = _Edge(eid, u, v, cap, active, fwd, rev)
        self._edges[eid] = edge
        self._edge_flows[eid] = 0
        self._adj[u].append(fwd)
        self._adj[v].append(rev)
        return edge

    def _make_term(self, node: str, cap: int, sign: int) -> _Term:
        if sign > 0:
            fwd, rev = self._make_arc_pair(f"term:supply:{node}",
                                           _SUPER_S, node)
        else:
            fwd, rev = self._make_arc_pair(f"term:demand:{node}",
                                           node, _SUPER_T)
        term = _Term(fwd.eid, node, sign, cap, fwd, rev)
        self._terms[node] = term
        self._term_flows[fwd.eid] = 0
        self._adj[fwd.u].append(fwd)
        self._adj[fwd.v].append(rev)
        return term

    def _drop_term(self, node: str) -> None:
        term = self._terms.pop(node)
        for arc in (term.fwd_arc, term.rev_arc):
            self._adj[arc.u].remove(arc)
        self._term_flows.pop(term.fwd_arc.eid, None)

    # ------------------------------------------------------------------ 残余
    @staticmethod
    def _term_key(eid: str) -> str:
        # _Term 以 node 为键，弧 eid 形如 term:(supply|demand):<node>
        return eid.split(":", 2)[2]

    def _holder(self, arc: _Arc):
        if arc.eid.startswith("term:"):
            return self._terms.get(self._term_key(arc.eid))
        return self._edges.get(arc.eid)

    def _res_cap(self, arc: _Arc) -> int:
        holder = self._holder(arc)
        if holder is None:
            return 0
        if isinstance(holder, _Edge) and not holder.active:
            return 0
        flow = self._term_flows[arc.eid] if arc.eid.startswith("term:") \
            else self._edge_flows[arc.eid]
        return holder.cap - flow if arc.fwd else flow

    # ------------------------------------------------------------------ 搜索
    def _bfs_path(self, source: str, targets: frozenset[str],
                  walls: frozenset[str] = frozenset(),
                  blocked_edges: frozenset[str] = frozenset()
                  ) -> tuple[list[_Arc] | None, int]:
        """BFS 找一条 source -> targets 中任一节点的严格正残余路径。

        walls 中的节点不可进入；blocked_edges 中边（含终端弧 eid）的所有
        残余弧不可使用。返回 (路径弧序列, 本次访问的残余弧条数)。
        """
        if source in walls or source in targets:
            return None, 0
        parent: dict[str, _Arc] = {source: None}  # type: ignore[assignment]
        queue = deque([source])
        visited_edges = 0
        while queue:
            cur = queue.popleft()
            for arc in self._adj[cur]:
                visited_edges += 1
                if arc.eid in blocked_edges:
                    continue
                nxt = arc.v
                if nxt in walls or nxt in parent:
                    continue
                if self._res_cap(arc) <= 0:
                    continue
                parent[nxt] = arc
                if nxt in targets:
                    path: list[_Arc] = []
                    p = nxt
                    while parent[p] is not None:
                        a = parent[p]
                        path.append(a)
                        p = a.u
                    path.reverse()
                    return path, visited_edges
                queue.append(nxt)
        return None, visited_edges

    def _path_nodes(self, path: Sequence[_Arc]) -> tuple[str, ...]:
        return tuple([path[0].u] + [a.v for a in path])

    @staticmethod
    def _bottleneck(path: Sequence[_Arc], cap_of) -> int:
        return min(cap_of(a) for a in path)

    # ------------------------------------------------------------ 增广/可行
    def _max_augment(self, blocked_edges: frozenset[str],
                     changes: list[Change] | None = None) -> frozenset[str]:
        """从超级源反复 BFS 增广到超级汇，返回残余可达的源侧顶点集合。"""
        targets = frozenset({_SUPER_T})
        while True:
            path, seen = self._bfs_path(_SUPER_S, targets,
                                        blocked_edges=blocked_edges)
            self.augment_visited += seen
            if path is None:
                break
            amount = self._bottleneck(path, self._res_cap)
            self._apply_path_flows(path, amount)
            if changes is not None:
                nodes = self._path_nodes(path)
                changes.append(Change(
                    "augment", None, amount,
                    f"残余网络继续增广 {amount}，路径 {nodes}",
                    nodes))
        return self._reachable_from_source()

    def _flows_for(self, eid: str) -> dict[str, int]:
        return self._term_flows if eid.startswith("term:") else self._edge_flows

    def _apply_path_flows(self, path: Sequence[_Arc], amount: int) -> None:
        for arc in path:
            flows = self._flows_for(arc.eid)
            if arc.fwd:
                flows[arc.eid] = flows.get(arc.eid, 0) + amount
            else:
                flows[arc.eid] = flows.get(arc.eid, 0) - amount

    def _reachable_from_source(self) -> frozenset[str]:
        seen_nodes = {_SUPER_S}
        queue = deque([_SUPER_S])
        while queue:
            cur = queue.popleft()
            for arc in self._adj[cur]:
                if self._res_cap(arc) > 0 and arc.v not in seen_nodes:
                    seen_nodes.add(arc.v)
                    queue.append(arc.v)
        return seen_nodes

    def _total_supply(self) -> int:
        return sum(t.cap for t in self._terms.values() if t.sign > 0)

    def _total_demand(self) -> int:
        return sum(t.cap for t in self._terms.values() if t.sign < 0)

    def _current_value(self) -> int:
        return sum(f for eid, f in self._term_flows.items()
                   if eid.startswith("term:supply:") and f > 0)

    def _satisfied(self) -> bool:
        value = self._current_value()
        return value == self._total_supply() == self._total_demand()

    def _deficits(self, reachable: frozenset[str]) -> dict[str, int]:
        """各供应节点未路由出去的供应量（终端容量 - 终端流）。"""
        deficits: dict[str, int] = {}
        for term in self._terms.values():
            if term.sign <= 0:
                continue
            flow = self._term_flows.get(term.fwd_arc.eid, 0)
            if flow < term.cap:
                deficits[term.node] = term.cap - flow
        return deficits

    # ------------------------------------------------------------ 增量修复
    def _repair_excess(self, eid: str, excess: int, kind: str,
                       changes: list[Change]) -> None:
        """收缩产生 excess 单位超容量流量：先改道、后撤回。

        kind == "edge"：
          reroute  超量点 u -> v（仅经过原始点，绕开正在收缩的边）；
          cancel   u -> S* 与 T* -> v 两条反向残余路径，各撤 1 单位总流。
        kind == "supply"（终端弧 S*->node 收缩）：
          reroute  S* -> ... -> node（仅原始点，终点为该供应点），
                   表示从别的供应点补偿；
          cancel   T* -> node，撤回该供应点支撑的 1 单位流。
        kind == "demand"（终端弧 node->T* 收缩）：
          reroute  node -> ... -> T*（仅原始点，起点为该需求点），
                   表示由别的需求点接收；
          cancel   node -> S*，撤回 1 单位送往该需求点的流。
        """
        term = self._terms.get(self._term_key(eid)) if kind != "edge" else None
        blocked = frozenset({eid})
        originals = frozenset(self._nodes)
        remaining = excess
        reroute_kind = "reroute"

        def reroute_once() -> int:
            if kind == "edge":
                edge = self._edges[eid]
                path, seen = self._bfs_path(
                    edge.u, frozenset({edge.v}),
                    walls=frozenset({_SUPER_S, _SUPER_T}),
                    blocked_edges=blocked)
            elif kind == "supply":
                path, seen = self._bfs_path(
                    _SUPER_S, frozenset({term.node}),
                    walls=frozenset({_SUPER_T}), blocked_edges=blocked)
            else:
                path, seen = self._bfs_path(
                    term.node, frozenset({_SUPER_T}),
                    walls=frozenset({_SUPER_S}), blocked_edges=blocked)
            self.repair_visited += seen
            if path is None:
                return 0
            nodes = self._path_nodes(path)
            if kind == "edge":
                if not all(n in originals for n in nodes):
                    return 0
            else:
                inner = nodes[1:-1]
                if not all(n in originals for n in inner):
                    return 0
            bn = min(self._bottleneck(path, self._res_cap), remaining)
            self._apply_path_flows(path, bn)
            changes.append(Change(
                reroute_kind, eid, bn,
                f"{eid} 收缩 {excess}：反向残余改道 {bn}，路径 {nodes}",
                nodes))
            return bn

        while remaining > 0:
            moved = reroute_once()
            if moved:
                remaining -= moved
                continue
            # 撤回：两条不经过超级点内部的反向残余路径
            if kind == "edge":
                edge = self._edges[eid]
                p1, s1 = self._bfs_path(edge.u, frozenset({_SUPER_S}),
                                        walls=frozenset({_SUPER_T}),
                                        blocked_edges=blocked)
                p2, s2 = self._bfs_path(_SUPER_T, frozenset({edge.v}),
                                        walls=frozenset({_SUPER_S}),
                                        blocked_edges=blocked)
            elif kind == "supply":
                p1 = None
                p2, s2 = self._bfs_path(_SUPER_T, frozenset({term.node}),
                                        walls=frozenset({_SUPER_S}),
                                        blocked_edges=blocked)
                s1 = 0
            else:
                p1, s1 = self._bfs_path(term.node, frozenset({_SUPER_S}),
                                        walls=frozenset({_SUPER_T}),
                                        blocked_edges=blocked)
                p2 = None
                s2 = 0
            self.repair_visited += s1 + s2
            if (kind != "supply" and p1 is None) or \
               (kind != "demand" and p2 is None):
                raise RuntimeError(
                    f"内部错误：{eid} 收缩 {excess} 后无法撤回残余流量")
            take = remaining
            if p1 is not None:
                take = min(take, self._bottleneck(p1, self._res_cap))
            if p2 is not None:
                take = min(take, self._bottleneck(p2, self._res_cap))
            if take <= 0:
                raise RuntimeError(f"内部错误：{eid} 撤回瓶颈为 0")
            if p1 is not None:
                self._apply_path_flows(p1, take)
            if p2 is not None:
                self._apply_path_flows(p2, take)
            if kind == "edge":
                label = f"{eid} 收缩：残余撤回 {take} 单位总流"
            elif kind == "supply":
                label = f"供应 {term.node} 缩减：撤回 {take} 单位"
            else:
                label = f"需求 {term.node} 缩减：撤回 {take} 单位"
            changes.append(Change("cancel", eid, take, label))
            remaining -= take

    # ------------------------------------------------------------ 事件应用
    def _set_edge_capacity(self, eid: str, new_cap: int,
                           changes: list[Change]) -> None:
        edge = self._edges[eid]
        old_cap = edge.cap
        flow = self._edge_flows[eid]
        if new_cap > old_cap:
            edge.cap = new_cap
            changes.append(Change(
                "grow", eid, new_cap - old_cap,
                f"边 {eid} 容量 {old_cap} -> {new_cap}，"
                f"正向残余增加 {new_cap - old_cap}（流 {flow} 保留）"))
            return
        if new_cap < old_cap:
            excess = flow - new_cap
            if excess > 0:
                self._repair_excess(eid, excess, "edge", changes)
            edge.cap = new_cap
            self._edge_flows[eid] = new_cap if excess > 0 else flow
            changes.append(Change(
                "shrink", eid, old_cap - new_cap,
                f"边 {eid} 容量 {old_cap} -> {new_cap}，"
                f"流调整为 {self._edge_flows[eid]}，无超容量"))

    def _deactivate_edge(self, eid: str,
                         changes: list[Change]) -> None:
        edge = self._edges[eid]
        flow = self._edge_flows[eid]
        if flow > 0:
            self._repair_excess(eid, flow, "edge", changes)
        edge.active = False
        self._edge_flows[eid] = 0
        changes.append(Change(
            "deactivate", eid, 0,
            f"边 {eid} 停用（名义容量 {edge.cap} 保留），"
            f"残余弧不可用，流置 0"))

    def _set_balance(self, node: str, new_bal: int,
                     changes: list[Change]) -> None:
        old = self._terms.get(node)
        old_cap = old.cap if old is not None else 0
        new_sign = 1 if new_bal > 0 else (-1 if new_bal < 0 else 0)
        if old is None and new_sign == 0:
            return
        if old is not None and (new_sign == 0 or new_sign != old.sign):
            # 过零或变号：先把旧终端弧流撤干净，再拆除
            flow = self._term_flows.get(old.fwd_arc.eid, 0)
            if flow > 0:
                kind = "supply" if old.sign > 0 else "demand"
                self._repair_excess(old.fwd_arc.eid, flow, kind, changes)
            self._term_flows[old.fwd_arc.eid] = 0
            self._drop_term(node)
            old = None
            old_cap = 0
        if new_sign == 0:
            changes.append(Change(
                "balance_down", f"term:{node}", 0,
                f"节点 {node} 供需归零，终端弧拆除"))
            return
        if old is None:
            term = self._make_term(node, abs(new_bal), new_sign)
            self._term_flows[term.fwd_arc.eid] = 0
            kind_cn = "供应" if new_sign > 0 else "需求"
            changes.append(Change(
                "balance_up", term.fwd_arc.eid, abs(new_bal),
                f"节点 {node} 新增{kind_cn} {abs(new_bal)}，终端弧已建立"))
            return
        new_cap = abs(new_bal)
        if new_cap < old_cap:
            flow = self._term_flows[old.fwd_arc.eid]
            excess = flow - new_cap
            if excess > 0:
                kind = "supply" if old.sign > 0 else "demand"
                self._repair_excess(old.fwd_arc.eid, excess, kind, changes)
            old.cap = new_cap
            self._term_flows[old.fwd_arc.eid] = min(flow, new_cap)
            kind_cn = "供应" if old.sign > 0 else "需求"
            changes.append(Change(
                "balance_down", old.fwd_arc.eid, old_cap - new_cap,
                f"节点 {node} {kind_cn} {old_cap} -> {new_cap}"))
        elif new_cap > old_cap:
            old.cap = new_cap
            kind_cn = "供应" if old.sign > 0 else "需求"
            changes.append(Change(
                "balance_up", old.fwd_arc.eid, new_cap - old_cap,
                f"节点 {node} {kind_cn} {old_cap} -> {new_cap}，"
                f"终端正向残余增加"))

    # ------------------------------------------------------------ 割证书
    def _cut_certificate(self, reachable: frozenset[str]) -> CutCertificate:
        source_side = tuple(n for n in self._nodes if n in reachable)
        crossing: list[CrossingEdge] = []
        total = 0
        for eid in self._edges:
            edge = self._edges[eid]
            if not edge.active:
                continue
            if edge.u in reachable and edge.v not in reachable:
                f = self._edge_flows[eid]
                crossing.append(CrossingEdge(
                    eid, edge.u, edge.v, f, edge.cap, "edge"))
                total += edge.cap
        for node, term in self._terms.items():
            f = self._term_flows.get(term.fwd_arc.eid, 0)
            if term.sign > 0 and _SUPER_S in reachable and node not in reachable:
                crossing.append(CrossingEdge(
                    term.fwd_arc.eid, _SUPER_S, node, f, term.cap, "supply"))
                total += term.cap
            elif term.sign < 0 and node in reachable and _SUPER_T not in reachable:
                crossing.append(CrossingEdge(
                    term.fwd_arc.eid, node, _SUPER_T, f, term.cap, "demand"))
                total += term.cap
        return CutCertificate(source_side, tuple(crossing), total)

    # ------------------------------------------------------------ 克隆/快照
    def _clone(self) -> "FlowNetwork":
        clone = FlowNetwork.__new__(FlowNetwork)
        clone._nodes = list(self._nodes)
        clone._node_set = set(self._node_set)
        clone._edges = {}
        clone._terms = {}
        clone._adj = {n: [] for n in clone._nodes}
        clone._adj[_SUPER_S] = []
        clone._adj[_SUPER_T] = []
        for eid, edge in self._edges.items():
            fwd, rev = clone._make_arc_pair(eid, edge.u, edge.v)
            clone._edges[eid] = _Edge(eid, edge.u, edge.v, edge.cap,
                                      edge.active, fwd, rev)
            clone._adj[edge.u].append(fwd)
            clone._adj[edge.v].append(rev)
        for node, term in self._terms.items():
            fwd, rev = clone._make_arc_pair(term.fwd_arc.eid,
                                            term.fwd_arc.u, term.fwd_arc.v)
            clone._terms[node] = _Term(term.fwd_arc.eid, node, term.sign,
                                       term.cap, fwd, rev)
            clone._adj[fwd.u].append(fwd)
            clone._adj[fwd.v].append(rev)
        clone._edge_flows = dict(self._edge_flows)
        clone._term_flows = dict(self._term_flows)
        clone.repair_visited = 0
        clone.augment_visited = 0
        return clone

    def _snapshot(self, version: int, time, interval_end) -> Snapshot:
        return Snapshot(
            version=version,
            time=time,
            interval_end=interval_end,
            nodes=tuple(self._nodes),
            edges=tuple((e.eid, e.u, e.v, e.cap, e.active)
                        for e in self._edges.values()),
            balances=tuple(sorted(
                ((t.node, t.cap if t.sign > 0 else -t.cap)
                 for t in self._terms.values()))),
            flow=tuple(sorted(self._edge_flows.items())),
            value=self._current_value(),
            cut=self._cut_certificate(self._reachable_from_source()),
        )

    # ---------------------------------------------------------------- 访问
    @property
    def nodes(self) -> tuple[str, ...]:
        return tuple(self._nodes)

    @property
    def edges(self) -> tuple[tuple[str, str, str, int, bool], ...]:
        return tuple((e.eid, e.u, e.v, e.cap, e.active)
                     for e in self._edges.values())

    @property
    def balances(self) -> dict[str, int]:
        return {t.node: (t.cap if t.sign > 0 else -t.cap)
                for t in self._terms.values()}

    def flow(self) -> dict[str, int]:
        return dict(self._edge_flows)

    def flow_value(self) -> int:
        return self._current_value()

    def cut(self) -> CutCertificate:
        return self._cut_certificate(self._reachable_from_source())

    @classmethod
    def _from_data(cls, nodes, edges5, balances_map, flows,
                   ) -> "FlowNetwork":
        net = cls.__new__(cls)
        net._nodes = []
        net._node_set = set()
        net._edges = {}
        net._terms = {}
        net._adj = {}
        for n in nodes:
            net._add_node(n)
        net._adj[_SUPER_S] = []
        net._adj[_SUPER_T] = []
        net._edge_flows = {}
        net._term_flows = {}
        for eid, u, v, cap, active in edges5:
            net._make_edge(eid, u, v, cap, active)
            net._edge_flows[eid] = int(flows.get(eid, 0))
        for node, b in balances_map.items():
            if b == 0:
                continue
            term = net._make_term(node, abs(b), 1 if b > 0 else -1)
            net._term_flows[term.fwd_arc.eid] = 0
        net.repair_visited = 0
        net.augment_visited = 0
        for eid, f in net._edge_flows.items():
            edge = net._edges[eid]
            assert 0 <= f and (f <= edge.cap if edge.active else f == 0)
        # 由边流推导终端弧流量，保证残余网络守恒一致
        net_out = {n: 0 for n in net._nodes}
        for eid, f in net._edge_flows.items():
            edge = net._edges[eid]
            if edge.active:
                net_out[edge.u] += f
                net_out[edge.v] -= f
        for node, term in net._terms.items():
            tf = net_out[node] if term.sign > 0 else -net_out[node]
            assert 0 <= tf <= term.cap, (node, tf, term.cap)
            net._term_flows[term.fwd_arc.eid] = tf
        net._max_augment(frozenset())
        return net

    @classmethod
    def from_snapshot(cls, snap: Snapshot) -> "FlowNetwork":
        """从旧快照重建一份可独立检查的网络（流一并恢复）。"""
        return cls._from_data(snap.nodes, snap.edges, snap.balances_dict(),
                              snap.flow_dict())


class FlowHistory:
    """维护当前网络与左闭右开的只读快照序列。"""

    def __init__(self, net: FlowNetwork, initial_time: int | float = 0):
        self._net = net
        self._snapshots: list[Snapshot] = []
        self._times: list[int | float] = []
        self._append(net, initial_time)

    def _append(self, net: FlowNetwork, time) -> None:
        if self._times and not time > self._times[-1]:
            raise ValueError("版本时间必须严格递增")
        if self._snapshots:
            prev = self._snapshots[-1]
            self._snapshots[-1] = Snapshot(
                prev.version, prev.time, time, prev.nodes, prev.edges,
                prev.balances, prev.flow, prev.value, prev.cut)
        version = len(self._snapshots)
        snap = net._snapshot(version, time, None)
        self._snapshots.append(snap)
        self._times.append(time)

    @property
    def network(self) -> FlowNetwork:
        return self._net

    @property
    def snapshots(self) -> tuple[Snapshot, ...]:
        return tuple(self._snapshots)

    @property
    def latest(self) -> Snapshot:
        return self._snapshots[-1]

    def version(self, k: int) -> Snapshot:
        return self._snapshots[k]

    def at(self, time) -> Snapshot:
        """读取时间点 ``time`` 生效的快照：版本区间左闭右开 ``[t_k,t_{k+1})``。"""
        import bisect
        idx = bisect.bisect_right(self._times, time) - 1
        if idx < 0:
            raise KeyError(f"时间 {time} 早于首个版本 {self._times[0]}")
        return self._snapshots[idx]

    # ----------------------------------------------------------- 原子更新
    def apply_update(self, time: int | float, *,
                     capacity: dict[str, int] | None = None,
                     deactivate: Sequence[str] = (),
                     balances: dict[str, int] | None = None,
                     strict: bool = False
                     ) -> UpdateReport:
        """原子批量更新。

        strict=False（默认）：供需不可满足时提交最大可行流，报告缺口；
        strict=True：结果不满足全部供需则抛 InfeasibleFlowError，
        原快照保持不变。
        """
        capacity = dict(capacity or {})
        deactivate = tuple(deactivate)
        balances = dict(balances or {})
        self._validate(time, capacity, deactivate, balances)
        clone = self._net._clone()
        changes: list[Change] = []
        for node, b in balances.items():
            clone._set_balance(node, b, changes)
        for eid, new_cap in capacity.items():
            clone._set_edge_capacity(eid, new_cap, changes)
        for eid in deactivate:
            clone._deactivate_edge(eid, changes)
        reachable = clone._max_augment(frozenset(), changes)
        value = clone._current_value()
        cut = clone._cut_certificate(reachable)
        if cut.capacity != value:
            raise AssertionError(
                f"内部错误：流值 {value} 与割容量 {cut.capacity} 不一致")
        if strict and not clone._satisfied():
            deficits = clone._deficits(reachable)
            raise InfeasibleFlowError(
                f"供需不可满足：已路由 {value}，总供应 "
                f"{clone._total_supply()}，缺口明细 {deficits}",
                value=value, required=clone._total_supply(),
                deficits=deficits,
                reachable=frozenset(n for n in reachable
                                    if not n.startswith("\x00")))
        self._net = clone
        self._append(clone, time)
        snap = self.latest
        reason = self._summarize(changes)
        return UpdateReport(
            version=snap.version,
            time=time,
            interval=snap.interval,
            flow=snap.flow_dict(),
            value=value,
            total_supply=clone._total_supply(),
            total_demand=clone._total_demand(),
            satisfied=clone._satisfied(),
            cut=cut,
            changes=tuple(changes),
            repair_visited_edges=clone.repair_visited,
            augment_visited_edges=clone.augment_visited,
            reason=reason,
        )

    @staticmethod
    def _summarize(changes: list[Change]) -> str:
        kinds = {c.kind for c in changes}
        if not kinds:
            return "空更新：残余网络上无需调整，流与割不变"
        if kinds & {"shrink", "deactivate", "balance_down",
                    "reroute", "cancel"}:
            if "reroute" in kinds:
                return "缩减事件：残余改道为主，必要时反向撤回，随后残余增广"
            return "缩减事件：反向残余撤回超容量流量，随后残余增广"
        return "扩张事件：既有流保留，在残余网络上继续增广"

    def _validate(self, time, capacity, deactivate, balances) -> None:
        if not self._times or time <= self._times[-1]:
            raise InvalidUpdateError(
                f"时间 {time} 必须严格晚于当前版本时间 {self._times[-1]}")
        net = self._net
        for eid, cap in capacity.items():
            if not _is_int(cap) or cap < 0:
                raise InvalidUpdateError(f"边 {eid} 新容量必须是非负整数")
            edge = net._edges.get(eid)
            if edge is None:
                raise InvalidUpdateError(f"未知边 ID: {eid}")
            if not edge.active:
                raise InvalidUpdateError(
                    f"边 {eid} 已停用，不允许在停用状态下改容量（停用不可复活）")
        dead = set()
        for eid in deactivate:
            edge = net._edges.get(eid)
            if edge is None:
                raise InvalidUpdateError(f"未知边 ID: {eid}")
            if not edge.active or eid in dead:
                raise InvalidUpdateError(f"边 {eid} 已停用或重复停用")
            dead.add(eid)
        if dead & set(capacity):
            raise InvalidUpdateError("同一条边不能同时停用又修改容量")
        for node, b in balances.items():
            if not _is_int(b):
                raise InvalidUpdateError(f"节点 {node} 供需必须是整数")
            if node not in net._node_set:
                raise InvalidUpdateError(f"未知节点 ID: {node}")
