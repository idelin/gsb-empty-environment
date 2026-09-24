import unittest

from network import Demand, Interval, Network


def demand(did, priority=1, src="A", dst="B", bw=5, start=0, end=10):
    return Demand(did, priority, src, dst, bw, Interval(start, end))


class ReservationOrderTest(unittest.TestCase):
    def test_capacity_for_one_only_commits_one(self):
        net = Network()
        net.add_edge("e1", "A", "B", 10)
        demands = [demand("d1", priority=1, bw=6), demand("d2", priority=2, bw=6)]
        result = net.reserve_all(demands)
        self.assertIsNotNone(result["d2"])  # higher priority wins
        self.assertIsNone(result["d1"])

    def test_same_priority_ordered_by_demand_id(self):
        net = Network()
        net.add_edge("e1", "A", "B", 10)
        demands = [demand("dz", priority=5, bw=6), demand("da", priority=5, bw=6)]
        result = net.reserve_all(demands)
        self.assertIsNotNone(result["da"])  # smaller id first
        self.assertIsNone(result["dz"])


class IntervalTest(unittest.TestCase):
    def test_half_open_intervals_do_not_overlap(self):
        net = Network()
        net.add_edge("e1", "A", "B", 10)
        demands = [
            demand("d1", bw=10, start=0, end=10),
            demand("d2", bw=10, start=10, end=20),
        ]
        result = net.reserve_all(demands)
        self.assertIsNotNone(result["d1"])
        self.assertIsNotNone(result["d2"])

    def test_partial_overlap_counts_peak_load(self):
        net = Network()
        net.add_edge("e1", "A", "B", 10)
        demands = [
            demand("d1", bw=6, start=0, end=10),
            demand("d2", bw=6, start=9, end=20),
        ]
        result = net.reserve_all(demands)
        self.assertIsNotNone(result["d1"])
        self.assertIsNone(result["d2"])  # 6+6 > 10 during [9, 10)


class TopologyTest(unittest.TestCase):
    def test_isolated_vertex_preserved_and_unreachable(self):
        net = Network()
        net.add_vertex("iso")
        net.add_edge("e1", "A", "B", 10)
        self.assertIn("iso", net.vertices)
        result = net.reserve_all([demand("d1", src="A", dst="iso")])
        self.assertIsNone(result["d1"])

    def test_parallel_edges_both_usable(self):
        net = Network()
        net.add_edge("e1", "A", "B", 5)
        net.add_edge("e2", "A", "B", 5)
        demands = [demand("d1", bw=5), demand("d2", bw=5)]
        result = net.reserve_all(demands)
        self.assertIsNotNone(result["d1"])
        self.assertIsNotNone(result["d2"])
        self.assertNotEqual(result["d1"], result["d2"])


class FailureRerouteTest(unittest.TestCase):
    def build(self):
        net = Network()
        net.add_edge("e1", "A", "B", 10)
        net.add_edge("e2", "B", "C", 10)
        net.add_edge("e3", "A", "X", 5)
        net.add_edge("e4", "X", "C", 5)
        net.add_edge("e5", "C", "D", 10)
        demands = [
            demand("d1", priority=2, src="A", dst="C", bw=5),
            demand("d2", priority=1, src="C", dst="D", bw=5),
        ]
        by_id = {d.id: d for d in demands}
        net.reserve_all(demands)
        return net, by_id

    def test_only_affected_demands_rerouted(self):
        net, by_id = self.build()
        before = net.placement("d2")
        result = net.fail_edge("e1", by_id)
        self.assertEqual(set(result), {"d1"})
        self.assertEqual(net.placement("d2"), before)
        self.assertEqual(net.placement("d1"), ["e3", "e4"])

    def test_failed_reroute_rolls_back_reservation(self):
        net = Network()
        net.add_edge("e1", "A", "B", 10)  # only path A->B
        d = demand("d1", bw=5)
        net.reserve_all([d])
        result = net.fail_edge("e1", {"d1": d})
        self.assertIsNone(result["d1"])
        self.assertIsNone(net.placement("d1"))
        full = net.available_capacity("e1", Interval(0, 10))
        self.assertEqual(full, 10)  # reservation released, not leaked

    def test_midway_failure_releases_acquired_edges(self):
        net = Network()
        net.add_edge("e1", "A", "B", 10)
        net.add_edge("e2", "B", "C", 4)  # too small for bw=5
        d = demand("d1", src="A", dst="C", bw=5)
        ok = net.reserve_path(d, ["e1", "e2"])
        self.assertFalse(ok)
        self.assertEqual(net.available_capacity("e1", Interval(0, 10)), 10)
        self.assertIsNone(net.placement("d1"))

    def test_failed_edge_not_used_by_new_paths(self):
        net = Network()
        net.add_edge("e1", "A", "B", 10)
        net.fail_edge("e1", {})
        result = net.reserve_all([demand("d1")])
        self.assertIsNone(result["d1"])


class RecoveryTest(unittest.TestCase):
    def test_recover_does_not_double_add_capacity(self):
        net = Network()
        net.add_edge("e1", "A", "B", 10)
        net.add_edge("e2", "A", "B", 10)
        d = demand("d1", bw=6)
        net.reserve_all([d])
        net.fail_edge("e1", {"d1": d})  # d1 reroutes to e2
        interval = Interval(0, 10)
        self.assertTrue(net.recover_edge("e1"))
        after_first = net.available_capacity("e1", interval)
        self.assertFalse(net.recover_edge("e1"))  # idempotent no-op
        after_second = net.available_capacity("e1", interval)
        self.assertEqual(after_first, 10)
        self.assertEqual(after_first, after_second)

    def test_recover_makes_edge_usable_again(self):
        net = Network()
        net.add_edge("e1", "A", "B", 10)
        net.fail_edge("e1", {})
        net.recover_edge("e1")
        result = net.reserve_all([demand("d1", bw=10)])
        self.assertEqual(result["d1"], ["e1"])


if __name__ == "__main__":
    unittest.main()
