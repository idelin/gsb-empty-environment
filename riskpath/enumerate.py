"""小图全简单路径枚举，用于与标签设置搜索对照验证。"""
from __future__ import annotations

import math
from dataclasses import dataclass
from fractions import Fraction
from typing import List, Optional, Tuple

from .model import Graph, Number
from .search import pareto_frontier


@dataclass(frozen=True)
class PathInfo:
    edges: Tuple[int, ...]
    cost: Number
    delay: Number
    groups: frozenset
    nll: float
    success: Fraction


def enumerate_simple_paths(graph: Graph, source: str, target: str) -> List[PathInfo]:
    """DFS 枚举全部简单路径（小图专用，指数复杂度）。"""
    results: List[PathInfo] = []

    def dfs(node, visited, edges, cost, delay, groups) -> None:
        if node == target:
            results.append(PathInfo(
                edges=edges, cost=cost, delay=delay, groups=groups,
                nll=graph.nll_of(groups), success=graph.success_of(groups)))
            return
        for edge_id, edge in graph.outgoing(node):
            if edge.dst in visited:
                continue
            dfs(edge.dst, visited | {edge.dst}, edges + (edge_id,),
                cost + edge.cost, delay + edge.delay, groups | {edge.group})

    dfs(source, frozenset({source}), (), 0, 0, frozenset())
    return results


@dataclass
class EnumerationResult:
    all_paths: List[PathInfo]
    feasible: List[PathInfo]
    frontier: List[PathInfo]
    best: Optional[PathInfo]


def evaluate(graph: Graph, source: str, target: str, max_cost, max_delay) -> EnumerationResult:
    """枚举 + 双预算过滤 + 前沿/最优，作为搜索的基准对照。"""
    all_paths = enumerate_simple_paths(graph, source, target)
    feasible = [p for p in all_paths
                if p.cost <= max_cost and p.delay <= max_delay
                and not math.isinf(p.nll)]
    frontier = pareto_frontier(feasible)
    best = min(frontier, key=lambda p: (p.nll, p.cost, p.delay), default=None)
    return EnumerationResult(all_paths=all_paths, feasible=feasible,
                             frontier=frontier, best=best)
