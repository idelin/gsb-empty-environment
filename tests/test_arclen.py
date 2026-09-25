import unittest
from fractions import Fraction as F

from arclen import (
    NonRationalLengthError,
    Polyline,
    Sampler,
    SeamRegistry,
    StaleCursorError,
    Vertex,
)


def mileages(sampler):
    return [s.mileage for s in sampler.samples]


def points(sampler):
    return [s.point for s in sampler.samples]


class TestEqualArcLengthGrid(unittest.TestCase):
    def test_length10_spacing3(self):
        s = Sampler(Polyline([(0, 0), (10, 0)]), 3)
        self.assertEqual(mileages(s), [F(0), F(3), F(6), F(9), F(10)])
        self.assertEqual(
            points(s), [(F(0), F(0)), (F(3), F(0)), (F(6), F(0)),
                        (F(9), F(0)), (F(10), F(0))])
        self.assertEqual([x.id for x in s.samples],
                         ["K0", "G0.1", "G0.2", "G0.3", "K1"])
        self.assertEqual([x.kind for x in s.samples],
                         ["key", "grid", "grid", "grid", "key"])
        self.assertEqual([x.t for x in s.samples],
                         [F(0), F(3, 10), F(3, 5), F(9, 10), F(1)])
        self.assertTrue(all(x.direction == (F(10), F(0)) for x in s.samples))
        self.assertEqual(s.total_length, F(10))

    def test_fractional_spacing_is_exact(self):
        s = Sampler(Polyline([(0, 0), (10, 0)]), F(5, 2))
        self.assertEqual(mileages(s), [F(0), F(5, 2), F(5), F(15, 2), F(10)])

    def test_cross_segment_carry_is_rational(self):
        # 5 + 20 = 35 with spacing 5: the grid crosses the segment join
        # exactly at mileage 15 with no float residue.
        s = Sampler(Polyline([(0, 0), (9, 12), (25, 0)]), 5)
        at_joint = s.samples[3]
        self.assertEqual(at_joint.id, "G0.3")
        self.assertEqual(at_joint.mileage, F(15))
        self.assertEqual(at_joint.point, (F(9), F(12)))
        self.assertEqual(at_joint.seg, 1)
        self.assertEqual(at_joint.t, F(0))
        self.assertEqual(s.total_length, F(35))

    def test_3d_points(self):
        s = Sampler(Polyline([(0, 0, 0), (2, 3, 6)]), 2)  # length 7
        self.assertEqual(mileages(s), [F(0), F(2), F(4), F(6), F(7)])
        self.assertEqual(s.samples[1].point, (F(4, 7), F(6, 7), F(12, 7)))


class TestMoveVertexPatch(unittest.TestCase):
    def setUp(self):
        # A(0,0) - M(9,12) - B(25,0): lengths 15 + 20 = 35, single run.
        self.poly = Polyline([(0, 0), (9, 12), (25, 0)])
        self.sampler = Sampler(self.poly, 5)
        self.original = list(self.sampler.samples)

    def test_initial_grid(self):
        self.assertEqual(
            points(self.sampler),
            [(F(0), F(0)), (F(3), F(4)), (F(6), F(8)), (F(9), F(12)),
             (F(13), F(9)), (F(17), F(6)), (F(21), F(3)), (F(25), F(0))])

    def test_move_midpoint_produces_additions(self):
        # M -> (7,24): lengths become 25 + 30 = 55, delta = +20.
        patch = self.sampler.move_vertex(1, (7, 24))
        self.assertEqual(patch.mileage_delta, F(20))
        self.assertEqual(patch.affected_range, (F(0), F(35)))
        removed_ids = {s.id for s in patch.removed}
        added_ids = {s.id for s in patch.added}
        # brand-new grid samples appear (run got longer)
        self.assertTrue({"G0.7", "G0.8", "G0.9", "G0.10"} <= added_ids)
        # the start anchor keeps its point and mileage (only its recorded
        # outgoing direction changed with the moved segment)
        for k0 in [s for s in patch.added if s.id == "K0"]:
            self.assertEqual(k0.point, (F(0), F(0)))
            self.assertEqual(k0.mileage, F(0))
        # old grid points and the shifted end anchor are retired
        self.assertTrue({"G0.1", "G0.2", "G0.3", "G0.4", "G0.5", "G0.6", "K2"}
                        <= removed_ids)
        self.assertEqual(len(self.sampler.samples), 12)
        self.assertEqual(self.sampler.total_length, F(55))
        self.assertEqual(mileages(self.sampler)[-1], F(55))
        self.assertEqual(self.sampler.samples[1].point, (F(7, 5), F(24, 5)))

    def test_local_update_matches_full_resampling(self):
        self.sampler.move_vertex(1, (7, 24))
        fresh = Sampler(self.sampler.polyline, 5)
        self.assertEqual(self.sampler.samples, fresh.samples)

    def test_undo_restores_exact_state(self):
        patch = self.sampler.move_vertex(1, (7, 24))
        self.assertNotEqual(self.sampler.samples, self.original)
        self.sampler.undo(patch)
        self.assertEqual(self.sampler.samples, self.original)
        self.assertEqual(self.sampler.total_length, F(35))
        self.assertEqual(self.sampler.polyline, self.poly)

    def test_undo_requires_lifo_order(self):
        patch = self.sampler.move_vertex(1, (7, 24))
        self.sampler.undo(patch)
        with self.assertRaises(RuntimeError):
            self.sampler.undo(patch)


class TestKeyVertexLocality(unittest.TestCase):
    def test_move_key_vertex_repairs_only_adjacent_runs(self):
        # key vertices at M and B split the path into three runs.
        poly = Polyline([
            Vertex((0, 0)),
            Vertex((9, 12), key=True),
            Vertex((25, 0), key=True),
            Vertex((25, 10)),
        ])
        s = Sampler(poly, 5)
        before = {x.id: x for x in s.samples}
        self.assertEqual(mileages(s),
                         [F(0), F(5), F(10), F(15), F(20), F(25), F(30),
                          F(35), F(40), F(45)])

        patch = s.move_vertex(1, (7, 24))  # runs become 25 + 30 + 10
        self.assertEqual(patch.mileage_delta, F(20))
        self.assertEqual(patch.affected_range, (F(0), F(35)))

        # the moved key sample itself is part of the patch
        moved = {x.id: x for x in patch.added}
        self.assertEqual(moved["K1"].point, (F(7), F(24)))
        self.assertEqual(moved["K1"].mileage, F(25))

        # the third run is outside the affected arc-length range: its
        # samples keep identical ids and points, only mileage shifts by +20
        after = {x.id: x for x in s.samples}
        for sample_id in ("G2.1", "K3"):
            self.assertEqual(after[sample_id].point, before[sample_id].point)
            self.assertEqual(after[sample_id].mileage,
                             before[sample_id].mileage + 20)
            self.assertNotIn(sample_id, {x.id for x in patch.removed})
            self.assertNotIn(sample_id, {x.id for x in patch.added})

        # independent full resampling verifies the local update
        fresh = Sampler(s.polyline, 5)
        self.assertEqual(s.samples, fresh.samples)

        s.undo(patch)
        self.assertEqual(s.samples, list(before.values()))


class TestZeroLengthAndBreakpoints(unittest.TestCase):
    def test_no_cross_interpolation(self):
        poly = Polyline([
            Vertex((0, 0)),                    # 0: A
            Vertex((0, 0)),                    # 1: Z, zero-length segment A->Z
            Vertex((6, 0)),                    # 2: B
            Vertex((100, 100), break_before=True),  # 3: C, gap before it
            Vertex((103, 104)),                # 4: D (segment C->D has length 5)
        ])
        s = Sampler(poly, 4)
        self.assertEqual(mileages(s),
                         [F(0), F(4), F(6), F(6), F(10), F(11)])
        self.assertEqual([x.id for x in s.samples],
                         ["K0", "G0.1", "K2", "K3", "G3.1", "K4"])
        # the zero-length segment 0 owns no interpolated sample
        self.assertTrue(all(x.seg != 0 for x in s.samples if x.kind == "grid"))
        # nothing is interpolated across the break: every sample lies on
        # one of the two real subpaths
        for x in s.samples:
            self.assertTrue(x.point[0] <= 6 or x.point[0] >= 100, x)
        # the break contributes zero mileage: both sides sit at mileage 6
        self.assertEqual(s.samples[2].mileage, F(6))
        self.assertEqual(s.samples[3].mileage, F(6))
        # exact interpolation after the zero-length segment
        g = s.samples[1]
        self.assertEqual(g.point, (F(4), F(0)))
        self.assertEqual(g.seg, 1)
        self.assertEqual(g.t, F(2, 3))
        self.assertEqual(g.direction, (F(6), F(0)))
        # subpath after the break starts with its own key sample
        self.assertEqual(s.samples[3].point, (F(100), F(100)))
        self.assertEqual(s.samples[4].point, (F(512, 5), F(516, 5)))


class TestSharedEdges(unittest.TestCase):
    def setUp(self):
        self.U = (3, 4)
        self.V = (15, 9)  # shared edge length 13
        self.registry = SeamRegistry()
        self.p1 = Polyline(
            [(0, 0), self.U, self.V, (18, 13)], shared={1})
        self.p2 = Polyline(
            [(15, 4), self.V, self.U, (0, 8)], shared={1})  # reversed edge
        self.s1 = Sampler(self.p1, 4, self.registry)
        self.s2 = Sampler(self.p2, 4, self.registry)

    def edge_samples(self, sampler):
        return [x for x in sampler.samples if x.id.startswith("E")]

    def test_reverse_traversal_gives_reversed_identical_points(self):
        e1 = self.edge_samples(self.s1)
        e2 = self.edge_samples(self.s2)
        self.assertEqual(len(e1), 3)
        self.assertEqual([x.point for x in e1],
                         [x.point for x in reversed(e2)])
        self.assertEqual({x.id for x in e1}, {x.id for x in e2})
        # exact canonical points, no independent rounding anywhere
        self.assertEqual(e1[0].point, (F(87, 13), F(72, 13)))
        self.assertEqual(e1[0].t, F(4, 13))
        # source parameters and direction follow the traversal direction
        self.assertEqual(e1[0].direction, (F(12), F(5)))
        self.assertEqual(e2[-1].direction, (F(-12), F(-5)))
        self.assertEqual(e2[-1].t, 1 - e1[0].t)

    def test_edge_is_computed_once_for_all_polylines(self):
        self.assertEqual(self.registry.computations, 1)
        self.assertEqual({x.id for x in self.edge_samples(self.s1)},
                         {"E0.1", "E0.2", "E0.3"})

    def test_shared_edge_requires_registry(self):
        with self.assertRaises(ValueError):
            Sampler(self.p1, 4)


class TestBudgetCursor(unittest.TestCase):
    def test_budget_returns_resumable_cursor(self):
        s = Sampler(Polyline([(0, 0), (10, 0)]), 3)
        b1 = s.sample(budget=2)
        self.assertFalse(b1.done)
        self.assertIsNotNone(b1.cursor)  # never silently drops points
        b2 = s.sample(budget=2, cursor=b1.cursor)
        self.assertFalse(b2.done)
        b3 = s.sample(budget=2, cursor=b2.cursor)
        self.assertTrue(b3.done)
        self.assertIsNone(b3.cursor)
        joined = b1.samples + b2.samples + b3.samples
        self.assertEqual(joined, s.samples)
        self.assertEqual([x.mileage for x in joined],
                         [F(0), F(3), F(6), F(9), F(10)])

    def test_generous_budget_completes(self):
        s = Sampler(Polyline([(0, 0), (10, 0)]), 3)
        batch = s.sample(budget=100)
        self.assertTrue(batch.done)
        self.assertEqual(len(batch.samples), 5)

    def test_cursor_invalidated_by_edit(self):
        s = Sampler(Polyline([(0, 0), (9, 12), (25, 0)]), 5)
        batch = s.sample(budget=1)
        s.move_vertex(1, (7, 24))
        with self.assertRaises(StaleCursorError):
            s.sample(cursor=batch.cursor)


class TestClosedPath(unittest.TestCase):
    def test_fixed_start_and_no_duplicate(self):
        square = Polyline([(0, 0), (4, 0), (4, 4), (0, 4)], closed=True)
        s = Sampler(square, 3)
        self.assertEqual(s.total_length, F(16))
        self.assertEqual(mileages(s), [F(0), F(3), F(6), F(9), F(12), F(15)])
        self.assertEqual(points(s), [(F(0), F(0)), (F(3), F(0)), (F(4), F(2)),
                                     (F(3), F(4)), (F(0), F(4)), (F(0), F(1))])
        first = s.samples[0]
        self.assertEqual((first.id, first.point), ("K0", (F(0), F(0))))
        self.assertEqual(sum(1 for p in points(s) if p == (F(0), F(0))), 1)


class TestDegenerateCases(unittest.TestCase):
    def test_empty_polyline(self):
        s = Sampler(Polyline([]), 3)
        self.assertEqual(s.samples, [])
        self.assertEqual(s.total_length, F(0))
        self.assertTrue(s.sample(budget=5).done)

    def test_single_vertex(self):
        s = Sampler(Polyline([(2, 3)]), 3)
        self.assertEqual(len(s.samples), 1)
        only = s.samples[0]
        self.assertEqual((only.id, only.mileage, only.point),
                         ("K0", F(0), (F(2), F(3))))
        self.assertIsNone(only.seg)
        self.assertIsNone(only.direction)

    def test_all_zero_length_segments(self):
        s = Sampler(Polyline([(1, 1), (1, 1), (1, 1)]), 3)
        self.assertEqual([x.id for x in s.samples], ["K0", "K2"])
        self.assertEqual(mileages(s), [F(0), F(0)])

    def test_irrational_length_is_a_defined_error(self):
        with self.assertRaises(NonRationalLengthError):
            Sampler(Polyline([(0, 0), (1, 1)]), 1)

    def test_irrational_length_after_move_is_a_defined_error(self):
        s = Sampler(Polyline([(0, 0), (10, 0)]), 3)
        with self.assertRaises(NonRationalLengthError):
            s.move_vertex(1, (1, 1))
        self.assertEqual(mileages(s), [F(0), F(3), F(6), F(9), F(10)])

    def test_spacing_larger_than_length(self):
        s = Sampler(Polyline([(0, 0), (10, 0)]), 30)
        self.assertEqual([x.id for x in s.samples], ["K0", "K1"])

    def test_invalid_inputs_are_rejected(self):
        with self.assertRaises(ValueError):
            Sampler(Polyline([(0, 0), (10, 0)]), 0)
        with self.assertRaises(TypeError):
            Sampler(Polyline([(0, 0), (10, 0)]), 2.5)
        with self.assertRaises(TypeError):
            Polyline([(0.0, 0.0), (10, 0)])


if __name__ == "__main__":
    unittest.main()
