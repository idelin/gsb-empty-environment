import unittest
from fractions import Fraction as Fr

from stcollide.core import le_interval, lt_interval, piece_collision
from stcollide.trajectory import Trajectory


class TestLeInterval(unittest.TestCase):
    def test_zero_velocity_satisfied(self):
        self.assertEqual(le_interval(Fr(-1), Fr(0), Fr(0), Fr(10)), (Fr(0), Fr(10)))

    def test_zero_velocity_violated_never_collides(self):
        # 静止分离：永不碰撞
        self.assertIsNone(le_interval(Fr(1), Fr(0), Fr(0), Fr(10)))

    def test_positive_slope_root_inside(self):
        self.assertEqual(le_interval(Fr(-4), Fr(2), Fr(0), Fr(10)), (Fr(0), Fr(2)))

    def test_negative_slope_root_inside(self):
        self.assertEqual(le_interval(Fr(4), Fr(-2), Fr(0), Fr(10)), (Fr(2), Fr(10)))

    def test_root_exactly_at_boundary_is_included(self):
        self.assertEqual(le_interval(Fr(-20), Fr(2), Fr(0), Fr(10)), (Fr(0), Fr(10)))
        self.assertEqual(le_interval(Fr(20), Fr(-2), Fr(0), Fr(10)), (Fr(10), Fr(10)))

    def test_root_outside_window(self):
        self.assertIsNone(le_interval(Fr(2), Fr(2), Fr(0), Fr(10)))
        self.assertIsNone(le_interval(Fr(22), Fr(-2), Fr(0), Fr(10)))

    def test_rational_root_exact(self):
        self.assertEqual(le_interval(Fr(1), Fr(-3), Fr(0), Fr(10)),
                         (Fr(1, 3), Fr(10)))


class TestLtInterval(unittest.TestCase):
    def test_strict_excludes_root_on_boundary(self):
        self.assertEqual(lt_interval(Fr(-20), Fr(2), Fr(0), Fr(10)), (Fr(0), Fr(10)))
        self.assertIsNone(lt_interval(Fr(20), Fr(-2), Fr(0), Fr(10)))

    def test_zero_velocity_strict(self):
        self.assertEqual(lt_interval(Fr(-1), Fr(0), Fr(0), Fr(10)), (Fr(0), Fr(10)))
        self.assertIsNone(lt_interval(Fr(0), Fr(0), Fr(0), Fr(10)))

    def test_degenerate_empty_window(self):
        self.assertIsNone(lt_interval(Fr(-1), Fr(0), Fr(5), Fr(5)))


class TestPieceCollision(unittest.TestCase):
    def test_static_separated_never(self):
        hit = piece_collision((Fr(0),), (Fr(1),), (Fr(0),),
                              (Fr(5),), (Fr(1),), (Fr(0),), Fr(0), Fr(10))
        self.assertIsNone(hit)

    def test_static_overlapping_whole_piece(self):
        hit = piece_collision((Fr(0),), (Fr(2),), (Fr(0),),
                              (Fr(1),), (Fr(2),), (Fr(0),), Fr(0), Fr(10))
        self.assertEqual((hit.ta, hit.tb, hit.sa, hit.sb), (0, 10, 0, 10))

    def test_approach_and_pass(self):
        # A=[t, t+1], B=[2-t, 3-t]：1+t >= 2-t → t >= 1/2；t <= 3-t+... → t <= 3/2
        hit = piece_collision((Fr(0),), (Fr(1),), (Fr(1),),
                              (Fr(2),), (Fr(1),), (Fr(-1),), Fr(0), Fr(10))
        self.assertEqual((hit.ta, hit.tb), (Fr(1, 2), Fr(3, 2)))
        self.assertEqual((hit.sa, hit.sb), (Fr(1, 2), Fr(3, 2)))

    def test_pure_touch_single_instant(self):
        # B 沿 (-1,+1) 掠过 A 的角点 (1,1)：仅在 t=1 相切
        hit = piece_collision((Fr(0), Fr(0)), (Fr(1), Fr(1)), (Fr(0), Fr(0)),
                              (Fr(2), Fr(0)), (Fr(1), Fr(1)), (Fr(-1), Fr(1)),
                              Fr(0), Fr(4))
        self.assertEqual((hit.ta, hit.tb), (Fr(1), Fr(1)))
        self.assertIsNone(hit.sa)

    def test_gliding_contact_no_interior(self):
        # B 贴着 A 的上棱 (y=1) 滑过：持续接触但无内部相交
        hit = piece_collision((Fr(0), Fr(0)), (Fr(1), Fr(1)), (Fr(0), Fr(0)),
                              (Fr(2), Fr(1)), (Fr(1), Fr(1)), (Fr(-1), Fr(0)),
                              Fr(0), Fr(10))
        self.assertEqual((hit.ta, hit.tb), (Fr(1), Fr(3)))
        self.assertIsNone(hit.sa)

    def test_negative_velocities(self):
        # A=[-t, 1-t]，B=[-5+t, -4+t]：t in [2, 3]
        hit = piece_collision((Fr(0),), (Fr(1),), (Fr(-1),),
                              (Fr(-5),), (Fr(1),), (Fr(1),), Fr(0), Fr(10))
        self.assertEqual((hit.ta, hit.tb), (Fr(2), Fr(3)))


class TestTrajectory(unittest.TestCase):
    def test_piecewise_position(self):
        tr = Trajectory(0, (0,), (2,))
        tr.set_velocity(3, (-1,))
        self.assertEqual(tr.position(0), (Fr(0),))
        self.assertEqual(tr.position(3), (Fr(6),))
        self.assertEqual(tr.position(5), (Fr(4),))

    def test_replace_at_segment_start(self):
        tr = Trajectory(0, (0,), (1,))
        tr.set_velocity(2, (5,))
        tr.set_velocity(2, (-2,))
        self.assertEqual(tr.position(4), (Fr(-2),))

    def test_rational_velocity(self):
        tr = Trajectory(0, (0,), (Fr(1, 3),))
        self.assertEqual(tr.position(6), (Fr(2),))


if __name__ == "__main__":
    unittest.main()
