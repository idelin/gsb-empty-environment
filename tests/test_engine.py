import unittest
from fractions import Fraction as Fr

from stcollide import World


class TestStalePrediction(unittest.TestCase):
    def test_turn_cancels_predicted_event(self):
        # A 与 B 预测在 t=9 碰撞，但 t=3 时 A 转向 → 旧事件必须取消
        w = World(dim=1, t0=0, t1=20)
        w.add_object("a", (0,), (1,), (1,))
        w.add_object("b", (10,), (11,), (0,))
        w.schedule(3, lambda world: world.set_velocity("a", 3, (-1,)))
        result = w.simulate()
        self.assertEqual(result.events, [])
        self.assertEqual(result.status, "ok")
        self.assertGreaterEqual(result.stale_discarded, 1)

    def test_destroy_cancels_predicted_event(self):
        w = World(dim=1, t0=0, t1=20)
        w.add_object("a", (0,), (1,), (1,))
        w.add_object("b", (10,), (11,), (0,))
        w.schedule(5, lambda world: world.destroy("b", 5))
        result = w.simulate()
        self.assertEqual(result.events, [])
        self.assertGreaterEqual(result.stale_discarded, 1)

    def test_destroy_after_collision_keeps_event(self):
        w = World(dim=1, t0=0, t1=20)
        w.add_object("a", (0,), (1,), (1,))
        w.add_object("b", (10,), (11,), (0,))
        w.schedule(12, lambda world: world.destroy("b", 12))
        result = w.simulate()
        self.assertEqual(len(result.events), 1)
        self.assertEqual(result.events[0].time, Fr(9))

    def test_turn_into_new_collision_repredicted(self):
        # t=3 转向后反而撞向静止的 C → 应产生新事件
        w = World(dim=1, t0=0, t1=20)
        w.add_object("a", (0,), (1,), (1,))
        w.add_object("b", (10,), (11,), (0,))
        w.add_object("c", (-6,), (-5,), (0,))
        w.schedule(3, lambda world: world.set_velocity("a", 3, (-1,)))
        result = w.simulate()
        self.assertEqual(len(result.events), 1)
        ev = result.events[0]
        self.assertEqual({ev.a, ev.b}, {"a", "c"})
        # t=3 时 a 位于 [3,4]，反向 v=-1：a_lo = 3-(t-3) = 6-t；6-t = -5 → t=11
        self.assertEqual(ev.time, Fr(11))


class TestSameTimeBatch(unittest.TestCase):
    def build_three_body(self):
        w = World(dim=2, t0=0, t1=10)
        w.add_object("a", (0, 0), (1, 1), (1, 0))
        w.add_object("b", (2, 0), (3, 1), (0, 0))
        w.add_object("c", (Fr(3, 2), 2), (Fr(5, 2), 3), (0, -1))
        return w

    def test_three_pairs_same_time_all_processed(self):
        w = self.build_three_body()
        result = w.simulate()
        pairs = {frozenset((e.a, e.b)) for e in result.events}
        self.assertEqual(pairs, {frozenset(p) for p in
                                 (("a", "b"), ("a", "c"), ("b", "c"))})
        self.assertTrue(all(e.time == Fr(1) for e in result.events))

    def test_batch_uses_same_pre_state(self):
        # 响应把 a 传送走：同批另两个事件仍基于响应前的前态成立
        w = self.build_three_body()
        seen = []

        def respond(world, records):
            if seen:
                return  # 只响应第一批，避免人为制造零时间环
            seen.append(list(records))
            world.set_velocity("a", records[0].time, (0, 5))

        result = w.simulate(respond=respond)
        self.assertEqual(len(seen[0]), 3)  # 三个事件同一批交付
        expected = {
            "a": ((Fr(1), Fr(0)), (Fr(2), Fr(1))),
            "b": ((Fr(2), Fr(0)), (Fr(3), Fr(1))),
            "c": ((Fr(3, 2), Fr(1)), (Fr(5, 2), Fr(2))),
        }
        for rec in seen[0]:
            self.assertEqual(rec.time, Fr(1))
            self.assertEqual(rec.box_a, expected[rec.a])
            self.assertEqual(rec.box_b, expected[rec.b])
        # a 转向后不再与 b 重叠 → 后续无 a-b 事件
        later = [e for e in result.events if e.time > 1]
        self.assertFalse(any({e.a, e.b} == {"a", "b"} for e in later))


class TestZeroTimeLoopAndBudget(unittest.TestCase):
    def test_zero_time_loop_bounded_by_budget(self):
        w = World(dim=1, t0=0, t1=10, event_budget=20)
        w.add_object("a", (0,), (1,), (0,))
        w.add_object("b", (Fr(1, 2),), (Fr(3, 2),), (0,))

        def respond(world, records):
            for rec in records:
                # 响应修改了轨迹但仍保持重叠 → 同刻再次碰撞（零时间环）
                world.set_velocity("a", rec.time, (0,))

        result = w.simulate(respond=respond)
        self.assertEqual(result.status, "budget_exceeded")
        self.assertEqual(len(result.events), 20)
        self.assertTrue(all(e.time == Fr(0) for e in result.events))

    def test_separating_response_terminates(self):
        w = World(dim=1, t0=0, t1=10, event_budget=20)
        w.add_object("a", (0,), (1,), (1,))
        w.add_object("b", (2,), (3,), (-1,))

        def respond(world, records):
            for rec in records:  # 交换速度 → 分离
                world.set_velocity(rec.a, rec.time, (-1,))
                world.set_velocity(rec.b, rec.time, (1,))

        result = w.simulate(respond=respond)
        self.assertEqual(result.status, "ok")
        self.assertEqual(len(result.events), 1)
        self.assertEqual(result.events[0].time, Fr(1, 2))


class TestSnapshotAndResponse(unittest.TestCase):
    def test_snapshot_piecewise_and_destroy(self):
        w = World(dim=1, t0=0, t1=10)
        w.add_object("a", (0,), (1,), (2,))
        w.set_velocity("a", 3, (-1,))
        w.add_object("b", (5,), (6,), (0,))
        w.destroy("b", 8)
        self.assertEqual(w.snapshot(0)["a"], ((Fr(0),), (Fr(1),)))
        self.assertEqual(w.snapshot(3)["a"], ((Fr(6),), (Fr(7),)))
        self.assertEqual(w.snapshot(5)["a"], ((Fr(4),), (Fr(5),)))
        self.assertIn("b", w.snapshot(7))
        self.assertNotIn("b", w.snapshot(8))
        self.assertNotIn("b", w.snapshot(9))

    def test_elastic_bounce_end_to_end(self):
        w = World(dim=1, t0=0, t1=10)
        w.add_object("a", (0,), (1,), (1,))
        w.add_object("b", (2,), (3,), (-1,))

        def respond(world, records):
            for rec in records:
                world.set_velocity(rec.a, rec.time, (-1,))
                world.set_velocity(rec.b, rec.time, (1,))

        result = w.simulate(respond=respond)
        self.assertEqual(len(result.events), 1)
        self.assertEqual(result.events[0].time, Fr(1, 2))
        # 碰撞点：a=[1/2,3/2]，b=[1/2,3/2]；之后分离
        snap = w.snapshot(2)
        self.assertEqual(snap["a"], ((Fr(-1),), (Fr(0),)))
        self.assertEqual(snap["b"], ((Fr(3),), (Fr(4),)))

    def test_first_collision_matches_intervals(self):
        w = World(dim=1, t0=0, t1=10)
        w.add_object("a", (0,), (1,), (1,))
        w.add_object("b", (5,), (6,), (0,))
        w.add_object("c", (3,), (4,), (-1,))
        t, pair, kind = w.first_collision()
        # a-c：1+t = 3-t → t=1；a-b：t=4；b-c 远离
        self.assertEqual((t, frozenset(pair), kind), (Fr(1), frozenset(("a", "c")), "overlap"))


if __name__ == "__main__":
    unittest.main()
