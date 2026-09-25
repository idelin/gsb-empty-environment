"""unittest 套件：规则边界、版本失效、同刻批处理、预算、参考对照。

运行：python3.11 -m unittest test_collision -v
"""

import random
import unittest
from fractions import Fraction as Fr

import reference
from world import EventBudgetExceeded, World


class TestExplicitRules(unittest.TestCase):
    def test_stationary_separated_never_collide(self):
        w = World()
        w.add_object("A", (0, 0), (1, 1))
        w.add_object("B", (5, 0), (1, 1))
        self.assertEqual(w.collision_intervals(0, 100), {})
        self.assertIsNone(w.first_collision(0, 100))
        self.assertEqual(w.simulate(0, 100), [])

    def test_stationary_touching_is_not_collision(self):
        # 持续贴面（面接触但无正体积重叠）不算碰撞
        w = World()
        w.add_object("A", (0, 0), (1, 1))
        w.add_object("B", (1, 0), (1, 1))  # 与 A 共面 x=1
        self.assertEqual(w.collision_intervals(0, 10), {})
        self.assertEqual(w.snapshot(5), set())

    def test_initial_overlap_reported_at_window_start(self):
        w = World()
        w.add_object("A", (0, 0), (2, 2))
        w.add_object("B", (1, 0), (2, 2))
        self.assertEqual(w.collision_intervals(0, 10), {("A", "B"): [(Fr(0), Fr(10))]})
        t, pairs = w.first_collision(0, 10)
        self.assertEqual(t, Fr(0))
        self.assertEqual(pairs, [("A", "B")])

    def test_instantaneous_tangency_is_not_collision(self):
        # A 沿对角线运动，与 B 仅在 t=1 时点接触 (2,1)
        w = World()
        w.add_object("A", (0, 0), (1, 1), vel=(1, 1))
        w.add_object("B", (2, 0), (1, 1))
        self.assertEqual(w.collision_intervals(0, 10), {})
        self.assertEqual(w.snapshot(1), set())

    def test_sliding_face_contact_is_not_collision(self):
        # A 沿 x 滑动，y 方向始终与 B 贴面
        w = World()
        w.add_object("A", (0, 0), (1, 1), vel=(1, 0))
        w.add_object("B", (0, 1), (1, 1))
        self.assertEqual(w.collision_intervals(0, 10), {})

    def test_zero_duration_window(self):
        w = World()
        w.add_object("A", (0, 0), (2, 2))
        w.add_object("B", (1, 0), (2, 2))
        self.assertEqual(w.collision_intervals(3, 3), {})
        self.assertIsNone(w.first_collision(3, 3))
        self.assertEqual(w.simulate(3, 3), [])

    def test_negative_velocities(self):
        w = World()
        w.add_object("A", (10, 0), (2, 2), vel=(-3, 0))
        w.add_object("B", (0, 0), (2, 2), vel=(-1, 0))
        self.assertEqual(
            w.collision_intervals(0, 20), {("A", "B"): [(Fr(4), Fr(6))]}
        )

    def test_fractional_exact_boundary(self):
        # 边界落在有理点：A 速度 1/3，B 静止，尺寸 1，间距 1
        w = World()
        w.add_object("A", (0, 0), (1, 1), vel=(Fr(1, 3), 0))
        w.add_object("B", (2, 0), (1, 1))
        # A 右沿 t/3+1 > 2 => t > 3；B 左沿 2 < ... 精确开始于 t=3
        self.assertEqual(
            w.collision_intervals(0, 30), {("A", "B"): [(Fr(3), Fr(9))]}
        )


class TestIntervalsAndQueries(unittest.TestCase):
    def test_all_intervals_multiple(self):
        # A 先正向穿过 B，t=10 折返，再穿一次
        w = World()
        w.add_object("A", (0, 0), (1, 1), vel=(1, 0))
        w.change_velocity("A", (-1, 0), 10)
        w.add_object("B", (3, 0), (1, 1))
        self.assertEqual(
            w.collision_intervals(0, 30),
            {("A", "B"): [(Fr(2), Fr(4)), (Fr(16), Fr(18))]},
        )

    def test_first_collision_lists_all_pairs_at_earliest_time(self):
        w = World()
        w.add_object("A", (0, 0), (1, 1), vel=(1, 0))
        w.add_object("B", (2, 0), (1, 1))
        w.add_object("C", (4, 0), (1, 1), vel=(-1, 0))
        t, pairs = w.first_collision(0, 10)
        self.assertEqual(t, Fr(1))
        self.assertEqual(pairs, [("A", "B"), ("B", "C")])

    def test_snapshot_query(self):
        w = World()
        w.add_object("A", (0, 0), (1, 1), vel=(1, 0))
        w.add_object("B", (2, 0), (1, 1))
        w.add_object("C", (4, 0), (1, 1), vel=(-1, 0))
        self.assertEqual(w.snapshot(Fr(3, 2)), {("A", "B"), ("B", "C")})
        self.assertEqual(w.snapshot(1), set())  # 恰相切，不算重叠
        self.assertEqual(w.snapshot(0), set())

    def test_snapshot_query_does_not_mutate(self):
        w = World()
        w.add_object("A", (0, 0), (1, 1), vel=(1, 0))
        w.add_object("B", (3, 0), (1, 1), vel=(-1, 0))
        before = (w.version_of("A"), w.version_of("B"))
        first = w.collision_intervals(0, 10)
        second = w.collision_intervals(0, 10)
        w.snapshot(5)
        w.first_collision(0, 10)
        after = (w.version_of("A"), w.version_of("B"))
        self.assertEqual(first, second)
        self.assertEqual(before, after)

    def test_destroy_cancels_future_collisions(self):
        w = World()
        w.add_object("A", (0, 0), (1, 1), vel=(1, 0))
        w.add_object("B", (11, 0), (1, 1), vel=(-1, 0))
        self.assertEqual(w.first_collision(0, 20)[0], Fr(5))
        w.destroy("B", 4)
        self.assertIsNone(w.first_collision(0, 20))
        self.assertEqual(w.simulate(0, 20), [])

    def test_destroy_clips_interval(self):
        w = World()
        w.add_object("A", (0, 0), (1, 1), vel=(1, 0))
        w.add_object("B", (11, 0), (1, 1), vel=(-1, 0))
        w.destroy("B", Fr(11, 2))
        self.assertEqual(
            w.collision_intervals(0, 20), {("A", "B"): [(Fr(5), Fr(11, 2))]}
        )


class TestVersionedEvents(unittest.TestCase):
    def test_turn_cancels_predicted_event(self):
        # A 与 B 预测 t=5 碰撞；t=3 时 A 转向，旧事件必须失效
        w = World()
        w.add_object("A", (0, 0), (1, 1), vel=(1, 0))
        w.add_object("B", (11, 0), (1, 1), vel=(-1, 0))
        self.assertEqual(w.first_collision(0, 10)[0], Fr(5))
        w.change_velocity("A", (0, 1), 3)
        self.assertIsNone(w.first_collision(0, 10))
        self.assertEqual(w.simulate(0, 10), [])

    def test_stale_prediction_dropped_after_response(self):
        # A-B 在 t=4 碰撞；响应把 A 停下；A-C 在 t=7 的旧预测不得触发
        w = World()
        w.add_object("A", (0, 0), (1, 1), vel=(1, 0))
        w.add_object("B", (5, 0), (1, 1))
        w.add_object("C", (8, 0), (1, 1))

        def respond(world, t, collisions):
            world.change_velocity("A", (0, 0), t)

        log = w.simulate(0, 20, respond=respond)
        self.assertEqual(len(log), 1)
        t, collisions = log[0]
        self.assertEqual(t, Fr(4))
        self.assertEqual([c.pair for c in collisions], [("A", "B")])

    def test_trajectory_replacement_after_collision(self):
        # 碰撞后整体替换轨迹（弹回），之后不应再有碰撞
        w = World()
        w.add_object("A", (0, 0), (1, 1), vel=(1, 0))
        w.add_object("B", (5, 0), (1, 1), vel=(-1, 0))

        def respond(world, t, collisions):
            for c in collisions:
                pos_a = world.position_of(c.pair[0], t)
                pos_b = world.position_of(c.pair[1], t)
                world.set_trajectory(c.pair[0], [(t, pos_a, (-1, 0))])
                world.set_trajectory(c.pair[1], [(t, pos_b, (1, 0))])

        log = w.simulate(0, 20, respond=respond)
        self.assertEqual(len(log), 1)
        self.assertEqual(log[0][0], Fr(2))

    def test_simultaneous_batch_three_body(self):
        # 三物体同刻碰撞：(A,B) 与 (B,C) 必须在同一批、基于同一前态
        w = World()
        w.add_object("A", (0, 0), (1, 1), vel=(1, 0))
        w.add_object("B", (2, 0), (1, 1))
        w.add_object("C", (4, 0), (1, 1), vel=(-1, 0))
        seen = []

        def respond(world, t, collisions):
            seen.append((t, [c.pair for c in collisions],
                         [c.pos_a for c in collisions]
                         + [c.pos_b for c in collisions]))
            for c in collisions:
                for oid in c.pair:
                    world.change_velocity(oid, (0, 0), t)

        log = w.simulate(0, 10, respond=respond)
        self.assertEqual(len(log), 1)
        t, collisions = log[0]
        self.assertEqual(t, Fr(1))
        self.assertEqual({c.pair for c in collisions}, {("A", "B"), ("B", "C")})
        # 前态位置：A 在 (1,0)，B 在 (2,0)，C 在 (3,0)
        positions = seen[0][2]
        self.assertIn((Fr(1), Fr(0)), positions)
        self.assertIn((Fr(3), Fr(0)), positions)

    def test_zero_time_loop_hits_budget(self):
        # 响应在同一时刻反复制造碰撞：事件预算必须终止循环
        w = World()
        w.add_object("A", (0, 0), (2, 2))
        w.add_object("B", (1, 0), (2, 2))

        def respond(world, t, collisions):
            for c in collisions:
                world.change_velocity(c.pair[0], (0, 0), t)

        with self.assertRaises(EventBudgetExceeded):
            w.simulate(0, 10, respond=respond, max_events=8)

    def test_event_budget_not_exceeded_in_normal_run(self):
        w = World()
        w.add_object("A", (0, 0), (1, 1), vel=(1, 0))
        w.add_object("B", (5, 0), (1, 1), vel=(-1, 0))
        log = w.simulate(0, 20, max_events=4)
        self.assertEqual(len(log), 1)


class TestReferenceCrossCheck(unittest.TestCase):
    def _random_world(self, rng):
        w = World()
        n = rng.randint(2, 4)
        for i in range(n):
            oid = f"o{i}"
            pos = (rng.randint(-5, 5), rng.randint(-5, 5))
            size = (rng.randint(1, 3), rng.randint(1, 3))
            vel = (rng.randint(-3, 3), rng.randint(-3, 3))
            w.add_object(oid, pos, size, vel=vel)
            t = 0
            for _ in range(rng.randint(0, 2)):
                t += rng.randint(1, 4)
                w.change_velocity(
                    oid, (rng.randint(-3, 3), rng.randint(-3, 3)), t
                )
        return w

    def test_intervals_match_reference(self):
        rng = random.Random(20260925)
        for _ in range(300):
            w = self._random_world(rng)
            got = w.collision_intervals(0, 12)
            exp = reference.reference_all_pairs(w, 0, 12)
            self.assertEqual(got, exp)
            # first_collision 与区间结果一致
            fc = w.first_collision(0, 12)
            if not exp:
                self.assertIsNone(fc)
            else:
                best = min(ivs[0][0] for ivs in exp.values())
                self.assertEqual(fc[0], best)
                self.assertEqual(
                    set(fc[1]),
                    {p for p, ivs in exp.items() if ivs[0][0] == best},
                )

    def test_sampled_oracle(self):
        # 独立采样判定：在非边界有理点上，直接判定重叠与区间成员关系一致
        rng = random.Random(777)
        for _ in range(150):
            w = self._random_world(rng)
            intervals = w.collision_intervals(0, 12)
            ids = sorted(w._objects)
            for i in range(len(ids)):
                for j in range(i + 1, len(ids)):
                    pair = (ids[i], ids[j])
                    ivs = intervals.get(pair, [])
                    oa, ob = w._objects[pair[0]], w._objects[pair[1]]
                    for k in range(1, 84):
                        t = Fr(7 * k + 3, 7)  # 非整数、分母 7，不会落在边界上
                        if t >= 12:
                            break
                        pa = oa.traj.position_at(t)
                        pb = ob.traj.position_at(t)
                        direct = all(
                            pa[d] < pb[d] + ob.size[d]
                            and pb[d] < pa[d] + oa.size[d]
                            for d in range(2)
                        )
                        inside = any(lo < t < hi for lo, hi in ivs)
                        self.assertEqual(
                            direct, inside, f"{pair} at t={t}: {ivs}"
                        )


if __name__ == "__main__":
    unittest.main()
