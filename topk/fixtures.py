"""Local change fixtures: no message platform, just JSON files.

Every event carries: txn_id, table, pk, before, after, commit_seq.
"""
import json

from .keys import field

DEFAULT_SPEC = (field("score", desc=True, nulls_first=False),
                field("ts", desc=False, nulls_first=True))
DEFAULT_K = 5
GROUP_FIELD = "grp"


def _rec(grp, score, ts):
    return {"grp": grp, "score": score, "ts": ts}


def generate_events():
    """Deterministic scenario: skew, ties, nulls, updates, migration, deletes."""
    events = []
    seq = 0

    def commit(table, changes):
        nonlocal seq
        seq += 1
        txn = "txn-%03d" % seq
        for pk, before, after in changes:
            events.append({"txn_id": txn, "table": table, "pk": pk,
                           "before": before, "after": after,
                           "commit_seq": seq})

    # c1: bulk load a hot group (skewed vs the others)
    commit("metrics", [(i, None, _rec("hot", 1000 - i, i)) for i in range(1, 41)])
    # c2: a small cold group
    commit("metrics", [(100 + i, None, _rec("cold", 50 + i, 10 + i))
                       for i in range(4)])
    # c3: a null-heavy group
    commit("metrics", [(200, None, _rec("nil", None, 1)),
                       (201, None, _rec("nil", 5, None)),
                       (202, None, _rec("nil", None, 2)),
                       (203, None, _rec("nil", 7, 3))])
    # c4: a group where every row ties on the sort key
    commit("metrics", [(300 + i, None, _rec("tie", 50, 1)) for i in range(8)])
    # c5: sort-key update must not duplicate pk 1
    commit("metrics", [(1, _rec("hot", 999, 1), _rec("hot", 10, 1))])
    # c6: group migration pk 2 hot -> cold (leaves the old group)
    commit("metrics", [(2, _rec("hot", 998, 2), _rec("cold", 998, 2))])
    # c7: delete the current rank-1 of hot (pk 3, score 997)
    commit("metrics", [(3, _rec("hot", 997, 3), None)])
    # c8: delete two cold rows
    commit("metrics", [(100, _rec("cold", 50, 10), None),
                       (101, _rec("cold", 51, 11), None)])
    # c9: null -> value update
    commit("metrics", [(200, _rec("nil", None, 1), _rec("nil", 88, 1))])
    # c10: a table with no registered engine (must be skipped)
    commit("archive", [(1, None, {"grp": "x", "score": 1, "ts": 1})])
    return events


def write_fixture(path, events=None):
    if events is None:
        events = generate_events()
    with open(path, "w") as fh:
        json.dump(events, fh, indent=1, sort_keys=True)
    return path


def load_fixture(path):
    with open(path) as fh:
        return json.load(fh)
