"""Computation graph definitions shared by the planner and the engine."""
from __future__ import annotations


class Node:
    """A single op in the computation graph.

    fwd: callable(rng, *input_tensors) -> Tensor
    bwd: callable(grad_out, input_tensors, output_tensor) -> list[Tensor]
    cost: abstract recompute cost (time units) used by the planner.
    side_effect: True if fwd touches state outside the graph (I/O, globals).
    replayable: explicit declaration that a side-effecting node is still
        safe to recompute.  Without it the engine refuses to recompute the
        node and its output must be checkpointed.
    """

    def __init__(self, name, inputs, fwd, bwd, cost=1.0,
                 side_effect=False, replayable=False):
        self.name = name
        self.inputs = list(inputs)
        self.fwd = fwd
        self.bwd = bwd
        self.cost = float(cost)
        self.side_effect = bool(side_effect)
        self.replayable = bool(replayable)

    def __repr__(self):
        return f"Node({self.name!r}, inputs={self.inputs!r}, cost={self.cost})"


def topo_order(nodes, input_names):
    """Deterministic topological order (DFS in declaration order)."""
    by_name = {n.name: n for n in nodes}
    inputs = set(input_names)
    order = []
    state = {}

    def visit(name):
        s = state.get(name, 0)
        if s == 2:
            return
        if s == 1:
            raise ValueError(f"cycle involving {name!r}")
        state[name] = 1
        for dep in by_name[name].inputs:
            if dep in by_name:
                visit(dep)
            elif dep not in inputs:
                raise KeyError(f"unknown input {dep!r} of node {name!r}")
        state[name] = 2
        order.append(name)

    for n in nodes:
        visit(n.name)
    return order


def descendant_counts(nodes):
    """Number of transitive descendants per node (used by DAG heuristic)."""
    children = {n.name: [] for n in nodes}
    for n in nodes:
        for dep in n.inputs:
            if dep in children:
                children[dep].append(n.name)
    counts = {}
    for n in nodes:
        seen = set()
        stack = list(children[n.name])
        while stack:
            u = stack.pop()
            if u in seen:
                continue
            seen.add(u)
            stack.extend(children.get(u, ()))
        counts[n.name] = len(seen)
    return counts
