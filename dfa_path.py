#!/usr/bin/env python3
"""Finite-alphabet path compliance.

Given a directed multigraph whose edges carry a single-character label and a
non-negative cost, plus a DFA over the same alphabet, find a walk from a
source vertex s to a target vertex t whose edge-label string is accepted by
the DFA (vertices may repeat).  Among all accepted walks choose:

  1. minimum total cost, then
  2. minimum number of edges, then
  3. lexicographically smallest label sequence, then
  4. lexicographically smallest edge-ID sequence.

The search runs Dijkstra over the product graph (vertex, dfa_state) with the
tuple (cost, edges, labels, edge_ids) as priority key.  Because edge costs
are non-negative, every edge extension strictly increases the key (cost grows
or, at equal cost, the edge count grows), so each product state is settled at
most once and zero-cost cycles cannot cause an infinite search.

Usage:
    python3 dfa_path.py [input.json]     # reads stdin when file omitted or "-"
"""

from __future__ import annotations

import argparse
import heapq
import json
import sys


class SpecError(ValueError):
    """Raised when the input specification is invalid."""


def _is_number(value) -> bool:
    return isinstance(value, (int, float)) and not isinstance(value, bool)


def validate(spec: dict) -> dict:
    """Validate the raw JSON spec and return normalized structures."""
    if not isinstance(spec, dict):
        raise SpecError("top-level spec must be a JSON object")

    alphabet = spec.get("alphabet")
    if (
        not isinstance(alphabet, list)
        or not alphabet
        or any(not isinstance(c, str) or len(c) != 1 for c in alphabet)
        or len(set(alphabet)) != len(alphabet)
    ):
        raise SpecError("alphabet must be a non-empty list of unique single characters")
    alphabet_set = set(alphabet)

    graph = spec.get("graph")
    if not isinstance(graph, dict):
        raise SpecError("graph must be an object")
    vertices = graph.get("vertices")
    if (
        not isinstance(vertices, list)
        or any(not isinstance(v, str) for v in vertices)
        or len(set(vertices)) != len(vertices)
    ):
        raise SpecError("graph.vertices must be a list of unique strings")
    vertex_set = set(vertices)

    edges = graph.get("edges")
    if not isinstance(edges, list):
        raise SpecError("graph.edges must be a list")
    seen_ids = set()
    id_types = set()
    norm_edges = []
    for i, e in enumerate(edges):
        if not isinstance(e, dict):
            raise SpecError(f"edge #{i} must be an object")
        eid = e.get("id")
        if isinstance(eid, bool) or not isinstance(eid, (int, str)):
            raise SpecError(f"edge #{i}: id must be an int or a string")
        if eid in seen_ids:
            raise SpecError(f"duplicate edge id: {eid!r}")
        seen_ids.add(eid)
        id_types.add(type(eid))
        src, dst, label, cost = e.get("src"), e.get("dst"), e.get("label"), e.get("cost")
        if src not in vertex_set or dst not in vertex_set:
            raise SpecError(f"edge {eid!r}: src/dst must be declared vertices")
        if not isinstance(label, str) or label not in alphabet_set:
            raise SpecError(f"edge {eid!r}: label must be a single alphabet character")
        if not _is_number(cost) or cost < 0:
            raise SpecError(f"edge {eid!r}: cost must be a non-negative number")
        norm_edges.append((eid, src, dst, label, cost))
    if len(id_types) > 1:
        raise SpecError("edge ids must be all ints or all strings (homogeneous)")

    dfa = spec.get("dfa")
    if not isinstance(dfa, dict):
        raise SpecError("dfa must be an object")
    states = dfa.get("states")
    if (
        not isinstance(states, list)
        or not states
        or any(not isinstance(s, str) for s in states)
        or len(set(states)) != len(states)
    ):
        raise SpecError("dfa.states must be a non-empty list of unique strings")
    state_set = set(states)
    q0 = dfa.get("start")
    if q0 not in state_set:
        raise SpecError("dfa.start must be a declared state")
    accept = dfa.get("accept")
    if not isinstance(accept, list) or any(a not in state_set for a in accept):
        raise SpecError("dfa.accept must be a list of declared states")
    raw_trans = dfa.get("transitions", {})
    if not isinstance(raw_trans, dict):
        raise SpecError("dfa.transitions must be an object {state: {char: state}}")
    transitions = {}
    for st, row in raw_trans.items():
        if st not in state_set:
            raise SpecError(f"dfa.transitions: unknown state {st!r}")
        if not isinstance(row, dict):
            raise SpecError(f"dfa.transitions[{st!r}] must be an object")
        for ch, nxt in row.items():
            if ch not in alphabet_set:
                raise SpecError(f"dfa.transitions[{st!r}]: {ch!r} not in alphabet")
            if nxt not in state_set:
                raise SpecError(f"dfa.transitions[{st!r}][{ch!r}]: unknown state")
            transitions[(st, ch)] = nxt

    source, target = spec.get("source"), spec.get("target")
    if source not in vertex_set or target not in vertex_set:
        raise SpecError("source and target must be declared vertices")

    adj: dict[str, list] = {v: [] for v in vertices}
    for eid, src, dst, label, cost in norm_edges:
        adj[src].append((eid, dst, label, cost))

    return {
        "adj": adj,
        "transitions": transitions,
        "accept": frozenset(accept),
        "start_state": q0,
        "source": source,
        "target": target,
    }


def solve(spec: dict) -> dict:
    """Solve one instance; returns a JSON-serializable result dict."""
    data = validate(spec)
    adj = data["adj"]
    transitions = data["transitions"]
    accept = data["accept"]
    source, target, q0 = data["source"], data["target"], data["start_state"]

    start = (source, q0)
    start_key = (0, 0, (), ())
    best = {start: start_key}
    parent: dict[tuple, tuple] = {}
    heap = [(0, 0, (), (), source, q0)]
    settled_key = None
    settled_state = None

    while heap:
        cost, nedges, labels, ids, vertex, qstate = heapq.heappop(heap)
        key = (cost, nedges, labels, ids)
        if key != best.get((vertex, qstate)):
            continue  # stale heap entry
        if vertex == target and qstate in accept:
            settled_key = key
            settled_state = (vertex, qstate)
            break
        for eid, dst, label, ecost in adj[vertex]:
            qnext = transitions.get((qstate, label))
            if qnext is None:
                continue  # DFA rejects this extension
            nkey = (cost + ecost, nedges + 1, labels + (label,), ids + (eid,))
            nstate = (dst, qnext)
            if nstate not in best or nkey < best[nstate]:
                best[nstate] = nkey
                parent[nstate] = ((vertex, qstate), eid, label)
                heapq.heappush(heap, (*nkey, dst, qnext))

    if settled_state is None:
        return {"found": False}

    edge_ids, label_list, vertex_path, dfa_path = [], [], [], []
    vertex_path.append(settled_state[0])
    dfa_path.append(settled_state[1])
    cur = settled_state
    while cur != start:
        prev, eid, label = parent[cur]
        edge_ids.append(eid)
        label_list.append(label)
        cur = prev
        vertex_path.append(cur[0])
        dfa_path.append(cur[1])
    edge_ids.reverse()
    label_list.reverse()
    vertex_path.reverse()
    dfa_path.reverse()

    cost = settled_key[0]
    if isinstance(cost, float) and cost.is_integer():
        cost = int(cost)
    return {
        "found": True,
        "cost": cost,
        "edge_count": settled_key[1],
        "labels": "".join(label_list),
        "edge_ids": edge_ids,
        "vertex_path": vertex_path,
        "dfa_state_path": dfa_path,
    }


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        description="Cheapest s-t walk whose label string is accepted by a DFA."
    )
    parser.add_argument(
        "input",
        nargs="?",
        default="-",
        help="JSON instance file (default: read from stdin)",
    )
    parser.add_argument("--pretty", action="store_true", help="pretty-print JSON output")
    args = parser.parse_args(argv)

    try:
        text = sys.stdin.read() if args.input == "-" else open(args.input, encoding="utf-8").read()
        spec = json.loads(text)
    except OSError as exc:
        print(f"error: cannot read input: {exc}", file=sys.stderr)
        return 2
    except json.JSONDecodeError as exc:
        print(f"error: invalid JSON: {exc}", file=sys.stderr)
        return 2

    try:
        result = solve(spec)
    except SpecError as exc:
        print(f"error: invalid spec: {exc}", file=sys.stderr)
        return 2

    indent = 2 if args.pretty else None
    print(json.dumps(result, ensure_ascii=False, indent=indent))
    return 0


if __name__ == "__main__":
    sys.exit(main())
