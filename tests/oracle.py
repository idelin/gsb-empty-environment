"""Brute-force all-pairs oracle used to cross-check the tree engine.

Shares the exact geometry and evidence-finalisation code with the
engine (so semantics match by construction) but deliberately uses a
completely different traversal: plain enumeration of every eligible
segment pair.
"""

from __future__ import annotations

from engine import build_evidence, finalize_evidence
from geometry import closest_params


def oracle_query_all(model, exclusions=frozenset(), radius2=None, mode="all"):
    """model: {group_name: {seg_id: (p0, p1)}}.

    Returns ``(best_dist2_or_None, evidence_tuple)`` with the same
    semantics as ``Engine.query_all``.
    """
    best = radius2
    evidence = []
    names = sorted(model)
    for i, a in enumerate(names):
        for b in names[i + 1:]:
            if frozenset((a, b)) in exclusions:
                continue
            for seg_a, (p0, p1) in sorted(model[a].items()):
                for seg_b, (q0, q1) in sorted(model[b].items()):
                    m = closest_params(p0, p1, q0, q1)
                    if best is not None and m.dist2 > best:
                        continue
                    ev = build_evidence(a, seg_a, p0, p1,
                                        b, seg_b, q0, q1, m)
                    if best is None or m.dist2 < best:
                        best = m.dist2
                        evidence = [ev]
                    else:
                        evidence.append(ev)
    if not evidence:
        return None, ()
    return best, finalize_evidence(evidence, mode)


def oracle_query_pair(model, a, b, exclusions=frozenset(), radius2=None,
                      mode="all"):
    """Same, restricted to one ordered pair of groups (caller order)."""
    if frozenset((a, b)) in exclusions:
        return None, ()
    best = radius2
    evidence = []
    for seg_a, (p0, p1) in sorted(model[a].items()):
        for seg_b, (q0, q1) in sorted(model[b].items()):
            m = closest_params(p0, p1, q0, q1)
            if best is not None and m.dist2 > best:
                continue
            ev = build_evidence(a, seg_a, p0, p1, b, seg_b, q0, q1, m)
            if best is None or m.dist2 < best:
                best = m.dist2
                evidence = [ev]
            else:
                evidence.append(ev)
    if not evidence:
        return None, ()
    return best, finalize_evidence(evidence, mode)
