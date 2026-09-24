"""Independent exhaustive reference matcher used to validate the
incremental engine in tests."""
from __future__ import annotations

import itertools
from typing import Set

from .graph import TargetGraph
from .matcher import mapping_id
from .pattern import PatternGraph


def brute_force_match_ids(graph: TargetGraph, pattern: PatternGraph, *,
                          time: int = 0) -> Set[str]:
    pvs = sorted(pattern.vertices)
    tvs = sorted(graph.vertices)
    found: Set[str] = set()
    for perm in itertools.permutations(tvs, len(pvs)):
        m = dict(zip(pvs, perm))
        ok = True
        for pid in pvs:
            pv = pattern.vertices[pid]
            tv = graph.vertices[m[pid]]
            if pv.type is not None and pv.type != tv.type:
                ok = False
                break
            if not all(p.test(tv.attrs) for p in pv.predicates):
                ok = False
                break
        if not ok:
            continue
        for pe in pattern.edges:
            witness = any(
                (pe.type is None or e.type == pe.type)
                and all(p.test(e.attrs) for p in pe.predicates)
                for e in graph.active_edges_between(m[pe.src], m[pe.dst], time))
            if pe.exists and not witness:
                ok = False
                break
            if not pe.exists and witness:
                ok = False
                break
        if ok:
            found.add(mapping_id(m))
    return found
