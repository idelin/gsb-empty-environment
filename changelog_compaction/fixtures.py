"""Local change fixtures: txn_id, table, key, before/after, commit order."""

from __future__ import annotations

# Each entry is one raw change from a local fixture (no messaging platform).
# None means ABSENT. idx is the global intra-feed ordering.

FIXTURES: dict[str, list[dict]] = {
    "abc_merge": [
        {"idx": 0, "txn_id": "t1", "table": "orders", "key": "k1",
         "before": None, "after": "A", "commit_seq": 1},
        {"idx": 1, "txn_id": "t1", "table": "orders", "key": "k1",
         "before": "A", "after": "B", "commit_seq": 1},
        {"idx": 2, "txn_id": "t1", "table": "orders", "key": "k1",
         "before": "B", "after": "C", "commit_seq": 1},
    ],
    "insert_delete_cancel": [
        {"idx": 0, "txn_id": "t2", "table": "cart", "key": "k2",
         "before": None, "after": "x", "commit_seq": 1},
        {"idx": 1, "txn_id": "t2", "table": "cart", "key": "k2",
         "before": "x", "after": None, "commit_seq": 1},
    ],
    "delete_recreate_identity": [
        {"idx": 0, "txn_id": "t3a", "table": "users", "key": "k3",
         "before": None, "after": "A", "commit_seq": 1},
        {"idx": 1, "txn_id": "t3b", "table": "users", "key": "k3",
         "before": "A", "after": None, "commit_seq": 2},
        {"idx": 2, "txn_id": "t3b", "table": "users", "key": "k3",
         "before": None, "after": "C", "commit_seq": 2},
    ],
    "cross_commit_visible": [
        {"idx": 0, "txn_id": "t4", "table": "ledger", "key": "k4",
         "before": None, "after": "A", "commit_seq": 1},
        {"idx": 1, "txn_id": "t5", "table": "ledger", "key": "k4",
         "before": "A", "after": "B", "commit_seq": 2},
        {"idx": 2, "txn_id": "t6", "table": "ledger", "key": "k4",
         "before": "B", "after": "C", "commit_seq": 3},
    ],
    "schema_barrier": [
        {"idx": 0, "txn_id": "t7", "table": "table_a", "key": "k5",
         "before": None, "after": 1, "commit_seq": 1},
        {"idx": 1, "txn_id": "t7", "table": "table_b", "key": "k5",
         "before": None, "after": 2, "commit_seq": 1},
    ],
    "mixed": [
        {"idx": 0, "txn_id": "t8", "table": "orders", "key": "k6",
         "before": None, "after": 10, "commit_seq": 1},
        {"idx": 1, "txn_id": "t8", "table": "orders", "key": "k6",
         "before": 10, "after": 20, "commit_seq": 1},
        {"idx": 2, "txn_id": "t8", "table": "orders", "key": "k6",
         "before": 20, "after": 30, "commit_seq": 1},
        {"idx": 3, "txn_id": "t8", "table": "orders", "key": "k7",
         "before": None, "after": 5, "commit_seq": 1},
        {"idx": 4, "txn_id": "t9", "table": "orders", "key": "k6",
         "before": 30, "after": None, "commit_seq": 2},
        {"idx": 5, "txn_id": "t9", "table": "orders", "key": "k7",
         "before": 5, "after": 9, "commit_seq": 2},
    ],
}


def fixture_names() -> list[str]:
    return sorted(FIXTURES)


def get_fixture(name: str) -> list[dict]:
    return [dict(row) for row in FIXTURES[name]]
