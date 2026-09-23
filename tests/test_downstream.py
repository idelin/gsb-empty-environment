import unittest

from revisioned_aggregate import (
    Aggregator, DownstreamApplier, Revision, RevisionStore, load_change_fixture,
)


def make_revisions(fixture="fixtures/changes_basic.json"):
    store = RevisionStore(":memory:")
    agg = Aggregator(store)
    agg.ingest(load_change_fixture(fixture))
    return {r.id: r for r in store.revisions()}


class DownstreamTest(unittest.TestCase):
    def setUp(self):
        self.revs = make_revisions()
        self.down = DownstreamApplier()

    def deliver_all_in_order(self):
        for rid in ["rev-t1", "rev-t2", "rev-t3", "rev-t4"]:
            self.assertEqual(self.down.receive(self.revs[rid]), "applied")

    def test_in_order_apply(self):
        self.deliver_all_in_order()
        self.assertEqual(self.down.values, {"txn:A": 150, "txn:B": 0})

    def test_missing_intermediate_pauses_instead_of_scrambling(self):
        self.assertEqual(self.down.receive(self.revs["rev-t3"]), "waiting-gap")
        self.assertEqual(self.down.values, {})
        self.assertEqual(self.down.receive(self.revs["rev-t2"]), "waiting-gap")
        self.assertEqual(self.down.receive(self.revs["rev-t1"]), "applied")
        self.assertEqual(self.down.values, {"txn:A": 100, "txn:B": 50})
        self.down.receive(self.revs["rev-t4"])
        self.assertEqual(self.down.values, {"txn:A": 150, "txn:B": 0})

    def test_duplicate_delivery_does_not_double_add(self):
        self.deliver_all_in_order()
        self.assertEqual(self.down.receive(self.revs["rev-t1"]), "duplicate")
        self.assertEqual(self.down.values, {"txn:A": 150, "txn:B": 0})
        self.assertEqual(self.down.duplicates, ["rev-t1"])

    def test_gap_and_conflict_are_distinct(self):
        unknown_base = Revision(id="rev-x", txn_id="tx", bases={"txn:A": "rev-nope"},
                                deltas={"txn:A": 1}, seq_lo=9, seq_hi=9)
        self.assertEqual(self.down.receive(unknown_base), "waiting-gap")
        self.assertEqual(self.down.conflicts, [])

        self.deliver_all_in_order()
        stale = Revision(id="rev-stale", txn_id="ty", bases={"txn:A": "rev-t2"},
                         deltas={"txn:A": 999}, seq_lo=10, seq_hi=10)
        self.assertEqual(self.down.receive(stale), "conflict")
        self.assertEqual(self.down.conflicts,
                         [("rev-stale", "txn:A", "rev-t2", "rev-t4")])
        self.assertEqual(self.down.values, {"txn:A": 150, "txn:B": 0})

    def test_old_correction_cannot_overwrite_new_result(self):
        self.deliver_all_in_order()
        old = Revision(id="rev-old", txn_id="tz", bases={"txn:A": "rev-t1"},
                       deltas={"txn:A": -100}, seq_lo=11, seq_hi=11)
        self.assertEqual(self.down.receive(old), "conflict")
        self.assertEqual(self.down.values["txn:A"], 150)

    def test_retract_readd_applies_in_commit_order(self):
        revs = make_revisions("fixtures/changes_retract.json")
        down = DownstreamApplier()
        for rid in ["rev-t1", "rev-t2", "rev-t3"]:
            down.receive(revs[rid])
        self.assertEqual(down.values, {"txn:A": 45})


if __name__ == "__main__":
    unittest.main()
