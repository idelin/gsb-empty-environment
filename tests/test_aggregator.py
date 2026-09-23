import unittest

from revisioned_aggregate import Aggregator, RevisionStore, load_change_fixture


class AggregatorTest(unittest.TestCase):
    def setUp(self):
        self.store = RevisionStore(":memory:")
        self.agg = Aggregator(self.store)

    def test_basic_aggregation_with_corrections(self):
        published = self.agg.ingest(load_change_fixture("fixtures/changes_basic.json"))
        self.assertEqual([r.id for r in published],
                         ["rev-t1", "rev-t2", "rev-t3", "rev-t4"])
        self.assertEqual(self.agg.values, {"txn:A": 150, "txn:B": 0})
        self.assertEqual(self.store.checkpoint(), (5, "rev-t4"))

    def test_correction_carries_base_version_and_delta(self):
        self.agg.ingest(load_change_fixture("fixtures/changes_basic.json"))
        rev = self.store.get_revision("rev-t3")
        self.assertEqual(rev.bases, {"txn:A": "rev-t2"})
        self.assertEqual(rev.deltas, {"txn:A": -20})

    def test_multi_key_revision_forms_dag(self):
        self.agg.ingest(load_change_fixture("fixtures/changes_basic.json"))
        rev = self.store.get_revision("rev-t4")
        self.assertEqual(rev.parents, frozenset({"rev-t1", "rev-t3"}))

    def test_retract_then_readd_order(self):
        published = self.agg.ingest(load_change_fixture("fixtures/changes_retract.json"))
        self.assertEqual([r.deltas for r in published],
                         [{"txn:A": 30}, {"txn:A": -30}, {"txn:A": 45}])
        self.assertEqual(self.agg.values, {"txn:A": 45})
        self.assertEqual(published[2].bases, {"txn:A": "rev-t2"})

    def test_reingest_is_idempotent(self):
        events = load_change_fixture("fixtures/changes_basic.json")
        self.agg.ingest(events)
        again = self.agg.ingest(events)
        self.assertEqual(again, [])
        self.assertEqual(self.agg.values, {"txn:A": 150, "txn:B": 0})
        self.assertEqual(len(self.store.revisions()), 4)

    def test_state_rebuilt_from_store(self):
        self.agg.ingest(load_change_fixture("fixtures/changes_basic.json"))
        reloaded = Aggregator(self.store)
        self.assertEqual(reloaded.values, {"txn:A": 150, "txn:B": 0})
        self.assertEqual(reloaded.heads["txn:A"], "rev-t4")


if __name__ == "__main__":
    unittest.main()
