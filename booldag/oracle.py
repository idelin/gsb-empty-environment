"""Independent oracle: evaluate the boolean expression directly at sample points.

Sampling strategy: collect every finite endpoint coordinate appearing in the
leaf states, then probe (a) each endpoint itself, (b) the rational midpoint of
each gap between consecutive endpoints, and (c) one point beyond each extreme.
Membership of a normalized interval set is constant on each open gap and each
endpoint, so these probes fully determine the answer — no floats, no epsilon.
"""

from __future__ import annotations

from fractions import Fraction
from typing import Dict, List, Optional, Tuple

from .dag import Graph, Leaf, Node, OpNode
from .intervals import IntervalSet

Spec = Tuple  # ('leaf', name) | ('op', op, (sub, ...))


def spec_of(node: Node) -> Spec:
    if isinstance(node, Leaf):
        return ("leaf", node.name)
    return ("op", node.op, tuple(spec_of(p) for p in node.parents))


def sample_points(states: Dict[str, Optional[IntervalSet]]) -> List[Fraction]:
    coords = set()
    for st in states.values():
        if st is None:
            continue
        for iv in st.intervals:
            if iv.lo is not None:
                coords.add(iv.lo)
            if iv.hi is not None:
                coords.add(iv.hi)
    pts = set(coords)
    ordered = sorted(coords)
    for a, b in zip(ordered, ordered[1:]):
        pts.add((a + b) / 2)
    if ordered:
        pts.add(ordered[0] - 1)
        pts.add(ordered[-1] + 1)
    else:
        pts.add(Fraction(0))
    return sorted(pts)


def eval_spec(spec: Spec, states: Dict[str, Optional[IntervalSet]], x: Fraction):
    """True/False membership, or None if the expression is unknown at x."""
    if spec[0] == "leaf":
        st = states[spec[1]]
        return None if st is None else st.contains(x)
    _, op, subs = spec
    vals = [eval_spec(s, states, x) for s in subs]
    if any(v is None for v in vals):
        return None
    if op == "union":
        return any(vals)
    if op == "intersect":
        return all(vals)
    if op == "diff":
        return vals[0] and not vals[1]
    raise ValueError(op)


def check_node(graph: Graph, node: Node) -> int:
    """Cross-check ``node`` against the oracle at every sample point.

    Returns the number of probe points checked.  Raises AssertionError on any
    mismatch (including unknown-vs-known divergence).
    """
    states: Dict[str, Optional[IntervalSet]] = {}
    for name in graph.leaves_of(node):
        leaf = graph.nodes[name]
        states[name] = leaf.state
    spec = spec_of(node)
    points = sample_points(states)
    for x in points:
        expected = eval_spec(spec, states, x)
        if expected is None:
            assert node.is_unknown(), (
                f"{node.name}: oracle unknown at {x} but node={node.value!r}"
            )
        else:
            assert not node.is_unknown(), (
                f"{node.name}: oracle says {expected} at {x} but node is unknown"
            )
            actual = node.value.contains(x)
            assert actual == expected, (
                f"{node.name}: oracle={expected} node={actual} at x={x} "
                f"(value={node.value!r})"
            )
    return len(points)
