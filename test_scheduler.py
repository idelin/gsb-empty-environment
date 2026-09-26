import io
import json
import unittest
from contextlib import redirect_stdout
from fractions import Fraction
from pathlib import Path

import scheduler
from scheduler import fmt, merge_intervals, overlaps, parse_number, solve

EXAMPLES = Path(__file__).parent / "examples"


def make_instance(**overrides):
    base = {
        "operation": {"id": "op", "duration": 2, "pre_buffer": 0, "post_buffer": 0},
        "horizon": {"start": 0, "end": 10},
        "tracks": [{"id": "T1"}],
    }
    base.update(overrides)
    return base


class TestNumbers(unittest.TestCase):
    def test_parse_int_float_str_ratio(self):
        self.assertEqual(parse_number(3), Fraction(3))
        self.assertEqual(parse_number("3/4"), Fraction(3, 4))
        self.assertEqual(parse_number("1.5"), Fraction(3, 2))
        self.assertEqual(parse_number(0.25), Fraction(1, 4))

    def test_parse_rejects_garbage(self):
        with self.assertRaises(ValueError):
            parse_number("abc")
        with self.assertRaises(ValueError):
            parse_number(True)

    def test_fmt(self):
        self.assertEqual(fmt(Fraction(4)), "4")
        self.assertEqual(fmt(Fraction(5, 2)), "5/2")
        self.assertIsNone(fmt(None))


class TestHalfOpenSemantics(unittest.TestCase):
    def test_overlap_touching_is_not_overlap(self):
        self.assertFalse(overlaps(Fraction(0), Fraction(4), Fraction(4), Fraction(6)))
        self.assertTrue(overlaps(Fraction(0), Fraction(4), Fraction(3), Fraction(6)))

    def test_operation_may_touch_maintenance_boundary(self):
        # Maintenance [4,6); op duration 4 fills [0,4) and touches the window.
        inst = make_instance(
            operation={"id": "op", "duration": 4},
            tracks=[{"id": "T1", "maintenance": [[4, 6]]}],
        )
        res = solve(inst)
        self.assertTrue(res["feasible"])
        self.assertEqual(res["optimal"]["start"], "0")
        self.assertEqual(res["optimal"]["end"], "4")

    def test_operation_may_start_at_maintenance_end(self):
        inst = make_instance(
            horizon={"start": 0, "end": 8},
            tracks=[{"id": "T1", "maintenance": [[0, 6]]}],
        )
        res = solve(inst)
        self.assertTrue(res["feasible"])
        self.assertEqual(res["optimal"]["start"], "6")

    def test_adjacent_windows_merge(self):
        merged = merge_intervals([(0, 5, "a"), (5, 12, "b")])
        self.assertEqual([(s, e) for s, e, _ in merged], [(0, 12)])
        self.assertEqual(sorted(merged[0][2]), ["a", "b"])


class TestBuffersConsumeCapacity(unittest.TestCase):
    def test_reserved_length_includes_buffers(self):
        # Horizon exactly fits duration + buffers: feasible, start after pre-buffer.
        inst = make_instance(
            operation={"id": "op", "duration": 3, "pre_buffer": 1, "post_buffer": 1},
            horizon={"start": 0, "end": 5},
        )
        res = solve(inst)
        self.assertTrue(res["feasible"])
        self.assertEqual(res["optimal"]["start"], "1")
        self.assertEqual(res["optimal"]["reserved_start"], "0")
        self.assertEqual(res["optimal"]["reserved_end"], "5")

    def test_gap_of_exact_duration_is_not_enough(self):
        # Gap length 3 fits duration 3 but not duration + buffers (5).
        inst = make_instance(
            operation={"id": "op", "duration": 3, "pre_buffer": 1, "post_buffer": 1},
            horizon={"start": 0, "end": 3},
        )
        res = solve(inst)
        self.assertFalse(res["feasible"])

    def test_booking_buffers_block_capacity(self):
        # Booking [2,4) with buffers occupies [1,5); op (dur 2) starts at 5.
        inst = make_instance(
            horizon={"start": 0, "end": 8},
            tracks=[{"id": "T1",
                     "bookings": [{"start": 2, "end": 4,
                                   "pre_buffer": 1, "post_buffer": 1}]}],
        )
        res = solve(inst)
        self.assertTrue(res["feasible"])
        self.assertEqual(res["optimal"]["start"], "5")

    def test_start_offset_by_pre_buffer(self):
        inst = make_instance(
            operation={"id": "op", "duration": 2, "pre_buffer": 1, "post_buffer": 0},
            tracks=[{"id": "T1", "maintenance": [[0, 3]]}],
        )
        res = solve(inst)
        self.assertEqual(res["optimal"]["start"], "4")  # gap starts at 3, +1 pre


class TestRationals(unittest.TestCase):
    def test_rational_schedule(self):
        inst = make_instance(
            operation={"id": "op", "duration": "3/2",
                       "pre_buffer": "1/4", "post_buffer": "1/4"},
            tracks=[{"id": "T1", "maintenance": [["0", "1"]]}],
        )
        res = solve(inst)
        self.assertTrue(res["feasible"])
        self.assertEqual(res["optimal"]["start"], "5/4")
        self.assertEqual(res["optimal"]["end"], "11/4")
        self.assertEqual(res["optimal"]["reserved_start"], "1")
        self.assertEqual(res["optimal"]["reserved_end"], "3")

    def test_rational_gap_too_short_by_exact_fraction(self):
        # Gap [0, 7/4) is shorter than required 2 by exactly 1/4.
        inst = make_instance(
            operation={"id": "op", "duration": "3/2",
                       "pre_buffer": "1/4", "post_buffer": "1/4"},
            horizon={"start": 0, "end": "7/4"},
        )
        self.assertFalse(solve(inst)["feasible"])


class TestOptimalAndTracks(unittest.TestCase):
    def test_earliest_start_across_tracks(self):
        inst = make_instance(tracks=[
            {"id": "TA", "maintenance": [[0, 5]]},
            {"id": "TB", "maintenance": [[0, 2]]},
        ])
        res = solve(inst)
        self.assertEqual(res["optimal"]["track"], "TB")
        self.assertEqual(res["optimal"]["start"], "2")
        self.assertEqual([c["track"] for c in res["candidates"]], ["TB", "TA"])

    def test_tie_break_by_track_id(self):
        inst = make_instance(tracks=[{"id": "TZ"}, {"id": "TA"}])
        res = solve(inst)
        self.assertEqual(res["optimal"]["track"], "TA")

    def test_empty_track_set_infeasible(self):
        res = solve(make_instance(tracks=[]))
        self.assertFalse(res["feasible"])
        self.assertIsNone(res["optimal"])
        self.assertIn("empty", res["evidence"]["reason"])
        self.assertEqual(res["budget_status"], "undecided")

    def test_missing_tracks_key_infeasible(self):
        inst = make_instance()
        del inst["tracks"]
        self.assertFalse(solve(inst)["feasible"])

    def test_maintenance_outside_horizon_ignored(self):
        inst = make_instance(
            horizon={"start": 0, "end": 4},
            tracks=[{"id": "T1", "maintenance": [[10, 20], [-5, -1]]}],
        )
        res = solve(inst)
        self.assertTrue(res["feasible"])
        self.assertEqual(res["optimal"]["start"], "0")

    def test_unbounded_horizon(self):
        inst = make_instance(horizon={"start": 0, "end": None},
                             tracks=[{"id": "T1", "maintenance": [[0, 3]]}])
        res = solve(inst)
        self.assertTrue(res["feasible"])
        self.assertEqual(res["optimal"]["start"], "3")


class TestBudget(unittest.TestCase):
    def test_budget_met(self):
        inst = make_instance(budget=2)
        self.assertEqual(solve(inst)["budget_status"], "met")

    def test_budget_violated(self):
        inst = make_instance(budget="3/2")
        res = solve(inst)
        self.assertTrue(res["feasible"])
        self.assertEqual(res["budget_status"], "violated")

    def test_budget_undecided_when_absent(self):
        self.assertEqual(solve(make_instance())["budget_status"], "undecided")

    def test_budget_boundary_is_met(self):
        inst = make_instance(budget=2)  # op end == budget exactly
        self.assertEqual(solve(inst)["budget_status"], "met")


class TestEvidence(unittest.TestCase):
    def test_infeasible_evidence_details(self):
        inst = make_instance(
            operation={"id": "op", "duration": 4, "pre_buffer": 1, "post_buffer": 1},
            horizon={"start": 0, "end": 12},
            tracks=[
                {"id": "T1", "maintenance": [[3, 7]],
                 "bookings": [{"start": 8, "end": 10, "post_buffer": 1}]},
                {"id": "T2", "maintenance": [[0, 5], [5, 12]]},
            ],
        )
        res = solve(inst)
        self.assertFalse(res["feasible"])
        ev = res["evidence"]
        self.assertEqual(ev["required_reserved_length"], "6")
        self.assertEqual(len(ev["tracks"]), 2)

        t1 = ev["tracks"][0]
        self.assertEqual(t1["track"], "T1")
        # Forbidden: maintenance [3,7) and booking reserved [8,11).
        self.assertEqual([f["interval"] for f in t1["forbidden"]],
                         [["3", "7"], ["8", "11"]])
        self.assertIn("maintenance[3,7)", t1["forbidden"][0]["sources"])
        # Gaps [0,3) and [7,8) and [11,12): none fits length 6.
        self.assertEqual([g["length"] for g in t1["gaps"]], ["3", "1", "1"])
        self.assertTrue(all(not g["fits"] for g in t1["gaps"]))
        self.assertIn("no free gap fits", t1["reason"])

        t2 = ev["tracks"][1]
        # Adjacent maintenance windows merge into [0,12): no gap at all.
        self.assertEqual([f["interval"] for f in t2["forbidden"]], [["0", "12"]])
        self.assertEqual(t2["gaps"], [])

    def test_feasible_has_no_evidence(self):
        self.assertIsNone(solve(make_instance())["evidence"])


class TestValidation(unittest.TestCase):
    def test_nonpositive_duration(self):
        with self.assertRaises(ValueError):
            solve(make_instance(operation={"id": "op", "duration": 0}))

    def test_negative_buffer(self):
        with self.assertRaises(ValueError):
            solve(make_instance(
                operation={"id": "op", "duration": 1, "pre_buffer": -1}))

    def test_bad_maintenance_window(self):
        with self.assertRaises(ValueError):
            solve(make_instance(tracks=[{"id": "T1", "maintenance": [[5, 5]]}]))

    def test_bad_horizon(self):
        with self.assertRaises(ValueError):
            solve(make_instance(horizon={"start": 5, "end": 5}))


class TestCli(unittest.TestCase):
    def run_cli(self, path):
        buf = io.StringIO()
        with redirect_stdout(buf):
            code = scheduler.main([str(path)])
        return code, json.loads(buf.getvalue())

    def test_example_feasible(self):
        code, res = self.run_cli(EXAMPLES / "feasible.json")
        self.assertEqual(code, 0)
        self.assertTrue(res["feasible"])

    def test_example_infeasible_exit_code(self):
        code, res = self.run_cli(EXAMPLES / "infeasible_empty_tracks.json")
        self.assertEqual(code, 1)
        self.assertFalse(res["feasible"])

    def test_cli_bad_file(self):
        buf = io.StringIO()
        with redirect_stdout(buf):
            code = scheduler.main(["/nonexistent/instance.json"])
        self.assertEqual(code, 2)


if __name__ == "__main__":
    unittest.main()
