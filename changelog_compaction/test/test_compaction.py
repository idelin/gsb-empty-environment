import itertools
import unittest

from changelog_compaction.certificate import (
    assert_inequivalent,
    build_certificate,
    verify_certificate,
)
from changelog_compaction.changelog import (
    Change,
    load_changes,
    op_of,
    replay,
)
from changelog_compaction.compaction import compact
from changelog_compaction.fixtures import get_fixture


def _chain(seq, table="t", key="k", txn="x", commit=1):
    rows = []
    current = None
    for i, value in enumerate(seq):
        rows.append(
            {
                "idx": i,
                "txn_id": txn,
                "table": table,
                "key": key,
                "before": current,
                "after": value,
                "commit_seq": commit,
            }
        )
        current = value
    return rows


def _valid_paths(values, length):
    """All legal chains: states are ABSENT or a value; adjacent states differ."""
    states = [None] + list(values)
    paths = []

    def walk(path):
        if len(path) == length:
            paths.append(path)
            return
        for state in states:
            if path and state == path[-1]:
                continue
            walk(path + [state])

    walk([])
    return [
        path
        for path in paths
        if all(a != b for a, b in zip(path, path[1:]))
    ]


def _rows_from_path(path):
    rows = []
    states = [None] + path
    for idx, (before, after) in enumerate(zip(states, states[1:])):
        if before is None and after is None:
            continue
        rows.append(
            {
                "idx": idx,
                "txn_id": "x",
                "table": "t",
                "key": "k",
                "before": before,
                "after": after,
                "commit_seq": 1,
            }
        )
    return rows


class ExhaustiveSmallSequences(unittest.TestCase):
    def test_all_valid_chains_length_1_to_4(self):
        values = ["A", "B", "C"]
        checked = 0
        for length in range(1, 5):
            for path in _valid_paths(values, length):
                raw = _rows_from_path(path)
                for audit in (False, True):
                    changes = load_changes(raw)
                    result = compact(changes, audit=audit)
                    raw_views = replay(changes, [1])
                    compacted_views = replay(result.changes, [1])
                    self.assertEqual(
                        raw_views,
                        compacted_views,
                        msg=f"path={path} audit={audit}",
                    )
                    cert = build_certificate(raw, audit=audit)
                    ok, problems = verify_certificate(cert)
                    self.assertTrue(ok, msg=f"{path}: {problems}")
                checked += 1
        self.assertEqual(
            checked,
            sum(len(_valid_paths(values, n)) for n in range(1, 5)),
        )

    def test_delete_recreate_never_forged_as_update(self):
        values = ["A", "B"]
        for path in _valid_paths(values, 4):
            raw = _rows_from_path(path)
            changes = load_changes(raw)
            result = compact(changes)
            raw_by_idx = {row["idx"]: row for row in raw}
            for elision in result.elisions:
                if elision.kind in ("MERGE_UPDATE_CHAIN", "MERGE_INSERT_CHAIN",
                                    "MERGE_UPDATE_DELETE"):
                    chain_rows = [raw_by_idx[i] for i in elision.sources]
                    intermediate = [row["after"] for row in chain_rows[:-1]]
                    self.assertNotIn(
                        None,
                        intermediate,
                        msg=f"fused record hides ABSENT: {path}",
                    )
            for change in result.changes:
                if change.before is not None and change.after is not None:
                    self.assertFalse(change.idx < 0 and _saw_absent(raw, change, result),
                                     msg=f"forged update: {path}")
            raw_views = replay(changes, [1])
            compacted_views = replay(result.changes, [1])
            self.assertEqual(raw_views, compacted_views)

    def test_abc_merge_rule(self):
        result = compact(load_changes(get_fixture("abc_merge")))
        self.assertEqual(len(result.changes), 1)
        fused = result.changes[0]
        self.assertIsNone(fused.before)
        self.assertEqual(fused.after, "C")
        self.assertEqual(result.merges_used, 2)
        self.assertEqual(result.elisions[0].sources, [0, 1, 2])

    def test_insert_delete_cancel_and_audit_evidence(self):
        raw = get_fixture("insert_delete_cancel")
        normal = compact(load_changes(raw), audit=False)
        self.assertEqual(normal.changes, [])
        self.assertEqual(normal.elisions[0].kind, "INSERT_DELETE_CANCEL")
        self.assertIsNone(normal.elisions[0].evidence)

        audited = compact(load_changes(raw), audit=True)
        self.assertEqual(audited.changes, [])
        self.assertIsNotNone(audited.elisions[0].evidence)
        self.assertEqual(
            [entry["op"] for entry in audited.elisions[0].evidence],
            ["INSERT", "DELETE"],
        )
        cert = build_certificate(raw, audit=True)
        ok, _ = verify_certificate(cert)
        self.assertTrue(ok)

def _saw_absent(raw, fused, result):
    """True if the provenance behind a fused present->present record had a
    delete/recreate in it."""
    by_key = None
    for elision in result.elisions:
        if (elision.commit_seq == fused.commit_seq
                and elision.table == fused.table
                and elision.key == fused.key):
            rows = [row for row in raw if row["idx"] in elision.sources]
            if any(row["after"] is None for row in rows[:-1]):
                return True
    return False


class BarrierTests(unittest.TestCase):
    def test_cross_commit_is_not_merged(self):
        raw = get_fixture("cross_commit_visible")
        result = compact(load_changes(raw))
        self.assertEqual(len(result.changes), 3)
        self.assertTrue(all(change.idx >= 0 for change in result.changes))
        cert = build_certificate(raw)
        ok, _ = verify_certificate(cert)
        self.assertTrue(ok)

    def test_cross_schema_is_not_merged(self):
        raw = get_fixture("schema_barrier")
        result = compact(load_changes(raw))
        tables = {change.table for change in result.changes}
        self.assertEqual(tables, {"table_a", "table_b"})
        self.assertEqual(len(result.changes), 2)

    def test_cross_txn_barrier_rejected(self):
        raw = [
            {"idx": 0, "txn_id": "a", "table": "t", "key": "k",
             "before": None, "after": 1, "commit_seq": 1},
            {"idx": 1, "txn_id": "b", "table": "t", "key": "k",
             "before": 1, "after": 2, "commit_seq": 1},
        ]
        with self.assertRaises(ValueError):
            compact(load_changes(raw))


class CounterExampleTests(unittest.TestCase):
    def test_hidden_intermediate_version_is_inequivalent(self):
        raw = get_fixture("cross_commit_visible")
        illegal = [
            {"idx": 0, "txn_id": "t4", "table": "ledger", "key": "k4",
             "before": None, "after": "C", "commit_seq": 1},
        ]
        reasons = assert_inequivalent(raw, illegal)
        self.assertTrue(any("commit 2" in r for r in reasons))
        raw_views = replay(load_changes(raw), [1, 2, 3])
        bad_views = replay(load_changes(illegal), [1, 2, 3])
        # Final value at commit 3 matches; the earlier visible point does not.
        self.assertEqual(raw_views[3]["state"], bad_views[3]["state"])
        self.assertNotEqual(raw_views[2]["state"], bad_views[2]["state"])

    def test_delete_recreate_vs_update_identity_counterexample(self):
        raw = get_fixture("delete_recreate_identity")
        forged = [
            {"idx": 0, "txn_id": "t3a", "table": "users", "key": "k3",
             "before": None, "after": "A", "commit_seq": 1},
            {"idx": 1, "txn_id": "t3b", "table": "users", "key": "k3",
             "before": "A", "after": "C", "commit_seq": 2},
        ]
        reasons = assert_inequivalent(raw, forged)
        self.assertTrue(any("identity" in r or "aggregate" in r for r in reasons))
        raw_views = replay(load_changes(raw), [1, 2])
        forged_views = replay(load_changes(forged), [1, 2])
        # Same final value ...
        self.assertEqual(
            raw_views[2]["state"]["users"]["\"k3\""]["value"],
            forged_views[2]["state"]["users"]["\"k3\""]["value"],
        )
        # ... different identity generation and different churn aggregates.
        self.assertNotEqual(
            raw_views[2]["state"]["users"]["\"k3\""]["identity"],
            forged_views[2]["state"]["users"]["\"k3\""]["identity"],
        )
        self.assertNotEqual(
            raw_views[2]["aggregate"]["users"],
            forged_views[2]["aggregate"]["users"],
        )

    def test_correct_compaction_splits_delete_recreate(self):
        raw = get_fixture("delete_recreate_identity")
        result = compact(load_changes(raw))
        ops = [(change.op, change.before, change.after) for change in result.changes]
        self.assertEqual(
            ops,
            [
                ("INSERT", None, "A"),
                ("DELETE", "A", None),
                ("INSERT", None, "C"),
            ],
        )


class BeforeValueValidationTests(unittest.TestCase):
    def test_intra_commit_delete_recreate_bumps_identity(self):
        # Existing row deleted and re-created inside one commit gets a fresh
        # identity generation and delete+insert downstream aggregate effects.
        raw = [
            {"idx": 0, "txn_id": "t0", "table": "users", "key": "k",
             "before": None, "after": "A", "commit_seq": 1},
            {"idx": 1, "txn_id": "t1", "table": "users", "key": "k",
             "before": "A", "after": None, "commit_seq": 2},
            {"idx": 2, "txn_id": "t1", "table": "users", "key": "k",
             "before": None, "after": "C", "commit_seq": 2},
        ]
        result = compact(load_changes(raw))
        views = replay(result.changes, [1, 2])
        self.assertEqual(
            views[2]["state"]["users"]["\"k\""]["identity"],
            "users:\"k\"#g2",
        )
        self.assertEqual(views[2]["aggregate"]["users"]["inserts"], 2)
        self.assertEqual(views[2]["aggregate"]["users"]["deletes"], 1)
        self.assertEqual(views, replay(load_changes(raw), [1, 2]))

    def test_broken_chain_rejected(self):
        raw = [
            {"idx": 0, "txn_id": "a", "table": "t", "key": "k",
             "before": None, "after": "A", "commit_seq": 1},
            {"idx": 1, "txn_id": "a", "table": "t", "key": "k",
             "before": "Z", "after": "B", "commit_seq": 1},
        ]
        with self.assertRaises(ValueError):
            compact(load_changes(raw))

    def test_replay_detects_bad_before_across_commits(self):
        raw = [
            {"idx": 0, "txn_id": "a", "table": "t", "key": "k",
             "before": None, "after": "A", "commit_seq": 1},
            {"idx": 1, "txn_id": "b", "table": "t", "key": "k",
             "before": "Z", "after": "B", "commit_seq": 2},
        ]
        with self.assertRaises(ValueError):
            replay(load_changes(raw), [1, 2])


class BudgetTests(unittest.TestCase):
    def test_budget_zero_keeps_everything(self):
        raw = get_fixture("abc_merge")
        result = compact(load_changes(raw), budget=0)
        self.assertEqual(len(result.changes), 3)
        self.assertTrue(result.budget_exhausted)

    def test_budget_partial_keeps_uncompacted_fragments(self):
        raw = get_fixture("mixed")
        result = compact(load_changes(raw), budget=1)
        self.assertTrue(result.budget_exhausted)
        raw_views = replay(load_changes(raw), [1, 2])
        compacted_views = replay(result.changes, [1, 2])
        self.assertEqual(raw_views, compacted_views)

    def test_budget_allows_full_compaction_when_sufficient(self):
        raw = get_fixture("abc_merge")
        result = compact(load_changes(raw), budget=10)
        self.assertFalse(result.budget_exhausted)
        self.assertEqual(len(result.changes), 1)


class CertificateTests(unittest.TestCase):
    def test_tampered_view_is_rejected(self):
        cert = build_certificate(get_fixture("mixed"), audit=True)
        cert["declared_views"][0]["state"]["orders"]["\"k7\""]["value"] = 999
        ok, problems = verify_certificate(cert)
        self.assertFalse(ok)
        self.assertTrue(
            any("digest" in p or "views disagree" in p for p in problems)
        )

    def test_cross_commit_fused_record_is_rejected(self):
        cert = build_certificate(get_fixture("cross_commit_visible"))
        cert["compacted_changes"] = [
            {
                "idx": -1,
                "txn_id": "t4",
                "table": "ledger",
                "key": "k4",
                "before": None,
                "after": "C",
                "commit_seq": 1,
                "op": "INSERT",
                "fused": True,
            }
        ]
        cert["elisions"] = [
            {
                "kind": "MERGE_INSERT_CHAIN",
                "table": "ledger",
                "key": "k4",
                "commit_seq": 1,
                "txn_id": "t4",
                "sources": [0, 1, 2],
            }
        ]
        ok, problems = verify_certificate(cert)
        self.assertFalse(ok)
        self.assertTrue(
            any("commit visibility barrier" in p for p in problems), problems
        )

    def test_mixed_fixture_equivalent_at_every_commit_point(self):
        cert = build_certificate(get_fixture("mixed"), audit=True)
        ok, problems = verify_certificate(cert)
        self.assertTrue(ok, problems)
        self.assertEqual(cert["commit_points"], [1, 2])


if __name__ == "__main__":
    unittest.main(verbosity=2)
