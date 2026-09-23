import json
import unittest
from pathlib import Path

from revisioned_aggregate import (
    Aggregator, Revision, RevisionStore, load_change_fixture, recompute, verify,
)


def published(fixture):
    store = RevisionStore(":memory:")
    agg = Aggregator(store)
    return agg.ingest(load_change_fixture(fixture))


class VerifierTest(unittest.TestCase):
    def test_recompute_matches_published_log(self):
        events = load_change_fixture("fixtures/changes_basic.json")
        report = verify(published("fixtures/changes_basic.json"), events)
        self.assertTrue(report.ok, report.problems)
        self.assertEqual(report.expected, {"txn:A": 150, "txn:B": 0})

    def test_retraction_recomputed_correctly(self):
        events = load_change_fixture("fixtures/changes_retract.json")
        self.assertEqual(recompute(events), {"txn:A": 45})
        report = verify(published("fixtures/changes_retract.json"), events)
        self.assertTrue(report.ok, report.problems)

    def test_cycle_is_rejected(self):
        data = json.loads(Path("fixtures/revision_log_cycle.json").read_text())
        revisions = [Revision.from_dict(d) for d in data["revisions"]]
        report = verify(revisions, [])
        self.assertFalse(report.ok)
        self.assertTrue(any("cycle" in p for p in report.problems))

    def test_out_of_order_commit_seq_flagged(self):
        r1 = Revision("r1", "t1", {"txn:A": None}, {"txn:A": 10}, 5, 6)
        r2 = Revision("r2", "t2", {"txn:A": "r1"}, {"txn:A": 1}, 2, 3)
        report = verify([r1, r2], [])
        self.assertFalse(report.ok)
        self.assertTrue(any("out-of-order" in p for p in report.problems))

    def test_stale_base_detected(self):
        r1 = Revision("r1", "t1", {"txn:A": None}, {"txn:A": 10}, 1, 1)
        r2 = Revision("r2", "t2", {"txn:A": "r1"}, {"txn:A": 5}, 2, 2)
        r3 = Revision("r3", "t3", {"txn:A": "r1"}, {"txn:A": 7}, 3, 3)  # skips r2
        report = verify([r1, r2, r3], [])
        self.assertFalse(report.ok)
        self.assertTrue(any("stale base" in p for p in report.problems))

    def test_value_mismatch_detected(self):
        events = load_change_fixture("fixtures/changes_basic.json")
        revisions = published("fixtures/changes_basic.json")
        tampered = revisions[:-1] + [
            Revision("rev-t4", "t4", revisions[-1].bases, {"txn:A": 51, "txn:B": -50}, 5, 5)
        ]
        report = verify(tampered, events)
        self.assertFalse(report.ok)
        self.assertTrue(any("value mismatch" in p for p in report.problems))

    def test_duplicate_revision_id_rejected(self):
        r1 = Revision("r1", "t1", {"txn:A": None}, {"txn:A": 10}, 1, 1)
        report = verify([r1, r1], [])
        self.assertFalse(report.ok)
        self.assertTrue(any("duplicate" in p for p in report.problems))


if __name__ == "__main__":
    unittest.main()
