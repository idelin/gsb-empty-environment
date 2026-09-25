import unittest

from plan import EditPlan


BASE = [(0.0, 0.0), (1.0, 10.0), (2.0, 20.0), (3.0, 30.0)]


class TestPlan(unittest.TestCase):
    def test_revision_keeps_old_plan_readers(self):
        old = EditPlan.from_control_points(BASE)
        new_points = [(0.0, 0.0), (1.0, 12.0), (2.0, 20.0), (3.0, 30.0)]
        new = old.revise(new_points)
        # Old plan (and any reader holding it) still sees the old mapping.
        self.assertEqual(old.time_map.source_at(0.5), 5.0)
        self.assertEqual(new.time_map.source_at(0.5), 6.0)
        self.assertIsNot(old.time_map, new.time_map)

    def test_identical_plans_have_no_affected_range(self):
        a = EditPlan.from_control_points(BASE)
        b = EditPlan.from_control_points(list(BASE))
        self.assertIsNone(a.affected_output_range(b))

    def test_affected_range_localises_control_point_edit(self):
        old = EditPlan.from_control_points(BASE)
        # Move the control point at out_t=1: only the two adjacent segments
        # (output times 0..2) change; the segment 2..3 is untouched.
        new = old.revise([(0.0, 0.0), (1.0, 12.0), (2.0, 20.0), (3.0, 30.0)])
        self.assertEqual(old.affected_output_range(new), (0.0, 2.0))

    def test_affected_range_single_segment_edit(self):
        old = EditPlan.from_control_points(BASE)
        new = old.revise([(0.0, 0.0), (1.0, 10.0), (2.0, 25.0), (3.0, 30.0)])
        self.assertEqual(old.affected_output_range(new), (1.0, 3.0))

    def test_affected_range_with_appended_segment(self):
        old = EditPlan.from_control_points(BASE)
        new = old.revise(BASE + [(4.0, 0.0)])  # appended reverse leg
        self.assertEqual(old.affected_output_range(new), (3.0, 4.0))

    def test_compound_edit_is_not_a_single_speed(self):
        # Forward, then freeze, then reverse: no single rate can describe it.
        plan = EditPlan.from_control_points(
            [(0.0, 0.0), (1.0, 10.0), (2.0, 10.0), (3.0, 4.0)]
        )
        slopes = [s.slope for s in plan.time_map.segments]
        self.assertEqual(len(set(slopes)), 3)
        self.assertTrue(any(s == 0.0 for s in slopes))
        self.assertTrue(any(s < 0.0 for s in slopes))


if __name__ == "__main__":
    unittest.main()
