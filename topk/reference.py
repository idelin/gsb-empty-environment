"""Independent full-sort reference used to verify the incremental engine.

This module deliberately re-implements ordering with its own comparator
(no shared key encoding) and always sorts the *entire* group, so it serves
as a ground-truth oracle for hot-group skew, tiny-memory spilling,
recovery, and last-rank tie scenarios.
"""
import functools


def _cmp(a, b):
    return (a > b) - (a < b)


def make_record_comparator(spec):
    """Independent comparator: null placement, direction, id tie-break."""

    def compare(x, y):  # x, y are (pk, record)
        for name, desc, nulls_first in spec:
            vx, vy = x[1].get(name), y[1].get(name)
            if vx is None and vy is None:
                c = 0
            elif vx is None:
                c = -1 if nulls_first else 1
            elif vy is None:
                c = 1 if nulls_first else -1
            else:
                c = _cmp(vx, vy)
                if desc:
                    c = -c
            if c:
                return c
        return _cmp(x[0], y[0])  # stable id tie-break

    return compare


def expected_tops(records, group_field, spec, k):
    """Full-sort every group and return {group: [(pk, values), ...][:k]}."""
    groups = {}
    for pk, rec in records.items():
        groups.setdefault(rec[group_field], []).append((pk, rec))
    comparator = make_record_comparator(spec)
    out = {}
    for group, items in groups.items():
        items.sort(key=functools.cmp_to_key(comparator))
        out[group] = [
            (pk, tuple(rec.get(n) for n, _, _ in spec)) for pk, rec in items[:k]
        ]
    return out


def replay_events(events):
    """Fold change events (ordered by commit_seq) into final record state."""
    tables = {}
    for ev in sorted(events, key=lambda e: e["commit_seq"]):
        recs = tables.setdefault(ev["table"], {})
        if ev.get("before") is not None or ev["pk"] in recs:
            recs.pop(ev["pk"], None)
        if ev.get("after") is not None:
            recs[ev["pk"]] = ev["after"]
    return tables


def verify_engine(engine, records):
    """Compare engine tops against a full-sort oracle; returns mismatches."""
    expected = expected_tops(records, engine.group_field, engine.sort_spec, engine.k)
    problems = []
    engine_groups = {g for g, t in engine._top.items() if t}
    for group in sorted(set(expected) | engine_groups, key=repr):
        got = [list(t) for t in engine._top.get(group, ())]
        want = [list(t) for t in expected.get(group, [])]
        if got != want:
            problems.append((group, want, got))
    return problems
