"""Interval reuse and conservative root-identity matching after a polynomial
parameter revision.

Identity policy (important): a root identity is NEVER guessed from
interval-center distances, root magnitudes or parameter step sizes.  An
old/new root pair is linked only when exact, certified Sturm counts support
the link:

  * the two intervals intersect and the intersection certifiedly contains
    at least one old-radical root and one new-radical root, or
  * one interval is nested inside the other: the inner interval's own
    certificate (count == 1) plus the outer interval's certificate then
    certify that a root region of one revision lies inside a root region
    of the other.

Consequences, by construction:
  * a repeated root that SPLITS into several roots shows up as one
    "split" group (1 old, k new) inside the reused old interval -- the
    individual new roots are reported as a group, never as k invented
    one-to-one identities;
  * roots that MERGE show up as a "merge" group (k old, 1 new);
  * anything not decided by certificates stays "many_to_many" / unmatched
    instead of being guessed.

Interval reuse: every old isolation interval is re-used as a query region
for the NEW polynomial's radical, yielding a fresh certified root count per
old interval (ReusedInterval) without re-running bisection there.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from .certificates import RootCountCertificate
from .intervals import Interval
from .isolate import IsolationResult, count_with_cert, isolate
from .poly import Poly, degree
from .sturm import sturm_chain


@dataclass
class ReusedInterval:
    """An old isolation interval re-used as a certified count region for the
    revised polynomial."""

    interval: Interval
    old_root_ids: tuple
    new_count: int
    certificate: RootCountCertificate


@dataclass
class MatchEdge:
    old_id: int
    new_id: int
    certified_unique: bool  # both intersection counts equal 1
    evidence: str


@dataclass
class RootGroup:
    """A connected component of the certified old<->new link graph."""

    kind: str  # one_to_one | split | merge | many_to_many | old_only | new_only
    old_ids: tuple
    new_ids: tuple
    note: str


@dataclass
class RevisionReport:
    old_result: IsolationResult
    new_result: IsolationResult
    reused: list
    edges: list
    groups: list
    notes: list = field(default_factory=list)


def _classify(n_old: int, n_new: int) -> str:
    if n_old == 1 and n_new == 1:
        return "one_to_one"
    if n_old == 1:
        return "split"
    if n_new == 1:
        return "merge"
    return "many_to_many"


def compute_edges(old_result: IsolationResult, new_result: IsolationResult):
    """Certified containment edges between old and new isolated roots."""
    if not old_result.isolated or not new_result.isolated:
        return []
    old_chain = sturm_chain(old_result.radical)
    new_chain = sturm_chain(new_result.radical)
    edges = []
    for old_root in old_result.isolated:
        for new_root in new_result.isolated:
            inter = old_root.interval.intersect(new_root.interval)
            if inter is None:
                continue
            nested = old_root.interval.contains_interval(
                new_root.interval
            ) or new_root.interval.contains_interval(old_root.interval)
            new_in, _ = count_with_cert(new_chain, inter)
            old_in, _ = count_with_cert(old_chain, inter)
            if (new_in >= 1 and old_in >= 1) or nested:
                unique = new_in == 1 and old_in == 1
                evidence = (
                    f"intersection {inter}: certified old_roots={old_in}, "
                    f"new_roots={new_in}"
                )
                if old_root.is_point and new_root.is_point:
                    evidence += "; exact rational root identical"
                edges.append(
                    MatchEdge(
                        old_root.isolation_id,
                        new_root.isolation_id,
                        unique,
                        evidence,
                    )
                )
    return edges


def build_groups(old_result: IsolationResult, new_result: IsolationResult,
                 edges):
    """Connected components of the edge graph, classified conservatively."""
    parent = {}

    def find(x):
        parent.setdefault(x, x)
        while parent[x] != x:
            parent[x] = parent[parent[x]]
            x = parent[x]
        return x

    def union(a, b):
        ra, rb = find(a), find(b)
        if ra != rb:
            parent[rb] = ra

    for e in edges:
        union(("old", e.old_id), ("new", e.new_id))

    comps = {}
    for e in edges:
        root = find(("old", e.old_id))
        comps.setdefault(root, {"old": set(), "new": set()})
        comps[root]["old"].add(e.old_id)
        comps[root]["new"].add(e.new_id)

    linked_old = set().union(*(c["old"] for c in comps.values())) if comps else set()
    linked_new = set().union(*(c["new"] for c in comps.values())) if comps else set()

    groups = []
    for comp in comps.values():
        olds = tuple(sorted(comp["old"]))
        news = tuple(sorted(comp["new"]))
        kind = _classify(len(olds), len(news))
        if kind == "one_to_one":
            note = "certified unique counts in the intersection"
        elif kind == "split":
            note = ("one old root region certifiedly contains several new "
                    "roots; individual identities are NOT assigned")
        elif kind == "merge":
            note = ("several old root regions certifiedly lie inside one "
                    "new root interval; individual identities are NOT "
                    "assigned")
        else:
            note = "certificates do not determine a unique correspondence"
        groups.append(RootGroup(kind, olds, news, note))

    for old_root in old_result.isolated:
        if old_root.isolation_id not in linked_old:
            groups.append(
                RootGroup("old_only", (old_root.isolation_id,), (),
                          "no certified new root in the old interval")
            )
    for new_root in new_result.isolated:
        if new_root.isolation_id not in linked_new:
            groups.append(
                RootGroup("new_only", (), (new_root.isolation_id,),
                          "no certified old root linked to this interval")
            )
    return groups


def revise_polynomial(old_result: IsolationResult, new_f: Poly,
                      budget=None) -> RevisionReport:
    """Full revision workflow:

    1. isolate the revised polynomial (budget-bounded, resumable);
    2. RE-USE every old isolation interval as a certified count region for
       the new radical (ReusedInterval evidence);
    3. link old/new roots by certified containment only (never by distance).
    """
    new_result = isolate(new_f, budget=budget)
    notes = []
    reused = []
    if old_result.isolated and degree(new_result.radical) >= 1:
        new_chain = sturm_chain(new_result.radical)
        for old_root in old_result.isolated:
            count, cert = count_with_cert(new_chain, old_root.interval)
            reused.append(
                ReusedInterval(
                    old_root.interval,
                    (old_root.isolation_id,),
                    count,
                    cert,
                )
            )
    if old_result.pending:
        notes.append(
            f"old isolation had {len(old_result.pending)} pending "
            f"interval(s); roots inside them are not matched"
        )
    if new_result.pending:
        notes.append(
            f"new isolation has {len(new_result.pending)} unresolved "
            f"interval(s) within the budget; resume to refine"
        )
    edges = compute_edges(old_result, new_result)
    groups = build_groups(old_result, new_result, edges)
    return RevisionReport(old_result, new_result, reused, edges, groups,
                          notes)
