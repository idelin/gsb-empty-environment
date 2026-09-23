"""Unit tests for the change-driven LEFT OUTER JOIN view."""

from __future__ import annotations

import unittest
from collections import Counter
from pathlib import Path

from joinview import (
    Change,
    JoinChangelog,
    LeftOuterJoinView,
    SignedRow,
    freeze,
    load_fixture,
)

FIXTURE = Path(__file__).resolve().parents[1] / "fixtures" / "changes.json"


def change(txn, seq, table, pk, before=None, after=None):
    return Change(txn, table, (pk,), before, after, seq)


def counts(rows):
    return Counter(freeze(row) for row in rows)


class LeftOuterJoinViewTests(unittest.TestCase):
    def setUp(self):
        self.view = LeftOuterJoinView(right_columns=("rv",))

    def deltas(self, *changes):
        out = []
        for item in changes:
            out.extend(self.view.apply_change(item))
        return out

    def test_left_without_right_emits_null_extension(self):
        deltas = self.deltas(change("t", 0, "left", 1, after={"k": "A", "lv": 1}))
        self.assertEqual(deltas, [(1, {"l_k": "A", "l_lv": 1, "r_rv": None})])

    def test_first_right_match_retracts_null_row(self):
        self.deltas(change("t", 0, "left", 1, after={"k": "A", "lv": 1}))
        deltas = self.deltas(change("t", 0, "right", 10, after={"k": "A", "rv": 100}))
        self.assertEqual(
            deltas,
            [
                (-1, {"l_k": "A", "l_lv": 1, "r_rv": None}),
                (1, {"l_k": "A", "l_lv": 1, "r_rv": 100}),
            ],
        )

    def test_second_right_match_adds_without_null_churn(self):
        self.deltas(
            change("t", 0, "left", 1, after={"k": "A", "lv": 1}),
            change("t", 0, "right", 10, after={"k": "A", "rv": 100}),
        )
        deltas = self.deltas(change("t", 0, "right", 11, after={"k": "A", "rv": 200}))
        self.assertEqual(deltas, [(1, {"l_k": "A", "l_lv": 1, "r_rv": 200})])
        # one left row, two right rows -> materialised multiplicity is 2
        self.assertEqual(len(self.view.snapshot()), 2)
        self.assertEqual(counts(self.view.snapshot())[freeze({"l_k": "A", "l_lv": 1, "r_rv": None})], 0)

    def test_delete_one_of_two_right_rows_keeps_match_no_null(self):
        self.deltas(
            change("t", 0, "left", 1, after={"k": "A", "lv": 1}),
            change("t", 0, "right", 10, after={"k": "A", "rv": 100}),
            change("t", 0, "right", 11, after={"k": "A", "rv": 200}),
        )
        deltas = self.deltas(change("t", 0, "right", 10, before={"k": "A", "rv": 100}))
        self.assertEqual(deltas, [(-1, {"l_k": "A", "l_lv": 1, "r_rv": 100})])
        snapshot = self.view.snapshot()
        self.assertEqual(len(snapshot), 1)
        self.assertEqual(snapshot[0]["r_rv"], 200)
        self.assertNotIn(None, (row["r_rv"] for row in snapshot))

    def test_delete_last_right_row_restores_null_row(self):
        self.deltas(
            change("t", 0, "left", 1, after={"k": "A", "lv": 1}),
            change("t", 0, "right", 10, after={"k": "A", "rv": 100}),
        )
        deltas = self.deltas(change("t", 0, "right", 10, before={"k": "A", "rv": 100}))
        self.assertEqual(
            deltas,
            [
                (-1, {"l_k": "A", "l_lv": 1, "r_rv": 100}),
                (1, {"l_k": "A", "l_lv": 1, "r_rv": None}),
            ],
        )

    def test_right_update_same_key_passes_through_null_churn(self):
        self.deltas(
            change("t", 0, "left", 1, after={"k": "A", "lv": 1}),
            change("t", 0, "right", 10, after={"k": "A", "rv": 100}),
        )
        # delete-before makes this row the last match (NULL restored),
        # insert-after makes it the first match again (NULL retracted)
        deltas = self.deltas(
            change("t", 0, "right", 10,
                   before={"k": "A", "rv": 100}, after={"k": "A", "rv": 101})
        )
        null_row = {"l_k": "A", "l_lv": 1, "r_rv": None}
        self.assertEqual(
            deltas,
            [
                (-1, {"l_k": "A", "l_lv": 1, "r_rv": 100}),
                (1, null_row),
                (-1, null_row),
                (1, {"l_k": "A", "l_lv": 1, "r_rv": 101}),
            ],
        )
        # the view itself never observed the NULL row
        self.assertEqual(
            self.view.snapshot(), [{"l_k": "A", "l_lv": 1, "r_rv": 101}]
        )

    def test_right_join_key_migration(self):
        self.deltas(
            change("t", 0, "left", 1, after={"k": "A", "lv": 1}),
            change("t", 0, "left", 2, after={"k": "B", "lv": 2}),
            change("t", 0, "right", 10, after={"k": "A", "rv": 100}),
        )
        deltas = self.deltas(
            change("t", 0, "right", 10,
                   before={"k": "A", "rv": 100}, after={"k": "B", "rv": 100})
        )
        self.assertEqual(
            deltas,
            [
                (-1, {"l_k": "A", "l_lv": 1, "r_rv": 100}),  # last match leaves A
                (1, {"l_k": "A", "l_lv": 1, "r_rv": None}),
                (-1, {"l_k": "B", "l_lv": 2, "r_rv": None}),  # first match reaches B
                (1, {"l_k": "B", "l_lv": 2, "r_rv": 100}),
            ],
        )

    def test_left_delete_cascades_with_multiplicity(self):
        self.deltas(
            change("t", 0, "left", 1, after={"k": "A", "lv": 1}),
            change("t", 0, "right", 10, after={"k": "A", "rv": 100}),
            change("t", 0, "right", 11, after={"k": "A", "rv": 200}),
        )
        deltas = self.deltas(change("t", 0, "left", 1, before={"k": "A", "lv": 1}))
        self.assertEqual(
            deltas,
            [
                (-1, {"l_k": "A", "l_lv": 1, "r_rv": 100}),
                (-1, {"l_k": "A", "l_lv": 1, "r_rv": 200}),
            ],
        )
        self.assertEqual(self.view.snapshot(), [])


class TransactionCommitTests(unittest.TestCase):
    def run_changes(self, changes):
        view = LeftOuterJoinView(right_columns=("rv",))
        processor = JoinChangelog(view)
        commits = processor.ingest_all(changes)
        return view, processor, commits

    def test_left_and_right_in_same_txn_publish_final_join_only(self):
        _, _, commits = self.run_changes([
            change("T1", 1, "left", 1, after={"k": "A", "lv": 1}),
            change("T1", 1, "right", 10, after={"k": "A", "rv": 100}),
        ])
        self.assertEqual(len(commits), 1)
        self.assertEqual(
            commits[0].changes,
            (SignedRow(1, {"l_k": "A", "l_lv": 1, "r_rv": 100}),),
        )
        self.assertEqual(commits[0].retractions, ())

    def test_transient_right_insert_delete_in_txn_is_suppressed(self):
        _, processor, commits = self.run_changes([
            change("T5", 5, "left", 3, after={"k": "C", "lv": 3}),
            change("T5", 5, "right", 30, after={"k": "C", "rv": 400}),
            change("T5", 5, "right", 30, before={"k": "C", "rv": 400}),
        ])
        self.assertEqual(len(commits), 1)
        # right churn nets to zero; the brand-new left row still publishes
        self.assertEqual(
            commits[0].changes,
            (SignedRow(1, {"l_k": "C", "l_lv": 3, "r_rv": None}),),
        )
        self.assertEqual(
            processor.snapshot(), [{"l_k": "C", "l_lv": 3, "r_rv": None}]
        )

    def test_commits_replayed_in_commit_seq_order(self):
        # fixture order is shuffled; publish order must follow commit_seq
        received = []
        view = LeftOuterJoinView(right_columns=("rv",))
        processor = JoinChangelog(view)
        processor.subscribe(lambda commit: received.append(commit.txn_id))
        processor.ingest_all([
            change("T3", 3, "left", 3, after={"k": "C", "lv": 3}),
            change("T1", 1, "left", 1, after={"k": "A", "lv": 1}),
            change("T2", 2, "left", 2, after={"k": "B", "lv": 2}),
        ])
        self.assertEqual(received, ["T1", "T2", "T3"])

    def test_mixed_commit_seq_within_txn_rejected(self):
        view = LeftOuterJoinView(right_columns=("rv",))
        processor = JoinChangelog(view)
        with self.assertRaises(ValueError):
            processor.ingest_all([
                change("T1", 1, "left", 1, after={"k": "A", "lv": 1}),
                change("T1", 2, "left", 2, after={"k": "B", "lv": 2}),
            ])


class FixtureEndToEndTests(unittest.TestCase):
    def test_fixture_changelog_and_final_snapshot(self):
        join_cfg, changes = load_fixture(FIXTURE)
        view = LeftOuterJoinView(right_columns=join_cfg["right_columns"])
        processor = JoinChangelog(view)
        commits = processor.ingest_all(changes)

        by_id = {commit.txn_id: commit for commit in commits}
        # T1: left + its right match arrive together -> one final joined row
        self.assertEqual(
            [(d.sign, d.row["r_rv"]) for d in by_id["T1"].changes], [(1, 100)]
        )
        # T2: second right match -> a single addition, no NULL churn
        self.assertEqual(
            [(d.sign, d.row["r_rv"]) for d in by_id["T2"].changes], [(1, 200)]
        )
        # T3: one of two matches leaves -> single retraction, no NULL row
        self.assertEqual(
            [(d.sign, d.row["r_rv"]) for d in by_id["T3"].changes], [(-1, 100)]
        )
        # T4: left + right in one txn -> final consistent join published
        self.assertEqual(
            [(d.sign, d.row["l_k"]) for d in by_id["T4"].changes], [(1, "B")]
        )
        # T5: matching right inserted and deleted inside the txn -> its churn
        # nets to zero, but the new unmatched left row still publishes a NULL row
        self.assertEqual(
            by_id["T5"].changes,
            (SignedRow(1, {"l_k": "C", "l_lv": 3, "r_rv": None}),),
        )
        # T6: last match leaves -> retraction plus restored NULL extension
        self.assertEqual(
            [(d.sign, d.row["r_rv"]) for d in by_id["T6"].changes],
            [(-1, 200), (1, None)],
        )

        self.assertEqual(
            processor.snapshot(),
            [
                {"l_k": "A", "l_lv": 1, "r_rv": None},
                {"l_k": "B", "l_lv": 2, "r_rv": 300},
                {"l_k": "C", "l_lv": 3, "r_rv": None},
            ],
        )


if __name__ == "__main__":
    unittest.main(verbosity=2)
