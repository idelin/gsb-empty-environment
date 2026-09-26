"""Brute-force enumeration of every feasible simple path (test oracle).

Used by the unit tests as an independent cross-check for the dominating
label-correcting search on small random graphs.
"""

from __future__ import annotations

from fractions import Fraction
from typing import List

from .model import Graph, to_fraction
from .search import Label


def enumerate_simple_paths(graph: Graph, source, target, *,
                           cost_budget, delay_budget) -> List[Label]:
    cost_budget = to_fraction(cost_budget)
    delay_budget = to_fraction(delay_budget)
    found: List[Label] = []

    def dfs(label: Label) -> None:
        if label.node == target:
            found.append(label)
            return
        for edge in graph.out_edges(label.node):
            if edge.head in label.visited:
                continue
            cost = label.cost + edge.cost
            delay = label.delay + edge.delay
            if cost > cost_budget or delay > delay_budget:
                continue
            if edge.group in label.groups:
                log_success, groups = label.log_success, label.groups
            else:
                log_success = label.log_success + graph.group_log(edge.group)
                groups = label.groups | {edge.group}
            dfs(Label(
                edge.head, cost, delay, log_success, groups,
                label.visited | {edge.head}, label.path + (edge.head,),
            ))

    dfs(Label(source, Fraction(0), Fraction(0), 0.0,
              frozenset(), frozenset({source}), (source,)))
    return found
