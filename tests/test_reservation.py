import unittest

from network_reservation import Demand, Graph, Interval, ReservationManager


def make_manager():
    """拓扑：A-e1(10)-B-e2(10)-C，备选 A-e3(5)-D-e4(5)-C，多重边 e5(4): A-B，孤立点 Z。"""
    graph = Graph()
    graph.add_edge("e1", "A", "B", capacity=10)
    graph.add_edge("e2", "B", "C", capacity=10)
    graph.add_edge("e3", "A", "D", capacity=5)
    graph.add_edge("e4", "D", "C", capacity=5)
    graph.add_edge("e5", "A", "B", capacity=4)
    graph.add_vertex("Z")
    return graph, ReservationManager(graph)


class TestInterval(unittest.TestCase):
    def test_left_closed_right_open(self):
        a = Interval(0, 10)
        self.assertFalse(a.overlaps(Interval(10, 20)))  # 首尾相接不重叠
        self.assertTrue(a.overlaps(Interval(9, 20)))
        self.assertTrue(a.overlaps(Interval(0, 1)))

    def test_invalid_interval(self):
        with self.assertRaises(ValueError):
            Interval(10, 10)
        with self.assertRaises(ValueError):
            Interval(11, 10)


class TestGraph(unittest.TestCase):
    def test_isolated_vertex_kept(self):
        graph, _ = make_manager()
        self.assertIn("Z", graph.vertices)
        self.assertEqual(graph.incident_edges("Z"), [])

    def test_parallel_edges_kept(self):
        graph, _ = make_manager()
        self.assertEqual(sorted(graph.incident_edges("A")), ["e1", "e3", "e5"])
        self.assertEqual(graph.edges["e1"].endpoint_b, "B")
        self.assertEqual(graph.edges["e5"].endpoint_b, "B")

    def test_duplicate_edge_id_rejected(self):
        graph, _ = make_manager()
        with self.assertRaises(ValueError):
            graph.add_edge("e1", "A", "C", capacity=1)


class TestReservationOrder(unittest.TestCase):
    def test_priority_order_capacity_for_one_only(self):
        """容量只够一条时，高优先级成功、低优先级失败，不得两条都承诺。"""
        graph = Graph()
        graph.add_edge("e1", "A", "B", capacity=10)
        manager = ReservationManager(graph)
        results = manager.reserve_all([
            Demand("d-low", "A", "B", 10, Interval(0, 10), priority=1),
            Demand("d-high", "A", "B", 10, Interval(0, 10), priority=9),
        ])
        self.assertEqual(results, {"d-high": True, "d-low": False})

    def test_same_priority_ordered_by_demand_id(self):
        """同优先级按需求ID升序：ID 小者先占容量。"""
        graph = Graph()
        graph.add_edge("e1", "A", "B", capacity=10)
        manager = ReservationManager(graph)
        results = manager.reserve_all([
            Demand("d-b", "A", "B", 10, Interval(0, 10), priority=5),
            Demand("d-a", "A", "B", 10, Interval(0, 10), priority=5),
        ])
        self.assertEqual(results, {"d-a": True, "d-b": False})

    def test_back_to_back_intervals_both_fit(self):
        """左闭右开：[0,10) 与 [10,20) 不重叠，都可占用同一容量。"""
        graph = Graph()
        graph.add_edge("e1", "A", "B", capacity=10)
        manager = ReservationManager(graph)
        results = manager.reserve_all([
            Demand("d1", "A", "B", 10, Interval(0, 10)),
            Demand("d2", "A", "B", 10, Interval(10, 20)),
        ])
        self.assertEqual(results, {"d1": True, "d2": True})

    def test_overlapping_intervals_share_capacity(self):
        graph = Graph()
        graph.add_edge("e1", "A", "B", capacity=10)
        manager = ReservationManager(graph)
        results = manager.reserve_all([
            Demand("d1", "A", "B", 6, Interval(0, 10)),
            Demand("d2", "A", "B", 6, Interval(5, 15)),
        ])
        self.assertEqual(results, {"d1": True, "d2": False})

    def test_parallel_edges_serve_two_demands(self):
        """多重边：两条 A-B 需求分别落在 e1 与 e5 上。"""
        graph, manager = make_manager()
        results = manager.reserve_all([
            Demand("d1", "A", "B", 10, Interval(0, 10)),
            Demand("d2", "A", "B", 4, Interval(0, 10)),
        ])
        self.assertEqual(results, {"d1": True, "d2": True})
        self.assertEqual(manager.get_reservation("d1").path, ("e1",))
        self.assertEqual(manager.get_reservation("d2").path, ("e5",))

    def test_unreachable_isolated_vertex_fails(self):
        _, manager = make_manager()
        ok = manager.reserve(Demand("dz", "A", "Z", 1, Interval(0, 10)))
        self.assertFalse(ok)


class TestAtomicAllocate(unittest.TestCase):
    def test_midway_failure_releases_occupied_edges(self):
        """替代路径中途失败：已占用的前序边必须释放。"""
        graph = Graph()
        graph.add_edge("e1", "A", "B", capacity=10)
        graph.add_edge("e2", "B", "C", capacity=5)
        manager = ReservationManager(graph)
        demand = Demand("d", "A", "C", 6, Interval(0, 10))
        # e2 容量 5 < 6，分配到 e2 时失败，e1 必须被释放
        self.assertFalse(manager._allocate(demand, ["e1", "e2"]))
        self.assertEqual(manager.available_capacity("e1", Interval(0, 10)), 10)
        self.assertEqual(manager.available_capacity("e2", Interval(0, 10)), 5)


class TestEdgeFailure(unittest.TestCase):
    def test_only_affected_demands_rerouted(self):
        _, manager = make_manager()
        manager.reserve_all([
            Demand("d-via-e1", "A", "C", 5, Interval(0, 10), priority=9),
            Demand("d-via-e2", "B", "C", 5, Interval(0, 10), priority=1),
        ])
        before = manager.get_reservation("d-via-e2").path
        outcome = manager.fail_edge("e1")
        self.assertEqual(outcome["rerouted"], ["d-via-e1"])
        self.assertEqual(outcome["rolled_back"], [])
        # 未受影响的需求路径不变
        self.assertEqual(manager.get_reservation("d-via-e2").path, before)
        # 受影响需求改走备选路径 e3-e4
        self.assertEqual(manager.get_reservation("d-via-e1").path, ("e3", "e4"))

    def test_failed_reroute_rolls_back(self):
        """无备选路径时回滚：原预留与容量占用保持不变。"""
        graph = Graph()
        graph.add_edge("e1", "A", "B", capacity=10)
        manager = ReservationManager(graph)
        manager.reserve(Demand("d", "A", "B", 6, Interval(0, 10)))
        outcome = manager.fail_edge("e1")
        self.assertEqual(outcome["rerouted"], [])
        self.assertEqual(outcome["rolled_back"], ["d"])
        reservation = manager.get_reservation("d")
        self.assertIsNotNone(reservation)
        self.assertEqual(reservation.path, ("e1",))
        # 容量核算一致：e1 上仍占用 6
        self.assertEqual(manager.available_capacity("e1", Interval(0, 10)), 4)

    def test_reroute_respects_capacity_and_rolls_back(self):
        """备选路径容量不足时同样回滚，且不残留占用。"""
        _, manager = make_manager()
        manager.reserve_all([
            Demand("d-main", "A", "C", 6, Interval(0, 10), priority=9),   # 走 e1-e2
            Demand("d-block", "A", "C", 5, Interval(0, 10), priority=8),  # 走 e3-e4 占满备选
        ])
        self.assertEqual(manager.get_reservation("d-block").path, ("e3", "e4"))
        outcome = manager.fail_edge("e1")
        # 备选 e3-e4 剩余 0 < 6，重路由失败 -> 回滚
        self.assertEqual(outcome["rolled_back"], ["d-main"])
        self.assertEqual(manager.get_reservation("d-main").path, ("e1", "e2"))
        self.assertEqual(manager.available_capacity("e1", Interval(0, 10)), 4)
        self.assertEqual(manager.available_capacity("e3", Interval(0, 10)), 0)

    def test_fail_edge_idempotent(self):
        _, manager = make_manager()
        first = manager.fail_edge("e1")
        second = manager.fail_edge("e1")
        self.assertEqual(second, {"rerouted": [], "rolled_back": []})
        self.assertTrue(manager.graph.edges["e1"].failed)
        self.assertIsNotNone(first)


class TestEdgeRecovery(unittest.TestCase):
    def test_recover_restores_capacity_once(self):
        """恢复故障边不能重复增加可用容量。"""
        graph = Graph()
        graph.add_edge("e1", "A", "B", capacity=10)
        manager = ReservationManager(graph)
        manager.reserve(Demand("d", "A", "B", 6, Interval(0, 10)))
        manager.fail_edge("e1")
        self.assertTrue(manager.recover_edge("e1"))
        after_first = manager.available_capacity("e1", Interval(0, 10))
        # 重复恢复：空操作，容量不变
        self.assertFalse(manager.recover_edge("e1"))
        self.assertEqual(manager.available_capacity("e1", Interval(0, 10)), after_first)
        self.assertEqual(after_first, 4)

    def test_recover_non_failed_edge_is_noop(self):
        graph = Graph()
        graph.add_edge("e1", "A", "B", capacity=10)
        manager = ReservationManager(graph)
        self.assertFalse(manager.recover_edge("e1"))
        self.assertEqual(manager.available_capacity("e1", Interval(0, 10)), 10)

    def test_recovered_edge_usable_again(self):
        graph = Graph()
        graph.add_edge("e1", "A", "B", capacity=10)
        manager = ReservationManager(graph)
        manager.fail_edge("e1")
        self.assertIsNone(manager.find_path("A", "B", 5, Interval(0, 10)))
        manager.recover_edge("e1")
        self.assertEqual(manager.find_path("A", "B", 5, Interval(0, 10)), ["e1"])


if __name__ == "__main__":
    unittest.main()
