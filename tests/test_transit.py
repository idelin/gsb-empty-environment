"""unittest 套件：覆盖换乘规则、帕累托查询、容量预留、证据失效与穷举一致性。"""
from __future__ import annotations

import os
import random
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from transit.model import Graph, Timetable
from transit.query import QueryEngine
from transit.bruteforce import brute_force_frontier
from transit.reserve import ReservationManager
from transit.planner import Planner, JourneyStatus
from samples.replay import build_timetable, run as replay_run


def simple_timetable() -> Timetable:
    g = Graph()
    tt = Timetable(g)
    tt.set_transfer_time("B", 2)
    tt.add_trip("P1", ["A", "B"], [0, 5], [0, 5], capacity=2)
    tt.add_trip("P2", ["B", "C"], [6, 10], [6, 10], capacity=2)
    tt.add_trip("P3", ["B", "C"], [7, 12], [7, 12], capacity=1)
    return tt


class TransferRuleTest(unittest.TestCase):
    def test_arrive5_transfer2_cannot_board6_can_board7(self):
        tt = simple_timetable()
        engine = QueryEngine(tt)
        journeys = engine.query("A", "C", 0, max_transfers=1)
        self.assertEqual(len(journeys), 1)
        j = journeys[0]
        # 到站 5、换乘 2：不能搭 6 发车（P2），可搭 7 发车（P3）
        self.assertEqual(j.trip_ids, ["P1", "P3"])
        self.assertEqual(j.arrival, 12)
        self.assertEqual(j.transfers, 1)

    def test_boarding_rule_boundaries(self):
        tt = simple_timetable()
        self.assertEqual(tt.transfer_time("B"), 2)
        # 5 + 2 = 7 > 6，故 P2 不可乘；7 >= 7，P3 可乘（左闭）
        self.assertFalse(6 >= 5 + tt.transfer_time("B"))
        self.assertTrue(7 >= 5 + tt.transfer_time("B"))


class ParetoQueryTest(unittest.TestCase):
    def test_two_non_dominated_routes(self):
        tt = build_timetable()
        engine = QueryEngine(tt)
        frontier = engine.pareto_frontier("A", "C", 0, max_transfers=1)
        # 同一出发时刻：直达更晚 (15, 0) 与换乘更早 (12, 1) 互不支配
        self.assertEqual(frontier, [(12, 1), (15, 0)])

    def test_tied_journeys_same_label_both_returned(self):
        tt = build_timetable()
        engine = QueryEngine(tt)
        journeys = engine.query("C", "D", 0, max_transfers=0)
        # T5 与 T5B 同时刻同标签，两条并列旅程都返回
        self.assertEqual(len(journeys), 2)
        self.assertEqual({tuple(j.trip_ids) for j in journeys}, {("T5",), ("T5B",)})
        self.assertEqual({j.label for j in journeys}, {(26, 0)})

    def test_overnight_trip(self):
        tt = build_timetable()
        engine = QueryEngine(tt)
        journeys = engine.query("C", "E", 1400, max_transfers=0)
        self.assertEqual(len(journeys), 1)
        self.assertEqual(journeys[0].trip_ids, ["T4"])
        self.assertEqual(journeys[0].arrival, 1510)  # 次日 01:10

    def test_graph_isolated_vertices_and_parallel_edges(self):
        tt = build_timetable()
        self.assertIn("X", tt.graph.isolated_vertices())
        self.assertIn("X", tt.graph.vertices)
        edge_ids = [e.edge_id for e in tt.graph.out_edges("C")]
        self.assertEqual(edge_ids, ["W-CD-1", "W-CD-2"])  # 多重边均保留
        # 步行选最快平行边 W-CD-1 (30 分钟)
        engine = QueryEngine(tt)
        journeys = engine.query("A", "D", 0, max_transfers=2)
        walk_j = [j for j in journeys if any(s.kind == "walk" for s in j.segments)]
        self.assertTrue(walk_j)
        for j in walk_j:
            for s in j.segments:
                if s.kind == "walk":
                    self.assertEqual(s.edge_id, "W-CD-1")


class ReservationTest(unittest.TestCase):
    def setUp(self):
        self.tt = simple_timetable()

    def test_two_requests_compete_last_seat_only_one_wins(self):
        mgr = ReservationManager(self.tt, now=0)
        t1 = mgr.reserve(["P3"], ttl=100)
        t2 = mgr.reserve(["P3"], ttl=100)
        self.assertIsNotNone(t1)
        self.assertIsNone(t2)  # 最后座位只能一个成功
        self.assertEqual(mgr.available("P3"), 0)

    def test_partial_failure_rolls_back_no_leak(self):
        mgr = ReservationManager(self.tt, now=0)
        self.assertIsNotNone(mgr.reserve(["P3"], ttl=100))  # 占掉 P3 唯一座位
        before_p1, before_p2 = mgr.available("P1"), mgr.available("P2")
        token = mgr.reserve(["P1", "P2", "P3"], ttl=50)     # 第三段失败
        self.assertIsNone(token)
        self.assertEqual(mgr.available("P1"), before_p1)    # 无额度泄漏
        self.assertEqual(mgr.available("P2"), before_p2)

    def test_interval_left_closed_right_open(self):
        mgr = ReservationManager(self.tt, now=0)
        token = mgr.reserve(["P1"], ttl=50)  # 有效期 [0, 50)
        mgr.advance(49)
        self.assertTrue(mgr.is_valid(token))
        mgr.advance(50)
        self.assertFalse(mgr.is_valid(token))  # t == expires_at 即过期

    def test_stale_token_release_does_not_add_capacity(self):
        mgr = ReservationManager(self.tt, now=0)
        token = mgr.reserve(["P3"], ttl=10)
        mgr.advance(10)  # 过期，容量回收一次
        self.assertEqual(mgr.available("P3"), 1)
        self.assertFalse(mgr.release(token))   # 旧令牌被拒绝
        self.assertEqual(mgr.available("P3"), 1)  # 不重复增加额度
        # 容量确实被回收：新预留可成功
        self.assertIsNotNone(mgr.reserve(["P3"], ttl=10))
        self.assertEqual(mgr.available("P3"), 0)

    def test_expired_token_after_restart(self):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "state.json")
            mgr = ReservationManager(self.tt, state_path=path, now=0)
            token = mgr.reserve(["P3"], ttl=30)
            self.assertEqual(mgr.available("P3"), 0)
            # 重启：新管理器加载同一状态文件，时钟已越过期点
            mgr2 = ReservationManager(self.tt, state_path=path, now=100)
            self.assertEqual(mgr2.available("P3"), 1)   # 过期预留已回收且仅一次
            self.assertFalse(mgr2.release(token))       # 过期令牌仍被拒绝
            self.assertEqual(mgr2.available("P3"), 1)   # 额度不被旧令牌增加
            self.assertFalse(mgr2.is_valid(token))

    def test_release_valid_token_frees_exactly_once(self):
        mgr = ReservationManager(self.tt, now=0)
        token = mgr.reserve(["P3"], ttl=100)
        self.assertTrue(mgr.release(token))
        self.assertEqual(mgr.available("P3"), 1)
        self.assertFalse(mgr.release(token))  # 重复释放同一令牌被拒绝
        self.assertEqual(mgr.available("P3"), 1)


class EvidenceInvalidationTest(unittest.TestCase):
    def setUp(self):
        self.tt = build_timetable()
        self.engine = QueryEngine(self.tt)
        self.mgr = ReservationManager(self.tt, now=0)
        self.planner = Planner(self.tt, self.engine, self.mgr)

    def test_cancel_mid_journey_trip_requires_replan(self):
        journeys = self.engine.query("A", "D", 0, max_transfers=2)
        j = next(j for j in journeys if j.trip_ids == ["T1", "T3", "T5"])
        token = self.mgr.reserve(["T1", "T3", "T5"], ttl=100)
        cj = self.planner.confirm(j, token)
        self.assertIs(cj.status, JourneyStatus.CONFIRMED)
        self.tt.cancel_trip("T3")  # 撤销中途班次
        self.assertIs(cj.status, JourneyStatus.REPLAN_REQUIRED)
        # 原旅程不再出现在查询结果中
        after = self.engine.query("A", "D", 0, max_transfers=2)
        self.assertNotIn(["T1", "T3", "T5"], [j2.trip_ids for j2 in after])

    def test_update_trip_invalidates_evidence(self):
        j = self.engine.query("A", "C", 1, max_transfers=0)[0]  # T6 直达
        token = self.mgr.reserve(["T6"], ttl=100)
        cj = self.planner.confirm(j, token)
        self.tt.update_trip("T6", departures=[2, 16], arrivals=[2, 16])  # 更正
        self.assertIs(cj.status, JourneyStatus.REPLAN_REQUIRED)
        # 未涉及该班次的确认旅程不受影响
        j2 = self.engine.query("C", "D", 0, max_transfers=0)[0]
        cj2 = self.planner.confirm(j2, "manual")
        self.tt.update_trip("T1", departures=[0, 5], arrivals=[0, 5], capacity=3)
        self.assertIs(cj2.status, JourneyStatus.CONFIRMED)


def random_timetable(rng: random.Random) -> Timetable:
    g = Graph()
    stops = [f"S{i}" for i in range(rng.randint(3, 6))]
    for s in stops:
        g.add_vertex(s)
    tt = Timetable(g)
    for s in stops:
        if rng.random() < 0.7:
            tt.set_transfer_time(s, rng.randint(0, 3))
    # 仅前向步行边（无环），避免走回起点
    for i in range(len(stops)):
        for j in range(i + 1, len(stops)):
            if rng.random() < 0.2:
                g.add_edge(f"W{i}-{j}", stops[i], stops[j], rng.randint(1, 15))
    for t in range(rng.randint(2, 6)):
        k = rng.randint(2, min(4, len(stops)))
        path = rng.sample(stops, k)
        time = rng.randint(0, 1400)  # 可能跨日
        deps, arrs = [], []
        for idx in range(k):
            if idx > 0:
                time += rng.randint(1, 30)
            arr = time
            dep = arr + (rng.randint(0, 3) if idx < k - 1 else 0)
            arrs.append(arr)
            deps.append(dep)
            time = dep
        tt.add_trip(f"T{t}", path, deps, arrs, capacity=rng.randint(0, 3))
    return tt


class BruteForceConsistencyTest(unittest.TestCase):
    def test_query_matches_time_expanded_brute_force(self):
        rng = random.Random(20260923)
        for case in range(80):
            tt = random_timetable(rng)
            engine = QueryEngine(tt)
            origin, dest = rng.sample(sorted(tt.graph.vertices), 2)
            depart = rng.randint(0, 600)
            mt = rng.randint(0, 3)
            got = engine.pareto_frontier(origin, dest, depart, mt)
            want = brute_force_frontier(tt, origin, dest, depart, mt)
            self.assertEqual(got, want, f"case {case}: {origin}->{dest} @{depart} mt={mt}")

    def test_reconstructed_journeys_hit_frontier_labels(self):
        rng = random.Random(7)
        for case in range(40):
            tt = random_timetable(rng)
            engine = QueryEngine(tt)
            origin, dest = rng.sample(sorted(tt.graph.vertices), 2)
            depart = rng.randint(0, 600)
            mt = rng.randint(0, 3)
            journeys = engine.query(origin, dest, depart, mt)
            self.assertEqual(sorted({j.label for j in journeys}),
                             engine.pareto_frontier(origin, dest, depart, mt))
            for j in journeys:  # 每段时刻合法且换乘规则成立
                for s in j.segments:
                    self.assertLessEqual(s.dep, s.arr)


class ReplaySampleTest(unittest.TestCase):
    def test_replay_transcript(self):
        with tempfile.TemporaryDirectory() as d:
            text = "\n".join(replay_run(state_dir=d))
        for marker in [
            "00:06发车不可乘, 00:07发车可乘",
            "请求2 竞争 T3 最后座位: 失败(座位已占)",
            "已回滚, T2 额度 2->2 (无泄漏)",
            "取消中途班次 T3 后: J1=REPLAN_REQUIRED",
            "更正 T6 后: J1=CONFIRMED J2=REPLAN_REQUIRED",
            "t=50 令牌已过期: True",
            "过期令牌释放: 被拒绝, T2 额度变化: 0",
            "T4 可用额度=2(已回收且仅一次), 过期令牌释放: 被拒绝",
            "到达=+1d 01:10",
        ]:
            self.assertIn(marker, text)


if __name__ == "__main__":
    unittest.main()
