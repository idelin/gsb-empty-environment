"""Graph algorithms: Tarjan SCCs, condensation, stage leveling, path evidence."""

from __future__ import annotations

from collections import deque


def tarjan_scc(nodes: list[str], edges: dict[str, list[str]]) -> list[list[str]]:
    """Iterative Tarjan. Returns SCCs; member order within an SCC is arbitrary."""
    index_of: dict[str, int] = {}
    lowlink: dict[str, int] = {}
    on_stack: set[str] = set()
    stack: list[str] = []
    components: list[list[str]] = []
    counter = 0

    for root in nodes:
        if root in index_of:
            continue
        work: list[tuple[str, int]] = [(root, 0)]
        while work:
            node, child_i = work[-1]
            if child_i == 0:
                index_of[node] = lowlink[node] = counter
                counter += 1
                stack.append(node)
                on_stack.add(node)
            recursed = False
            neighbors = edges.get(node, [])
            for i in range(child_i, len(neighbors)):
                nxt = neighbors[i]
                if nxt not in index_of:
                    work[-1] = (node, i + 1)
                    work.append((nxt, 0))
                    recursed = True
                    break
                if nxt in on_stack:
                    lowlink[node] = min(lowlink[node], index_of[nxt])
            if recursed:
                continue
            work.pop()
            if work:
                parent = work[-1][0]
                lowlink[parent] = min(lowlink[parent], lowlink[node])
            if lowlink[node] == index_of[node]:
                component = []
                while True:
                    member = stack.pop()
                    on_stack.discard(member)
                    component.append(member)
                    if member == node:
                        break
                components.append(sorted(component))
    return components


def condensation_stages(
    components: list[list[str]], edges: dict[str, list[str]]
) -> list[int]:
    """Assign each component a stage: 0 = no dependencies, else 1 + max(dep stage).

    The condensation of a graph is always a DAG, so stages are well defined.
    """
    comp_of: dict[str, int] = {}
    for idx, comp in enumerate(components):
        for member in comp:
            comp_of[member] = idx
    dep_comps: list[set[int]] = [set() for _ in components]
    for node, neighbors in edges.items():
        for nxt in neighbors:
            src, dst = comp_of[node], comp_of[nxt]
            if src != dst:
                dep_comps[src].add(dst)
    stages = [-1] * len(components)

    def stage_of(idx: int) -> int:
        if stages[idx] == -1:
            stages[idx] = 0  # guard; condensation is acyclic
            stages[idx] = 1 + max((stage_of(d) for d in dep_comps[idx]), default=-1)
        return stages[idx]

    for idx in range(len(components)):
        stage_of(idx)
    return stages


def shortest_path(edges: dict[str, list[str]], start: str, goal: str) -> list[str] | None:
    """BFS shortest path start -> ... -> goal, or None. Used as cycle evidence."""
    if start == goal:
        return [start]
    prev: dict[str, str] = {}
    queue = deque([start])
    seen = {start}
    while queue:
        node = queue.popleft()
        for nxt in edges.get(node, []):
            if nxt in seen:
                continue
            seen.add(nxt)
            prev[nxt] = node
            if nxt == goal:
                path = [goal]
                while path[-1] != start:
                    path.append(prev[path[-1]])
                return list(reversed(path))
            queue.append(nxt)
    return None
