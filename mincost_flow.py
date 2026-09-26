"""最小费用整数流：容量上下界、负费用、精确有理数、不可行证据。

本模块不依赖任何外部求解器，仅使用 Python 标准库：
  * 内部全部使用 fractions.Fraction 做精确运算（输入为合成整数或有理数）；
  * 容量下界先强制占用流量并改写节点平衡；
  * 用「超源/超汇 + Dinic 最大流」判定残量可行流是否存在，
    不可行时以最小割（可达集）作为不可行证据；
  * 用 Bellman-Ford 在残量网络中寻找负费用环，再用消圈法（cycle canceling）
    把可行流推进到最优；所有容量有界，负环沿残量容量有界地增广；
  * 可行、最优、预算判定三件事分别给出结论（含「未决」状态）。

整数性：当全部下界、上界、平衡量均为整数时，残量网络容量为整数，
Dinic 与消圈增广量均为整数，故得到的流一定是整数流（整流定理）。
"""

from __future__ import annotations

import sys
from dataclasses import dataclass, field
from enum import Enum
from fractions import Fraction
from typing import Dict, List, NamedTuple, Optional, Sequence, Tuple

__all__ = [
    "Edge",
    "EdgeFlow",
    "Feasibility",
    "Optimality",
    "BudgetVerdict",
    "InfeasibleEvidence",
    "SolveResult",
    "MinCostFlow",
    "Z",
]

# ---------------------------------------------------------------------------
# 类型别名
# ---------------------------------------------------------------------------

Number = int | Fraction | str  # 输入可给整数或有理数（字符串会经 Fraction 解析）
Node = str  # 节点名


def Z(value: Number) -> Fraction:
    """把输入统一规范为 Fraction。"""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):  # bool 是 int 的子类，这里明确拒绝以免误用
        raise TypeError("布尔值不是合法的流参数")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, str):
        return Fraction(value)
    raise TypeError(f"不支持的参数类型: {type(value)!r}")


class Feasibility(str, Enum):
    FEASIBLE = "FEASIBLE"
    INFEASIBLE = "INFEASIBLE"


class Optimality(str, Enum):
    OPTIMAL = "OPTIMAL"  # 已证明最优（残量网络无负环）
    NOT_PROVEN = "NOT_PROVEN"  # 找到可行流，但未能在预算的迭代内证明最优
    NOT_APPLICABLE = "N/A"  # 问题本身不可行


class BudgetVerdict(str, Enum):
    WITHIN_BUDGET = "WITHIN_BUDGET"  # 已证最优且最优费用 <= 预算
    OVER_BUDGET = "OVER_BUDGET"  # 已证最优且最优费用 > 预算（预算不可行）
    UNDECIDED = "UNDECIDED"  # 未给预算，或未证最优、无法下结论


# ---------------------------------------------------------------------------
# 输入 / 输出数据结构
# ---------------------------------------------------------------------------


class Edge(NamedTuple):
    tail: Node
    head: Node
    lower: Fraction
    upper: Fraction
    cost: Fraction
    label: str


class EdgeFlow(NamedTuple):
    label: str
    tail: Node
    head: Node
    lower: Fraction
    upper: Fraction
    cost: Fraction
    flow: Fraction


@dataclass
class InfeasibleEvidence:
    """不可行证据。

    reason 取值：
      "supply_demand_mismatch" : 总供给 != 总需求（或总平衡不为 0）
      "bounds_violation"       : 某条边下界 > 上界
      "cut"                    : 容量瓶颈，可达集 S 给出违反的割约束
    """

    reason: str
    detail: str
    reachable: Optional[List[Node]] = None
    blocked_forward: List[Tuple[str, Node, Node, Fraction, Fraction]] = field(default_factory=list)
    residual_backward: List[Tuple[str, Node, Node, Fraction]] = field(default_factory=list)
    cut_capacity: Optional[Fraction] = None
    required: Optional[Fraction] = None
    total_supply: Optional[Fraction] = None
    total_demand: Optional[Fraction] = None

    def render(self) -> str:
        lines = [f"不可行证据（{self.reason}）：{self.detail}"]
        if self.total_supply is not None:
            lines.append(f"  总供给 = {self.total_supply}，总需求 = {self.total_demand}")
        if self.reachable is not None:
            lines.append(f"  最小割可达集 S = {sorted(self.reachable)}")
            if self.required is not None:
                lines.append(
                    f"  穿割残量容量合计 = {self.cut_capacity} < 必须注入的量 = {self.required}"
                )
            for label, u, v, cap, remain in self.blocked_forward:
                lines.append(
                    f"    正向穿出: {u} -> {v} [{label}] 容量(上界-下界)={cap}"
                    + ("（已到上界）" if remain == 0 else f"，当前残量 {remain}"))
            for label, u, v, cap in self.residual_backward:
                lines.append(f"    反向穿入（已到下界，无法回退）: {u} -> {v} [{label}]")
        return "\n".join(lines)


@dataclass
class SolveResult:
    feasibility: Feasibility
    optimality: Optimality
    budget: BudgetVerdict
    flows: List[EdgeFlow]
    cost: Optional[Fraction]
    evidence: Optional[InfeasibleEvidence]
    iterations: int
    budget_limit: Optional[Fraction]
    all_integral: bool
    negative_cycles_canceled: int

    def is_integral(self) -> bool:
        return self.all_integral

    def render(self) -> str:
        lines: List[str] = []
        lines.append(f"可行性   : {self.feasibility.value}")
        if self.feasibility is Feasibility.INFEASIBLE:
            lines.append(f"最优性   : {self.optimality.value}")
            lines.append(f"预算判定 : {self.budget.value}")
            if self.evidence is not None:
                lines.append(self.evidence.render())
            return "\n".join(lines)

        lines.append(f"最优性   : {self.optimality.value}"
                     + (f"（消圈 {self.negative_cycles_canceled} 个，迭代上限内未完成）"
                        if self.optimality is Optimality.NOT_PROVEN else ""))
        if self.budget_limit is None:
            lines.append(f"预算判定 : {self.budget.value}（未提供预算）")
        else:
            lines.append(f"预算判定 : {self.budget.value}（预算 {self.budget_limit}，最优费用 {self.cost}）")
        lines.append(f"总费用   : {self.cost}")
        lines.append(f"整数流   : {'是' if self.all_integral else '否（含有理参数，得到精确分数流）'}")
        lines.append("边流量：")
        for f in self.flows:
            lines.append(
                f"  {f.label:<8} {f.tail} -> {f.head}: "
                f"flow={f.flow} in [{f.lower}, {f.upper}], 单位费用 {f.cost}, 小计 {f.flow * f.cost}"
            )
        return "\n".join(lines)


# ---------------------------------------------------------------------------
# 内部残量网络（Dinic 与消圈共用）
# ---------------------------------------------------------------------------


class _Arc:
    __slots__ = ("to", "rev", "cap", "cost", "edge_idx", "forward")

    def __init__(self, to: int, rev: int, cap: Fraction, cost: Fraction,
                 edge_idx: int, forward: bool) -> None:
        self.to = to
        self.rev = rev
        self.cap = cap
        self.cost = cost
        self.edge_idx = edge_idx  # 对应原始边下标；-1 表示超源/超汇辅助弧
        self.forward = forward


class _Residual:
    def __init__(self, n: int) -> None:
        self.n = n
        self.g: List[List[_Arc]] = [[] for _ in range(n)]

    def add_arc(self, u: int, v: int, cap: Fraction, cost: Fraction,
                edge_idx: int = -1, forward: bool = True) -> _Arc:
        fwd = _Arc(v, len(self.g[v]), cap, cost, edge_idx, forward)
        rev = _Arc(u, len(self.g[u]), Z(0), -cost, edge_idx, not forward)
        self.g[u].append(fwd)
        self.g[v].append(rev)
        return fwd

    @staticmethod
    def push(arc: _Arc, g: List[List[_Arc]], amount: Fraction) -> None:
        arc.cap -= amount
        g[arc.to][arc.rev].cap += amount


# ---------------------------------------------------------------------------
# Dinic 最大流（精确 Fraction）
# ---------------------------------------------------------------------------


def _dinic_maxflow(net: _Residual, s: int, t: int,
                   limit: Fraction) -> Tuple[Fraction, List[bool]]:
    """返回 (从 s 到 t 的最大流, 最终 BFS 可达标记)。limit 为需要的最大流量上限。"""
    n = net.n
    total = Z(0)
    reachable = [False] * n

    while total < limit:
        level = [-1] * n
        level[s] = 0
        queue = [s]
        head = 0
        while head < len(queue):
            u = queue[head]
            head += 1
            for arc in net.g[u]:
                if arc.cap > 0 and level[arc.to] < 0:
                    level[arc.to] = level[u] + 1
                    queue.append(arc.to)
        if level[t] < 0:
            reachable = [lv >= 0 for lv in level]
            break
        reachable = [lv >= 0 for lv in level]

        it = [0] * n

        def dfs(u: int, pushed: Fraction) -> Fraction:
            if u == t or pushed == 0:
                return pushed
            adj = net.g[u]
            while it[u] < len(adj):
                arc = adj[it[u]]
                if arc.cap > 0 and level[arc.to] == level[u] + 1:
                    sent = dfs(arc.to, min(pushed, arc.cap))
                    if sent > 0:
                        _Residual.push(arc, net.g, sent)
                        return sent
                it[u] += 1
            return Z(0)

        while total < limit:
            pushed = dfs(s, limit - total)
            if pushed == 0:
                break
            total += pushed

    return total, reachable


# ---------------------------------------------------------------------------
# Bellman-Ford 找负费用环（从虚设零源出发，等价于全点距离初始化为 0）
# ---------------------------------------------------------------------------


def _find_negative_cycle(net: _Residual) -> Optional[List[int]]:
    """找到残量网络中的负费用环时，返回构成该环的弧下标序列（按 (u, 弧位置) 给出）。

    返回值为 [(u, arc_index_in_g[u]), ...]，沿这些弧走一圈构成负环；
    无负环返回 None。
    """
    n = net.n
    dist = [Z(0)] * n
    parent: List[Optional[Tuple[int, int]]] = [None] * n

    relaxed_node = -1
    for _ in range(n):
        relaxed_node = -1
        for u in range(n):
            du = dist[u]
            for idx, arc in enumerate(net.g[u]):
                if arc.cap > 0:
                    nd = du + arc.cost
                    if nd < dist[arc.to]:
                        dist[arc.to] = nd
                        parent[arc.to] = (u, idx)
                        relaxed_node = arc.to
        if relaxed_node == -1:
            break

    if relaxed_node == -1:
        return None

    # relaxed_node 是第 n 轮仍被松弛的点，沿父指针回溯 n 步后必在负环上。
    v = relaxed_node
    for _ in range(n):
        p = parent[v]
        assert p is not None
        v = p[0]
    cycle_start = v

    # 从环上起点收集一圈弧。
    cycle: List[Tuple[int, int]] = []
    cur = cycle_start
    seen = set()
    while cur not in seen:
        seen.add(cur)
        p = parent[cur]
        assert p is not None
        u, idx = p
        cycle.append((u, idx))
        cur = u
    cycle.reverse()
    return cycle




# ---------------------------------------------------------------------------
# 主求解器
# ---------------------------------------------------------------------------


class MinCostFlow:
    def __init__(self, max_cycle_iterations: int = 100_000) -> None:
        self._edges: List[Edge] = []
        self._balances: Dict[Node, Fraction] = {}
        self.max_cycle_iterations = max_cycle_iterations

    # ---- 构造输入 ---------------------------------------------------------

    def add_node(self, name: Node, balance: Number = 0) -> None:
        """登记节点并给出平衡量：正数=供给，负数=需求，0=中转。"""
        self._balances[str(name)] = Z(balance)

    def set_balance(self, name: Node, balance: Number) -> None:
        self._balances[str(name)] = Z(balance)

    def add_edge(self, tail: Node, head: Node, lower: Number, upper: Number,
                 cost: Number, label: Optional[str] = None) -> str:
        """添加有向边 tail -> head，流量 x 满足 lower <= x <= upper，单位费用 cost。"""
        tail, head = str(tail), str(head)
        lo, hi, c = Z(lower), Z(upper), Z(cost)
        if label is None:
            label = f"e{len(self._edges) + 1}"
        self._edges.append(Edge(tail, head, lo, hi, c, str(label)))
        # 边涉及的节点若未显式登记，按中转节点处理
        self._balances.setdefault(tail, Z(0))
        self._balances.setdefault(head, Z(0))
        return str(label)

    # ---- 求解 -------------------------------------------------------------

    def solve(self, budget: Optional[Number] = None,
              require_integral: bool = True) -> SolveResult:
        budget_limit = Z(budget) if budget is not None else None

        infeasible = self._precheck()
        if infeasible is not None:
            return SolveResult(
                feasibility=Feasibility.INFEASIBLE,
                optimality=Optimality.NOT_APPLICABLE,
                budget=BudgetVerdict.UNDECIDED,
                flows=[], cost=None, evidence=infeasible,
                iterations=0, budget_limit=budget_limit,
                all_integral=False, negative_cycles_canceled=0,
            )

        nodes = sorted(self._balances)
        index = {name: i for i, name in enumerate(nodes)}
        m = len(self._edges)

        # ---- 第 1 步：下界先占用流量 x = lower，并改写节点平衡 -------------
        forced: List[Fraction] = [e.lower for e in self._edges]
        # bal[v] = 供给(+)/需求(-) 中尚未被下界流满足的净流出需求
        bal = {v: self._balances[v] for v in nodes}
        for k, e in enumerate(self._edges):
            bal[e.tail] -= forced[k]
            bal[e.head] += forced[k]
        residual_cap = [self._edges[k].upper - forced[k] for k in range(m)]

        n = len(nodes)
        ss, tt = n, n + 1
        net = _Residual(n + 2)
        # 记录每条原始边在残量网络中的正向弧引用，供流量还原
        fwd_arcs: List[_Arc] = []
        for k, e in enumerate(self._edges):
            arc = net.add_arc(index[e.tail], index[e.head], residual_cap[k],
                              e.cost, edge_idx=k, forward=True)
            fwd_arcs.append(arc)

        required = Z(0)
        for v in nodes:
            b = bal[v]
            if b > 0:  # 还需净流出 b：超源 ss -> v
                net.add_arc(ss, index[v], b, Z(0))
                required += b
            elif b < 0:  # 还需净流入 -b：v -> 超汇 tt
                net.add_arc(index[v], tt, -b, Z(0))

        flow_achieved, reachable = _dinic_maxflow(net, ss, tt, required)

        if flow_achieved < required:
            evidence = self._build_cut_evidence(nodes, index, bal, net,
                                                reachable, required, flow_achieved)
            return SolveResult(
                feasibility=Feasibility.INFEASIBLE,
                optimality=Optimality.NOT_APPLICABLE,
                budget=BudgetVerdict.UNDECIDED,
                flows=[], cost=None, evidence=evidence,
                iterations=0, budget_limit=budget_limit,
                all_integral=False, negative_cycles_canceled=0,
            )

        # ---- 第 2 步：由最大流还原一个可行流 ------------------------------
        # 正向弧剩余容量 = residual_cap - 已推送量 => 残量部分流量 = 初始容量 - 当前容量
        residual_flow = [residual_cap[k] - fwd_arcs[k].cap for k in range(m)]
        total_flow = [forced[k] + residual_flow[k] for k in range(m)]

        self._assert_flow_conservation(nodes, total_flow)

        # 整数性判定：所有输入为整数时，算法产出必为整数流
        integral_inputs = all(
            e.lower.denominator == 1 and e.upper.denominator == 1
            and e.cost.denominator == 1 for e in self._edges
        ) and all(b.denominator == 1 for b in self._balances.values())
        all_integral = integral_inputs and all(
            f.denominator == 1 for f in total_flow
        )
        if require_integral and integral_inputs:
            assert all_integral, "整数参数下应得到整数流"

        # ---- 第 3 步：重建干净的残量网络做最小费用优化 --------------------
        opt = _Residual(n)
        opt_fwd: List[_Arc] = []
        for k, e in enumerate(self._edges):
            arc = opt.add_arc(index[e.tail], index[e.head],
                              self._edges[k].upper - total_flow[k],
                              e.cost, edge_idx=k, forward=True)
            # 反向弧容量 = 当前流量 - 下界
            opt_fwd.append(arc)
        # 上面为每条边只建了带反向零弧的正向弧；还需把“可退流”容量加到反向弧上。
        for k, e in enumerate(self._edges):
            back_cap = total_flow[k] - e.lower
            arc = opt_fwd[k]  # 正向弧；其 rev 指向自动建立的零容量反向弧
            opt.g[arc.to][arc.rev].cap = back_cap

        cost = sum(total_flow[k] * self._edges[k].cost for k in range(m))
        iterations = 0
        cycles_canceled = 0
        proven = True

        while iterations < self.max_cycle_iterations:
            cycle = _find_negative_cycle(opt)
            if cycle is None:
                break
            # 环上最小残量容量
            delta: Optional[Fraction] = None
            cycle_cost = Z(0)
            for u, idx in cycle:
                arc = opt.g[u][idx]
                if delta is None or arc.cap < delta:
                    delta = arc.cap
                cycle_cost += arc.cost
            assert delta is not None and delta > 0
            assert cycle_cost < 0, "检测到的环应为负费用环"
            for u, idx in cycle:
                arc = opt.g[u][idx]
                _Residual.push(arc, opt.g, delta)
                k = arc.edge_idx
                if k >= 0:
                    if arc.forward:
                        total_flow[k] += delta
                    else:
                        total_flow[k] -= delta
            cost += delta * cycle_cost
            cycles_canceled += 1
            iterations += 1
        else:
            proven = False

        self._assert_flow_conservation(nodes, total_flow)
        all_integral = integral_inputs and all(
            f.denominator == 1 for f in total_flow
        )
        if require_integral and integral_inputs:
            assert all_integral, "消圈后仍应为整数流"

        flows = [
            EdgeFlow(e.label, e.tail, e.head, e.lower, e.upper, e.cost,
                     total_flow[k])
            for k, e in enumerate(self._edges)
        ]

        optimality = Optimality.OPTIMAL if proven else Optimality.NOT_PROVEN
        budget_verdict = self._judge_budget(budget_limit, cost, proven)

        return SolveResult(
            feasibility=Feasibility.FEASIBLE,
            optimality=optimality,
            budget=budget_verdict,
            flows=flows,
            cost=cost,
            evidence=None,
            iterations=iterations,
            budget_limit=budget_limit,
            all_integral=all_integral,
            negative_cycles_canceled=cycles_canceled,
        )

    # ---- 内部校验与证据 ---------------------------------------------------

    def _precheck(self) -> Optional[InfeasibleEvidence]:
        if not self._edges and not self._balances:
            return None
        for e in self._edges:
            if e.lower > e.upper:
                return InfeasibleEvidence(
                    reason="bounds_violation",
                    detail=f"边 {e.label}（{e.tail} -> {e.head}）下界 {e.lower} 大于上界 {e.upper}",
                )
            if e.lower < 0:
                return InfeasibleEvidence(
                    reason="bounds_violation",
                    detail=f"边 {e.label}（{e.tail} -> {e.head}）下界 {e.lower} 为负，不允许",
                )
        total_supply = sum((b for b in self._balances.values() if b > 0), Z(0))
        total_demand = -sum((b for b in self._balances.values() if b < 0), Z(0))
        if total_supply != total_demand:
            return InfeasibleEvidence(
                reason="supply_demand_mismatch",
                detail=(f"总供给 {total_supply} 与总需求 {total_demand} 不相等，"
                        "任何流都无法同时满足所有节点平衡"),
                total_supply=total_supply,
                total_demand=total_demand,
            )
        return None

    def _build_cut_evidence(self, nodes: Sequence[Node], index: Dict[Node, int],
                            bal: Dict[Node, Fraction], net: _Residual,
                            reachable: List[bool], required: Fraction,
                            achieved: Fraction) -> InfeasibleEvidence:
        """以超源残量可达集 S 构造不可行证据。

        可行性割条件（下界已强制占用后）：对任意包含超源的集合 S，
            sum_{v in S} bal[v] <= sum_{e: tail in S, head not in S} (upper_e - lower_e)
        违反它即无可满足平衡的流。这里 S 取最大流停止时的残量可达集。
        """
        s_nodes = [nodes[i] for i in range(len(nodes)) if reachable[i]]
        s_set = set(s_nodes)

        required_out = sum((bal[v] for v in s_nodes), Z(0))

        blocked: List[Tuple[str, Node, Node, Fraction, Fraction]] = []
        backward: List[Tuple[str, Node, Node, Fraction]] = []
        cut_cap = Z(0)

        for e in self._edges:
            u_in, v_in = e.tail in s_set, e.head in s_set
            if u_in and not v_in:
                total_cap = e.upper - e.lower
                cut_cap += total_cap
                remain = self._arc_capacity(
                    net, index[e.tail], index[e.head],
                    edge_idx=self._edge_index(e.label), forward=True)
                blocked.append((e.label, e.tail, e.head, total_cap, remain))
            elif (not u_in) and v_in:
                remain_back = self._arc_capacity(
                    net, index[e.tail], index[e.head],
                    edge_idx=self._edge_index(e.label), forward=False)
                if remain_back == 0:
                    backward.append((e.label, e.tail, e.head, Z(0)))

        trapped = sorted(v for v in s_nodes if bal[v] > 0)
        unmet = sorted(v for v in nodes if bal[v] < 0 and v not in s_set)
        detail = (
            f"超源最大流只能推送 {achieved} / {required}，尚缺 {required - achieved}；"
            f"下界占用后可达集 S 内节点仍需净流出 {required_out}，"
            f"而穿出 S 的边容量合计仅 {cut_cap}。"
            f"S 内供给节点 {trapped} 无法把流量送出，"
            f"S 外需求节点 {unmet} 无法得到满足"
        )
        return InfeasibleEvidence(
            reason="cut",
            detail=detail,
            reachable=s_nodes,
            blocked_forward=blocked,
            residual_backward=backward,
            cut_capacity=cut_cap,
            required=required_out,
        )

    def _edge_index(self, label: str) -> int:
        for k, e in enumerate(self._edges):
            if e.label == label:
                return k
        return -1

    @staticmethod
    def _arc_capacity(net: _Residual, u: int, v: int, edge_idx: int,
                      forward: bool) -> Fraction:
        if forward:
            for arc in net.g[u]:
                if arc.to == v and arc.edge_idx == edge_idx and arc.forward:
                    return arc.cap
        else:
            # 反向残量弧：head -> tail
            for arc in net.g[v]:
                if arc.to == u and arc.edge_idx == edge_idx and not arc.forward:
                    return arc.cap
        return Z(0)

    def _assert_flow_conservation(self, nodes: Sequence[Node],
                                  flows: List[Fraction]) -> None:
        out: Dict[Node, Fraction] = {v: Z(0) for v in nodes}
        inn: Dict[Node, Fraction] = {v: Z(0) for v in nodes}
        for k, e in enumerate(self._edges):
            out[e.tail] += flows[k]
            inn[e.head] += flows[k]
        for v in nodes:
            net_out = out[v] - inn[v]
            assert net_out == self._balances[v], (
                f"节点 {v} 净流出 {net_out} != 平衡量 {self._balances[v]}"
            )

    @staticmethod
    def _judge_budget(budget_limit: Optional[Fraction], cost: Fraction,
                      proven: bool) -> BudgetVerdict:
        if budget_limit is None:
            return BudgetVerdict.UNDECIDED
        if not proven:
            # 即便当前费用已超预算，继续消圈只会让费用下降，故不能断言预算不可行
            return BudgetVerdict.UNDECIDED
        if cost <= budget_limit:
            return BudgetVerdict.WITHIN_BUDGET
        return BudgetVerdict.OVER_BUDGET


# ---------------------------------------------------------------------------
# 便捷函数
# ---------------------------------------------------------------------------


def solve_min_cost_flow(balances: Dict[Node, Number],
                        edges: Sequence[Tuple[Node, Node, Number, Number, Number]],
                        budget: Optional[Number] = None,
                        max_cycle_iterations: int = 100_000) -> SolveResult:
    """便捷入口。

    balances: {节点: 平衡量}，正供给、负需求、0 中转。
    edges:    (tail, head, lower, upper, unit_cost) 序列。
    """
    mcf = MinCostFlow(max_cycle_iterations=max_cycle_iterations)
    for name, b in balances.items():
        mcf.add_node(name, b)
    for tail, head, lo, hi, cost in edges:
        mcf.add_edge(tail, head, lo, hi, cost)
    return mcf.solve(budget=budget)


if __name__ == "__main__":
    # 不接受命令行参数时给出一个最小演示
    demo = MinCostFlow()
    demo.add_node("s", 3)
    demo.add_node("t", -3)
    demo.add_edge("s", "t", 0, 5, 2, "direct")
    result = demo.solve()
    print(result.render())
    sys.exit(0)
