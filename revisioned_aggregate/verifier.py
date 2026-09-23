"""Independent verifier.

Recomputes the aggregate from the raw change events alone and checks a
published revision log against it. Rejects: duplicate revision ids,
unknown bases, out-of-order commit sequences, stale bases (old-version
corrections), cycles in the version DAG, and value mismatches (including
mishandled retractions).
"""

from __future__ import annotations

from collections import defaultdict, deque
from dataclasses import dataclass, field

from .aggregator import event_deltas
from .model import Revision


def recompute(events, group_field: str = "account") -> dict:
    """Ground truth: fold raw events in commit order, from scratch."""
    totals = defaultdict(int)
    for ev in sorted(events, key=lambda e: e.commit_seq):
        for key, delta in event_deltas(ev, group_field).items():
            totals[key] += delta
    return dict(totals)


@dataclass
class VerificationReport:
    ok: bool
    problems: list = field(default_factory=list)
    expected: dict = field(default_factory=dict)
    actual: dict = field(default_factory=dict)

    def __str__(self) -> str:
        if self.ok:
            return f"OK: {len(self.expected)} key(s) verified"
        return "FAILED:\n" + "\n".join(f"  - {p}" for p in self.problems)


def verify(revisions: list, events, group_field: str = "account") -> VerificationReport:
    problems = []
    ids = [r.id for r in revisions]
    for rid in sorted({i for i in ids if ids.count(i) > 1}):
        problems.append(f"duplicate revision id: {rid}")
    by_id = {r.id: r for r in revisions}

    for rev in revisions:
        for parent in rev.parents:
            if parent not in by_id:
                problems.append(f"{rev.id}: unknown base version {parent}")

    for prev, cur in zip(revisions, revisions[1:]):
        if cur.seq_lo < prev.seq_hi:
            problems.append(
                f"out-of-order commit seq: {cur.id} covers "
                f"{cur.seq_lo}-{cur.seq_hi} after {prev.id} covers "
                f"{prev.seq_lo}-{prev.seq_hi}")

    topo = _topo_sort(revisions, by_id)
    if topo is None:
        leftover = sorted(r.id for r in revisions if r.id in by_id)
        problems.append("cycle detected in version DAG; log rejected")
        return VerificationReport(False, problems, {}, {})

    head = {}
    actual = defaultdict(int)
    for rid in topo:
        rev = by_id[rid]
        for key, delta in rev.deltas.items():
            declared = rev.bases.get(key)
            if declared != head.get(key):
                problems.append(
                    f"{rev.id}: stale base for {key}: declares {declared}, "
                    f"current version is {head.get(key)}")
            actual[key] += delta
            head[key] = rid

    expected = recompute(events, group_field)
    for key in sorted(set(expected) | set(actual)):
        if expected.get(key, 0) != actual.get(key, 0):
            problems.append(
                f"value mismatch on {key}: recomputed {expected.get(key, 0)} "
                f"!= revision log {actual.get(key, 0)}")
    return VerificationReport(not problems, problems, expected, dict(actual))


def _topo_sort(revisions: list, by_id: dict):
    """Kahn's algorithm; returns None when the version graph has a cycle."""
    indegree = {r.id: 0 for r in revisions}
    children = defaultdict(list)
    for rev in revisions:
        for parent in rev.parents:
            if parent in by_id:
                children[parent].append(rev.id)
                indegree[rev.id] += 1
    queue = deque(sorted(rid for rid, deg in indegree.items() if deg == 0))
    order = []
    while queue:
        node = queue.popleft()
        order.append(node)
        for child in sorted(children[node]):
            indegree[child] -= 1
            if indegree[child] == 0:
                queue.append(child)
    return order if len(order) == len(revisions) else None
