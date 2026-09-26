"""Exact integer minimum-cost flow with lower and upper edge bounds."""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from enum import Enum
from fractions import Fraction
from math import ceil, floor
from typing import Any, Hashable, Optional


class Status(Enum):
    OPTIMAL = "optimal"
    FEASIBLE = "feasible"
    BUDGET_UNDECIDED = "budget_undecided"
    INFEASIBLE = "infeasible"


@dataclass(frozen=True)
class Edge:
    tail: Hashable
    head: Hashable
    lower: Any
    upper: Any
    cost: Any
    key: Optional[Hashable] = None


@dataclass(frozen=True)
class InfeasibilityEvidence:
    kind: str
    message: str
    details: dict[str, Any]


@dataclass(frozen=True)
class CutEvidence:
    reachable: tuple[Hashable, ...]
    unreachable: tuple[Hashable, ...]
    crossing_forward: tuple[tuple[Hashable, Hashable, Hashable | None], ...]
    crossing_reverse: tuple[tuple[Hashable, Hashable, Hashable | None], ...]
    upper_bound_on_net_outflow: int
    required_net_outflow: int
    deficit: int


@dataclass(frozen=True)
class SolveResult:
    status: Status
    flow: dict[Hashable, int]
    cost: Fraction
    evidence: InfeasibilityEvidence | None
    cycle: tuple[Hashable | None, ...] | None = None
    cycles_canceled: int = 0


def _rational(value: Any, name: str) -> Fraction:
    if isinstance(value, bool) or isinstance(value, float):
        raise ValueError(f"{name} must be int, Fraction, or rational str")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, Fraction):
        return value
    if isinstance(value, str):
        text = value.strip()
        if not text:
            raise ValueError(f"{name} must be a finite rational number")
        try:
            return Fraction(text)
        except (ZeroDivisionError, ValueError) as exc:
            raise ValueError(f"{name} must be a finite rational number") from exc
    raise ValueError(f"{name} must be int, Fraction, or rational str")


@dataclass(slots=True)
class _Arc:
    to: int
    rev: int
    cap: int
    edge_id: int
    direction: int


class _Dinic:
    def __init__(self, n: int) -> None:
        self.n = n
        self.graph = [[] for _ in range(n)]
        self.initial_cap: dict[tuple[int, int, int], int] = {}

    def add(self, u: int, v: int, cap: int, edge_id: int, direction: int) -> int:
        forward = _Arc(v, -1, cap, edge_id, direction)
        index = len(self.graph[u])
        self.graph[u].append(forward)
        reverse = _Arc(u, index, 0, edge_id, -direction)
        forward.rev = len(self.graph[v])
        self.graph[v].append(reverse)
        if edge_id >= 0:
            self.initial_cap[(u, v, index)] = cap
        return index

    def max_flow(self, source: int, sink: int, limit: Optional[int]) -> int:
        total = 0
        while limit is None or total < limit:
            level = [-1] * self.n
            level[source] = 0
            queue = deque([source])
            while queue:
                u = queue.popleft()
                for arc in self.graph[u]:
                    if arc.cap > 0 and level[arc.to] < 0:
                        level[arc.to] = level[u] + 1
                        queue.append(arc.to)
            if level[sink] < 0:
                break
            it = [0] * self.n

            def dfs(u: int, pushed: int) -> int:
                if u == sink:
                    return pushed
                while it[u] < len(self.graph[u]):
                    arc = self.graph[u][it[u]]
                    if arc.cap > 0 and level[arc.to] == level[u] + 1:
                        amount = dfs(arc.to, min(pushed, arc.cap))
                        if amount:
                            arc.cap -= amount
                            self.graph[arc.to][arc.rev].cap += amount
                            return amount
                    it[u] += 1
                return 0

            while limit is None or total < limit:
                room = 10**30 if limit is None else limit - total
                amount = dfs(source, room)
                if not amount:
                    break
                total += amount
        return total

    def reachable_from(self, source: int) -> set[int]:
        seen = {source}
        queue = deque([source])
        while queue:
            u = queue.popleft()
            for arc in self.graph[u]:
                if arc.cap > 0 and arc.to not in seen:
                    seen.add(arc.to)
                    queue.append(arc.to)
        return seen


class FlowProblem:
    def __init__(self, edges: list[Edge], supplies: dict[Hashable, Any]):
        self.edges = list(edges)
        if not all(isinstance(edge, Edge) for edge in self.edges):
            raise ValueError("all edges must be Edge instances")

        nodes: list[Hashable] = []
        node_id: dict[Hashable, int] = {}

        def add_node(node: Hashable) -> int:
            if not isinstance(node, Hashable):
                raise ValueError("node labels must be hashable")
            if node not in node_id:
                node_id[node] = len(nodes)
                nodes.append(node)
            return node_id[node]

        seen_keys: set[Hashable] = set()
        prepared: list[tuple[int, int, Fraction, Fraction, Fraction, Any]] = []
        for index, edge in enumerate(self.edges):
            u = add_node(edge.tail)
            v = add_node(edge.head)
            key = index if edge.key is None else edge.key
            if key in seen_keys:
                raise ValueError(f"duplicate edge key: {key!r}")
            seen_keys.add(key)
            lower = _rational(edge.lower, f"edge {key!r} lower")
            upper = _rational(edge.upper, f"edge {key!r} upper")
            cost = _rational(edge.cost, f"edge {key!r} cost")
            if lower < 0:
                raise ValueError(f"edge {key!r} has negative lower bound")
            prepared.append((u, v, lower, upper, cost, key))

        if any(not isinstance(node, Hashable) for node in supplies):
            raise ValueError("supply node labels must be hashable")
        for node in supplies:
            add_node(node)
        if len(node_id) != len(nodes):
            raise ValueError("node labels must be unique")

        rational_supplies: dict[int, Fraction] = {}
        for node, value in supplies.items():
            rational_supplies[node_id[node]] = _rational(value, f"supply[{node!r}]")

        self.nodes = tuple(nodes)
        self.node_id = node_id
        self.edges_data = tuple(prepared)
        self.supplies = rational_supplies

    @staticmethod
    def _infeasible(kind: str, message: str, **details: Any) -> SolveResult:
        return SolveResult(Status.INFEASIBLE, {}, Fraction(0),
                           InfeasibilityEvidence(kind, message, details))

    def solve(self, *, prove_optimal: bool = True,
              max_cycles: Optional[int] = 10_000) -> SolveResult:
        if max_cycles is not None and (not isinstance(max_cycles, int) or max_cycles < 0):
            raise ValueError("max_cycles must be a non-negative int or None")

        balance = [Fraction(0) for _ in self.nodes]
        for node_id, supply in self.supplies.items():
            balance[node_id] += supply
        total_supply = sum((value for value in self.supplies.values() if value > 0), Fraction(0))
        total_demand = -sum((value for value in self.supplies.values() if value < 0), Fraction(0))
        if total_supply != total_demand:
            return self._infeasible(
                "supply_demand_mismatch",
                "total supply and total demand differ",
                total_supply=total_supply,
                total_demand=total_demand,
                difference=total_supply - total_demand,
            )

        lower_ints: list[int] = []
        upper_ints: list[int] = []
        flow: dict[Hashable, int] = {}
        for edge_id, (_, _, lower, upper, _, key) in enumerate(self.edges_data):
            low = ceil(lower)
            high = floor(upper)
            lower_ints.append(low)
            upper_ints.append(high)
            flow[key] = low
            if low > high:
                return self._infeasible(
                    "empty_integer_interval",
                    "edge has no integer value in its bounded interval",
                    edge=key,
                    lower=lower,
                    upper=upper,
                    integer_lower=low,
                    integer_upper=high,
                )
            balance[self.edges_data[edge_id][0]] -= low
            balance[self.edges_data[edge_id][1]] += low

        if any(value.denominator != 1 for value in balance):
            return self._infeasible(
                "fractional_required_balance",
                "lower-bound-adjusted node balance is not integral",
                balances={node: value for node, value in zip(self.nodes, balance)},
            )
        balance_ints = [int(value) for value in balance]
        required = sum(value for value in balance_ints if value > 0)

        n = len(self.nodes)
        source = n
        sink = n + 1
        dinic = _Dinic(n + 2)
        for edge_id, (u, v, _, _, _, _) in enumerate(self.edges_data):
            dinic.add(u, v, upper_ints[edge_id] - lower_ints[edge_id], edge_id, 1)
        for node_id, value in enumerate(balance_ints):
            if value > 0:
                dinic.add(source, node_id, value, -1, 0)
            elif value < 0:
                dinic.add(node_id, sink, -value, -1, 0)

        pushed = dinic.max_flow(source, sink, required)
        if pushed != required:
            reachable_ids = dinic.reachable_from(source)
            reachable = tuple(self.nodes[i] for i in range(n) if i in reachable_ids)
            unreachable = tuple(self.nodes[i] for i in range(n) if i not in reachable_ids)
            forward: list[tuple[Hashable, Hashable, Hashable | None]] = []
            reverse: list[tuple[Hashable, Hashable, Hashable | None]] = []
            upper_net = 0
            required_net = 0
            for edge_id, (u, v, _, _, _, key) in enumerate(self.edges_data):
                if u in reachable_ids and v not in reachable_ids:
                    forward.append((self.nodes[u], self.nodes[v], key))
                    upper_net += upper_ints[edge_id]
                if v in reachable_ids and u not in reachable_ids:
                    reverse.append((self.nodes[u], self.nodes[v], key))
                    upper_net -= lower_ints[edge_id]
            for i, value in enumerate(balance_ints):
                if i in reachable_ids:
                    required_net += value
            evidence = CutEvidence(
                reachable=reachable,
                unreachable=unreachable,
                crossing_forward=tuple(forward),
                crossing_reverse=tuple(reverse),
                upper_bound_on_net_outflow=upper_net,
                required_net_outflow=required_net,
                deficit=required_net - upper_net,
            )
            return self._infeasible(
                "cut_certificate",
                "no feasible flow satisfies lower bounds and node balances",
                cut=evidence,
                max_flow=pushed,
                required_flow=required,
            )

        for u, v, index in dinic.initial_cap:
            arc = dinic.graph[u][index]
            edge_id = arc.edge_id
            if edge_id >= 0 and arc.direction == 1:
                initial = dinic.initial_cap[(u, v, index)]
                flow[self.edges_data[edge_id][5]] += initial - arc.cap

        cost = Fraction(0)
        for edge_id, (_, _, _, _, edge_cost, key) in enumerate(self.edges_data):
            cost += edge_cost * flow[key]

        if not prove_optimal:
            return SolveResult(Status.FEASIBLE, dict(flow), cost, None)

        cycles_canceled = 0
        while True:
            cycle = self._negative_cycle(dinic)
            if cycle is None:
                return SolveResult(Status.OPTIMAL, dict(flow), cost, None,
                                   cycles_canceled=cycles_canceled)
            if max_cycles is not None and cycles_canceled >= max_cycles:
                return SolveResult(
                    Status.BUDGET_UNDECIDED,
                    dict(flow),
                    cost,
                    None,
                    cycle=tuple(
                    [self.nodes[cycle[0][0]]]
                    + [self.nodes[arc.to] for _, arc in cycle]
                ),
                    cycles_canceled=cycles_canceled,
                )
            delta = min(arc.cap for node, arc in cycle)
            cycle_cost = Fraction(0)
            for _, arc in cycle:
                edge_id = arc.edge_id
                signed_cost = self.edges_data[edge_id][4] * arc.direction
                cycle_cost += signed_cost
                key = self.edges_data[edge_id][5]
                flow[key] += delta * arc.direction
                arc.cap -= delta
                dinic.graph[arc.to][arc.rev].cap += delta
            cost += delta * cycle_cost
            cycles_canceled += 1

    def _negative_cycle(self, dinic: _Dinic) -> Optional[list[tuple[int, _Arc]]]:
        n = len(self.nodes)
        distance = [Fraction(0) for _ in range(n)]
        predecessor: list[Optional[tuple[int, _Arc]]] = [None] * n
        changed = -1
        for iteration in range(n):
            changed = -1
            for u in range(n):
                base = distance[u]
                for arc in dinic.graph[u]:
                    if arc.to >= n or arc.cap <= 0:
                        continue
                    candidate = base + self.edges_data[arc.edge_id][4] * arc.direction
                    if candidate < distance[arc.to]:
                        distance[arc.to] = candidate
                        predecessor[arc.to] = (u, arc)
                        changed = arc.to
            if changed < 0:
                return None
        start = changed
        for _ in range(n):
            previous = predecessor[start]
            if previous is None:
                return None
            start = previous[0]
        cycle = []
        visited = set()
        current = start
        while current not in visited:
            visited.add(current)
            previous = predecessor[current]
            if previous is None:
                return None
            u, arc = previous
            cycle.append((u, arc))
            current = u
        cycle.reverse()
        return cycle
