from decimal import Decimal
from fractions import Fraction
import unittest

from correctable_resampler import (
    CorrectableResampler,
    Mode,
    RevisionsBlocked,
    SealedIntervalError,
    UnknownReason,
)


class CoreSemanticsTest(unittest.TestCase):
    def test_linear_grid_two_is_four(self):
        resampler = CorrectableResampler(0, 1, Mode.LINEAR, 10)
        resampler.ingest("a", 0, 0)
        resampler.ingest("b", 4, 8)
        point = resampler.point(2)
        self.assertTrue(point.valid)
        self.assertEqual(point.basis, "linear")
        self.assertEqual(point.value, Decimal("4.000000"))
        self.assertEqual(point.sample_ids, ("a", "b"))
        self.assertEqual(point.time, Fraction(2))

    def test_breakpoint_prevents_linear_crossing(self):
        resampler = CorrectableResampler(0, 1, Mode.LINEAR, 10)
        resampler.ingest("a", 0, 0)
        resampler.ingest("b", 4, 8)
        resampler.add_breakpoint(2)
        point = resampler.point(2)
        self.assertFalse(point.valid)
        self.assertIsNone(point.value)
        self.assertEqual(point.reason, UnknownReason.BREAKPOINT)
        self.assertEqual(point.sample_ids, ("a", "b"))

    def test_withdraw_right_sample_makes_output_unknown(self):
        resampler = CorrectableResampler(0, 1, Mode.LINEAR, 10)
        resampler.ingest("a", 0, 0)
        resampler.ingest("b", 4, 8)
        resampler.watch([2])
        revisions = resampler.retract("b")
        self.assertEqual(len(revisions), 1)
        self.assertTrue(revisions[0].before.valid)
        self.assertFalse(revisions[0].after.valid)
        self.assertEqual(revisions[0].after.reason, UnknownReason.AFTER_LAST_SAMPLE)
        self.assertEqual(revisions[0].after.sample_ids, ("a",))
        self.assertEqual(resampler.drain_revisions()[0].index, 2)

    def test_non_integer_period_negative_time_and_endpoints(self):
        resampler = CorrectableResampler("-1.5", "0.25", Mode.LINEAR, "3.0")
        resampler.ingest("a", "-1.5", 0)
        resampler.ingest("b", "0.75", 9)
        first = resampler.point(0)
        middle = resampler.point(5)
        end = resampler.point(9)
        self.assertEqual(first.time, Fraction("-3/2"))
        self.assertEqual(first.value, Decimal("0.000000"))
        self.assertEqual(middle.time, Fraction(-0.25))
        self.assertEqual(middle.value, Decimal("5.000000"))
        self.assertEqual(end.time, Fraction(3, 4))
        self.assertEqual(end.basis, "observed")
        self.assertEqual(end.value, Decimal("9.000000"))

    def test_same_time_latest_sample_wins_and_retracts_repair(self):
        resampler = CorrectableResampler(0, 1, Mode.STEP, 10)
        resampler.ingest("old", 2, 3)
        resampler.ingest("new", 2, 7)
        point = resampler.point(2)
        self.assertEqual(point.sample_ids, ("new",))
        self.assertEqual(point.value, Decimal("7.000000"))
        resampler.retract("new")
        repaired = resampler.point(2)
        self.assertEqual(repaired.sample_ids, ("old",))
        self.assertEqual(repaired.value, Decimal("3.000000"))

    def test_right_continuous_step_respects_breakpoint_and_limit(self):
        resampler = CorrectableResampler(0, 1, Mode.STEP, 2)
        resampler.ingest("a", 0, 5)
        self.assertEqual(resampler.point(2).value, Decimal("5.000000"))
        self.assertFalse(resampler.point(3).valid)
        self.assertEqual(resampler.point(3).reason, UnknownReason.GAP_TOO_LONG)
        resampler.add_breakpoint(1)
        blocked = resampler.point(1)
        self.assertFalse(blocked.valid)
        self.assertEqual(blocked.reason, UnknownReason.BREAKPOINT)
        resampler.ingest("b", 1, 9)
        self.assertEqual(resampler.point(1).value, Decimal("9.000000"))

    def test_fixed_half_even_rounding(self):
        resampler = CorrectableResampler(0, 1, Mode.LINEAR, 10)
        resampler.ingest("a", 0, 0)
        resampler.ingest("b", 8, 1)
        self.assertEqual(resampler.point(1).value, Decimal("0.125000"))
        self.assertEqual(resampler.point(3).value, Decimal("0.375000"))
        self.assertEqual(resampler.point(2).value, Decimal("0.250000"))

    def test_float_inputs_are_rejected_for_exactness(self):
        resampler = CorrectableResampler(0, 1, Mode.LINEAR, 10)
        with self.assertRaises(TypeError):
            resampler.ingest("a", 0.1, 1)
        with self.assertRaises(TypeError):
            CorrectableResampler(0, 0.5, Mode.LINEAR, 10)

    def test_releasing_keeps_history_but_drops_closed_working_events(self):
        resampler = CorrectableResampler(0, 1, Mode.LINEAR, 10)
        resampler.ingest("a", 0, 0)
        resampler.ingest("b", 2, 4)
        resampler.ingest("c", 5, 10)
        result = resampler.release_through(2)
        self.assertEqual(result.archived_events, 1)
        self.assertEqual(result.retained_boundary_sample_id, "b")
        self.assertEqual(resampler.retained_sample_ids(), ("b", "c"))
        self.assertEqual(resampler.history_sample_ids(), ("a", "b", "c"))
        self.assertEqual(resampler.point(3).sample_ids, ("b", "c"))
        with self.assertRaises(SealedIntervalError):
            resampler.point(2)
        with self.assertRaises(SealedIntervalError):
            resampler.ingest("late", 2, 99)
        with self.assertRaises(SealedIntervalError):
            resampler.retract("a")

    def test_release_requires_drained_sealed_revisions_but_keeps_future_watch(self):
        resampler = CorrectableResampler(0, 1, Mode.STEP, 10)
        resampler.watch([0, 3])
        resampler.ingest("a", 0, 1)
        with self.assertRaises(RevisionsBlocked):
            resampler.release_through(2)
        self.assertEqual([revision.index for revision in resampler.drain_revisions()], [0, 3])
        result = resampler.release_through(2)
        self.assertEqual(result.archived_events, 0)
        self.assertEqual(resampler.index_stats()["watched"], 1)
        self.assertEqual(resampler.point(3).sample_ids, ("a",))

    def test_release_does_not_carry_sample_across_breakpoint(self):
        resampler = CorrectableResampler(0, 1, Mode.STEP, 10)
        resampler.ingest("before", 0, 1)
        resampler.add_breakpoint(2)
        result = resampler.release_through(3)
        self.assertIsNone(result.retained_boundary_sample_id)
        self.assertEqual(resampler.retained_sample_ids(), ())
        self.assertFalse(resampler.point(4).valid)
        self.assertEqual(resampler.point(4).reason, UnknownReason.NO_SAMPLES)

    def test_watch_mixed_sealed_and_future_indices_is_atomic(self):
        resampler = CorrectableResampler(0, 1, Mode.STEP, 10)
        resampler.ingest("a", 0, 1)
        resampler.release_through(1)
        with self.assertRaises(SealedIntervalError):
            resampler.watch([3, 1])
        self.assertEqual(resampler.index_stats()["watched"], 0)


if __name__ == "__main__":
    unittest.main()
