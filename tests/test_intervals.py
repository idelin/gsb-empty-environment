import unittest
from fractions import Fraction as Fr

from stcollide import World


def make(dim=1, t1=10, **kw):
    return World(dim=dim, t0=0, t1=t1, **kw)


class TestIntervalKinds(unittest.TestCase):
    def test_stationary_separated_never_collide(self):
        w = make()
        w.add_object("a", (0,), (1,), (0,))
        w.add_object("b", (5,), (6,), (0,))
        self.assertEqual(w.collision_intervals(), {})
        self.assertIsNone(w.first_collision())

    def test_initial_overlap_is_overlap_from_t0(self):
        w = make()
        w.add_object("a", (0,), (2,), (0,))
        w.add_object("b", (1,), (3,), (0,))
        (iv,) = w.collision_intervals()[("a", "b")]
        self.assertEqual((iv.start, iv.end, iv.kind, iv.enter),
                         (Fr(0), Fr(10), "overlap", Fr(0)))

    def test_instantaneous_touch_is_distinct(self):
        w = make(dim=2, t1=4)
        w.add_object("a", (0, 0), (1, 1), (0, 0))
        w.add_object("b", (2, 0), (3, 1), (-1, 1))  # 掠过角点 (1,1)
        (iv,) = w.collision_intervals()[("a", "b")]
        self.assertEqual((iv.start, iv.end, iv.kind), (Fr(1), Fr(1), "touch"))
        self.assertIsNone(iv.enter)
        t, pair, kind = w.first_collision()
        self.assertEqual((t, kind), (Fr(1), "touch"))

    def test_gliding_contact_is_distinct(self):
        w = make(dim=2)
        w.add_object("a", (0, 0), (1, 1), (0, 0))
        w.add_object("b", (2, 1), (3, 2), (-1, 0))  # 贴上棱滑移
        (iv,) = w.collision_intervals()[("a", "b")]
        self.assertEqual((iv.start, iv.end, iv.kind), (Fr(1), Fr(3), "contact"))

    def test_negative_velocities_interval(self):
        w = make()
        w.add_object("a", (0,), (1,), (-1,))
        w.add_object("b", (-5,), (-4,), (1,))
        (iv,) = w.collision_intervals()[("a", "b")]
        self.assertEqual((iv.start, iv.end, iv.kind, iv.enter),
                         (Fr(2), Fr(3), "overlap", Fr(2)))

    def test_rational_crossing_time(self):
        w = make()
        w.add_object("a", (0,), (1,), (Fr(1, 3),))
        w.add_object("b", (2,), (3,), (Fr(-1, 3),))
        (iv,) = w.collision_intervals()[("a", "b")]
        # 1 + t/3 = 2 - t/3 → t = 3/2；离开：t/3 = 3 - t/3 → t = 9/2
        self.assertEqual((iv.start, iv.end), (Fr(3, 2), Fr(9, 2)))

    def test_velocity_change_splits_trajectory(self):
        w = make()
        w.add_object("a", (0,), (1,), (2,))
        w.set_velocity("a", 2, (-1,))  # t=2 时位于 4，掉头
        w.add_object("b", (8,), (9,), (0,))
        # a_hi: t<2 时 1+2t（t=2 时为 5），之后 7-t，永远到不了 8
        self.assertEqual(w.collision_intervals(), {})

    def test_intervals_merge_across_segments(self):
        w = make()
        w.add_object("a", (0,), (1,), (1,))
        w.set_velocity("a", 3, (2,))  # 碰撞窗口跨过变速点
        w.add_object("b", (3,), (4,), (0,))
        ivs = w.collision_intervals()[("a", "b")]
        # a_hi: t<3 → 1+t；t>=3 → 4+2(t-3)=2t-2。进入：1+t=3 → t=2
        # 离开：a_lo 过 b_hi=4：t<3 时 t=4 不在段内；t>=3 时 3+2(t-3)=4 → t=7/2
        self.assertEqual(len(ivs), 1)
        self.assertEqual((ivs[0].start, ivs[0].end, ivs[0].kind),
                         (Fr(2), Fr(7, 2), "overlap"))


class TestDestroySemantics(unittest.TestCase):
    def test_destroy_truncates_interval(self):
        w = make()
        w.add_object("a", (0,), (1,), (1,))
        w.add_object("b", (3,), (4,), (0,))
        w.destroy("b", 2)  # 碰撞本为 [3,4]，销毁于 t=2
        self.assertEqual(w.collision_intervals(), {})

    def test_destroy_mid_overlap_clips_end(self):
        w = make()
        w.add_object("a", (0,), (1,), (1,))
        w.add_object("b", (3,), (4,), (0,))
        w.destroy("b", Fr(7, 2))
        (iv,) = w.collision_intervals()[("a", "b")]
        self.assertEqual((iv.start, iv.end, iv.kind), (Fr(2), Fr(7, 2), "overlap"))

    def test_touch_exactly_at_destroy_not_counted(self):
        w = make(dim=2, t1=4)
        w.add_object("a", (0, 0), (1, 1), (0, 0))
        w.add_object("b", (2, 0), (3, 1), (-1, 1))  # t=1 相切
        w.destroy("b", 1)
        self.assertEqual(w.collision_intervals(), {})

    def test_touch_at_window_end_counts(self):
        w = make(dim=2, t1=1)
        w.add_object("a", (0, 0), (1, 1), (0, 0))
        w.add_object("b", (2, 0), (3, 1), (-1, 1))  # t=1 相切，恰在窗口终点
        (iv,) = w.collision_intervals()[("a", "b")]
        self.assertEqual((iv.start, iv.kind), (Fr(1), "touch"))


if __name__ == "__main__":
    unittest.main()
