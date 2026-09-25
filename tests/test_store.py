import unittest

from bucketindex import EventStore, FineBuckets, Instant


def make_store(width=10):
    return EventStore(FineBuckets(origin=0, width=width))


class TestRangeCount(unittest.TestCase):
    def setUp(self):
        self.store = make_store()
        for i, t in enumerate([-25, -10, -1, 0, 3, 3, 10, 25, 42]):
            self.store.ingest(f"e{i}", Instant(t))

    def test_exact_count_arbitrary_range(self):
        r = self.store.count_range(Instant(-1), Instant(25))
        self.assertEqual(r.total, 5)  # -1, 0, 3, 3, 10
        self.assertEqual(r.total, r.internal + len(r.edge_items))

    def test_edges_itemised_not_double_counted(self):
        # Range [-5, 15) cuts edge buckets -1 and 1; bucket 0 is internal.
        r = self.store.count_range(Instant(-5), Instant(15))
        self.assertEqual(r.total, 5)  # -1, 0, 3, 3, 10
        self.assertEqual(r.internal, 3)  # 0, 3, 3 from complete bucket 0
        edge_ticks = sorted(it[0] for it in r.edge_items)
        self.assertEqual(edge_ticks, [-1, 10])
        self.assertEqual(r.total, r.internal + len(r.edge_items))

    def test_aligned_range_uses_internal_only(self):
        r = self.store.count_range(Instant(-10), Instant(20))
        self.assertEqual(r.total, 6)  # -10, -1, 0, 3, 3, 10
        self.assertEqual(r.internal, 6)
        self.assertEqual(r.edge_items, ())

    def test_single_partial_bucket(self):
        r = self.store.count_range(Instant(1), Instant(5))
        self.assertEqual(r.total, 2)  # the two events at t=3
        self.assertEqual(r.internal, 0)
        self.assertEqual(len(r.edge_items), 2)

    def test_empty_range(self):
        self.assertEqual(self.store.count_range(Instant(5), Instant(5)).total, 0)

    def test_cross_layer_sum_matches(self):
        # Sum of business bucket counts == total; adjacent ranges partition.
        rule = self.store.rules.register((-20, 0, 20))
        counts = self.store.business_counts(rule)
        self.assertEqual(counts, [1, 2, 4, 2])
        self.assertEqual(sum(counts), self.store.total())
        left = self.store.count_range(Instant(-100), Instant(0)).total
        right = self.store.count_range(Instant(0), Instant(100)).total
        self.assertEqual(left + right, self.store.total())


class TestEventLifecycle(unittest.TestCase):
    def test_late_events_insert_out_of_order(self):
        s = make_store()
        s.ingest("b", Instant(50))
        s.ingest("a", Instant(-50))  # late arrival, earlier time
        self.assertEqual(s.kth(0), (-50, "a", 1))
        self.assertEqual(s.kth(1), (50, "b", 1))

    def test_correction_moves_event_single_leaf(self):
        s = make_store()
        s.ingest("x", Instant(3))
        s.correct("x", Instant(77), version=2)
        self.assertEqual(s.total(), 1)  # same event counted exactly once
        self.assertEqual(s.count_range(Instant(0), Instant(10)).total, 0)
        self.assertEqual(s.count_range(Instant(70), Instant(80)).total, 1)
        self.assertEqual(s.kth(0), (77, "x", 2))

    def test_retract_removes_contribution(self):
        s = make_store()
        s.ingest("x", Instant(3))
        s.ingest("y", Instant(4))
        s.retract("x")
        self.assertEqual(s.total(), 1)
        self.assertEqual(s.kth(0), (4, "y", 1))

    def test_history_keeps_source_and_version(self):
        s = make_store()
        s.ingest("x", Instant(3))
        s.correct("x", Instant(8), version=2)
        s.retract("x")
        kinds = [c.kind for c in s.history]
        self.assertEqual(kinds, ["ingest", "correct", "retract"])
        self.assertEqual([c.version for c in s.history], [1, 2, 2])
        self.assertTrue(all(c.event_id == "x" for c in s.history))

    def test_explain_traces_contributions(self):
        s = make_store()
        s.ingest("a", Instant(1))
        s.ingest("b", Instant(2))
        s.correct("b", Instant(5), version=2)
        items = s.explain_range(Instant(0), Instant(10))
        self.assertEqual(items, [(1, "a", 1), (5, "b", 2)])


class TestOrderStatistics(unittest.TestCase):
    def test_kth_with_duplicate_times_stable_id_order(self):
        s = make_store()
        for eid in ("c", "a", "b"):
            s.ingest(eid, Instant(7))
        s.ingest("d", Instant(9))
        self.assertEqual([s.kth(k)[1] for k in range(4)], ["a", "b", "c", "d"])

    def test_quantile(self):
        s = make_store()
        for i, t in enumerate([10, 20, 30, 40]):
            s.ingest(f"e{i}", Instant(t))
        self.assertEqual(s.quantile(0.0), Instant(10))
        self.assertEqual(s.quantile(0.5), Instant(20))
        self.assertEqual(s.quantile(0.75), Instant(30))
        self.assertEqual(s.quantile(1.0), Instant(40))
        self.assertIsNone(make_store().quantile(0.5))


class TestSnapshotsAndRules(unittest.TestCase):
    def test_tree_snapshots_survive_later_mutations(self):
        s = make_store()
        s.ingest("a", Instant(5))
        snap = s.snapshot_version
        s.ingest("b", Instant(6))
        s.retract("a")
        self.assertEqual(s.total(snap), 1)
        self.assertEqual(s.kth(0, snap), (5, "a", 1))
        self.assertEqual(s.total(), 1)
        self.assertEqual(s.kth(0), (6, "b", 1))

    def test_rule_update_keeps_old_snapshots_without_rebuild(self):
        s = make_store()
        for i, t in enumerate([-30, -5, 10, 25, 90]):
            s.ingest(f"e{i}", Instant(t))
        rule_v1 = s.rules.register((0, 20))
        before = s.business_counts(rule_v1)
        root_before = s.root_at()

        # Update the bucket rules; the tree must be untouched.
        rule_v2 = s.rules.register((-10, 50))
        self.assertIs(s.root_at(), root_before)
        self.assertEqual(s.business_counts(rule_v1), before)  # old snapshot valid
        self.assertEqual(before, [2, 1, 2])
        self.assertEqual(s.business_counts(rule_v2), [1, 3, 1])

        # New events are visible under both rule versions.
        s.ingest("late", Instant(-100))
        self.assertEqual(s.business_counts(rule_v1), [3, 1, 2])
        self.assertEqual(s.business_counts(rule_v2), [2, 3, 1])


if __name__ == "__main__":
    unittest.main()
