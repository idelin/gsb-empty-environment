import unittest
from fractions import Fraction

from trigger_sets import (
    Checkpoint,
    CheckpointMismatch,
    Engine,
    PeriodicStream,
    crt_pair,
)


class CanonicalExampleTest(unittest.TestCase):
    """Period 2 phase 0 vs period 3 phase 1: first common trigger is 4."""

    def test_first_intersection_is_4(self):
        a = PeriodicStream("a", 2, 0)
        b = PeriodicStream("b", 3, 1)
        engine = Engine([a, b])
        points = [p.tick for p in engine.iter_common(0, 20)]
        self.assertEqual(points, [Fraction(4), Fraction(10), Fraction(16)])
        self.assertEqual(engine.kth(0, 0, 20).tick, Fraction(4))
        self.assertEqual(engine.count(0, 20), 3)

    def test_pause_then_next_intersection(self):
        a = PeriodicStream("a", 2, 0)
        b = PeriodicStream("b", 3, 1)
        a.add_pause(4, 5)  # excludes the t=4 intersection
        engine = Engine([a, b])
        points = [p.tick for p in engine.iter_common(0, 20)]
        self.assertEqual(points, [Fraction(10), Fraction(16)])
        self.assertEqual(engine.kth(0, 0, 20).tick, Fraction(10))
        self.assertEqual(engine.count(0, 20), 2)


class RationalExactnessTest(unittest.TestCase):
    def test_rational_periods_stay_exact(self):
        a = PeriodicStream("a", Fraction(3, 2), 0)
        b = PeriodicStream("b", Fraction(5, 4), Fraction(1, 2))
        engine = Engine([a, b])
        points = [p.tick for p in engine.iter_common(0, 20)]
        self.assertEqual(points, [Fraction(3), Fraction(21, 2), Fraction(18)])
        self.assertTrue(all(isinstance(p, Fraction) for p in points))

    def test_thirds_and_sevenths_no_float_drift(self):
        a = PeriodicStream("a", Fraction(1, 3), 0)
        b = PeriodicStream("b", Fraction(1, 7), 0)
        engine = Engine([a, b])
        points = [p.tick for p in engine.iter_common(0, 5)]
        self.assertEqual(points, [Fraction(n) for n in range(5)])
        kth = engine.kth(10**6, 0, 10**7)
        self.assertEqual(kth.tick, Fraction(10**6))
        self.assertEqual(kth.tick.denominator, 1)

    def test_unified_scale_huge_denominators(self):
        a = PeriodicStream("a", Fraction(1, 10**6 + 3), 0)
        b = PeriodicStream("b", Fraction(1, 10**6 + 33), 0)
        engine = Engine([a, b])
        scale = engine._scale(Fraction(0), Fraction(1))
        self.assertEqual(scale, (10**6 + 3) * (10**6 + 33))
        self.assertEqual(engine.kth(0, 0, 1).tick, Fraction(0))


class CrtTest(unittest.TestCase):
    def test_crt_pair_solution(self):
        self.assertEqual(crt_pair(0, 2, 1, 3), (4, 6))

    def test_crt_pair_conflict(self):
        self.assertIsNone(crt_pair(0, 2, 1, 2))


class NoSolutionTest(unittest.TestCase):
    def test_crt_conflict_evidence(self):
        a = PeriodicStream("a", 2, 0)
        b = PeriodicStream("b", 2, 1)
        engine = Engine([a, b])
        self.assertEqual(engine.count(0, 10), 0)
        self.assertEqual(list(engine.iter_common(0, 10)), [])
        with self.assertRaises(IndexError):
            engine.kth(0, 0, 10)
        report = engine.explain_no_solution(0, 10)
        self.assertIsNotNone(report)
        self.assertEqual(report.evidence[0].kind, "crt-conflict")
        self.assertIn("gcd = 2", report.evidence[0].detail)

    def test_paused_stream_evidence(self):
        a = PeriodicStream("a", 2, 0)
        a.add_pause(0, 100)
        b = PeriodicStream("b", 3, 1)
        engine = Engine([a, b])
        report = engine.explain_no_solution(0, 10)
        self.assertIsNotNone(report)
        self.assertEqual(report.evidence[0].kind, "paused-stream")
        self.assertIn("a", report.evidence[0].detail)

    def test_no_report_when_solution_exists(self):
        a = PeriodicStream("a", 2, 0)
        b = PeriodicStream("b", 3, 1)
        engine = Engine([a, b])
        self.assertIsNone(engine.explain_no_solution(0, 10))


class ExceptionTest(unittest.TestCase):
    def test_only_common_exceptions_are_added(self):
        a = PeriodicStream("a", 5, 0)
        b = PeriodicStream("b", 7, 0)
        a.add_exception(3)   # not a trigger of b -> rejected
        a.add_exception(14)  # 14 = 2*7 is a trigger of b -> accepted
        engine = Engine([a, b])
        points = [p.tick for p in engine.iter_common(0, 40)]
        self.assertEqual(points, [Fraction(0), Fraction(14), Fraction(35)])
        self.assertEqual(engine.count(0, 40), 3)
        self.assertEqual(engine.kth(1, 0, 40).tick, Fraction(14))

    def test_exception_must_intersect_every_stream(self):
        a = PeriodicStream("a", 5, 0)
        b = PeriodicStream("b", 7, 0)
        b.add_exception(9)  # alone: not a trigger of a
        engine = Engine([a, b])
        self.assertNotIn(Fraction(9), [p.tick for p in engine.iter_common(0, 40)])
        a.add_exception(9)  # now both fire at 9 -> common
        engine = Engine([a, b])
        points = [p.tick for p in engine.iter_common(0, 40)]
        self.assertEqual(points, [Fraction(0), Fraction(9), Fraction(35)])

    def test_exception_fires_during_pause(self):
        a = PeriodicStream("a", 5, 0)
        a.add_pause(0, 10)
        a.add_exception(3)
        b = PeriodicStream("b", 1, 0)
        engine = Engine([a, b])
        points = [p.tick for p in engine.iter_common(0, 5)]
        self.assertEqual(points, [Fraction(3)])


class PhaseSwitchTest(unittest.TestCase):
    def test_phase_switch_reanchors(self):
        a = PeriodicStream("a", 2, 0)
        a.add_phase_switch(5, 2)  # from t=5 on: 7, 9, 11, ...
        b = PeriodicStream("b", 3, 1)  # 1, 4, 7, 10, 13, 16, 19
        engine = Engine([a, b])
        points = [p.tick for p in engine.iter_common(0, 20)]
        self.assertEqual(
            points, [Fraction(4), Fraction(7), Fraction(13), Fraction(19)]
        )


class HugeWindowTest(unittest.TestCase):
    def test_huge_window_without_tick_scanning(self):
        p1, p2 = 10**12 + 39, 10**12 + 61  # coprime
        a = PeriodicStream("a", p1, 0)
        b = PeriodicStream("b", p2, 0)
        engine = Engine([a, b])
        modulus = p1 * p2
        hi = 10**30
        expected = -((-hi) // modulus)  # multiples of modulus in [0, hi)
        self.assertEqual(engine.count(0, hi), expected)
        self.assertEqual(engine.kth(0, 0, hi).tick, Fraction(0))
        last = engine.kth(expected - 1, 0, hi)
        self.assertEqual(last.tick, Fraction((expected - 1) * modulus))
        self.assertLess(last.tick, hi)
        self.assertGreaterEqual(expected * modulus, hi)
        with self.assertRaises(IndexError):
            engine.kth(expected, 0, hi)

    def test_iteration_is_lazy(self):
        a = PeriodicStream("a", 10**12 + 39, 0)
        b = PeriodicStream("b", 10**12 + 61, 0)
        engine = Engine([a, b])
        iterator = engine.iter_common(0, 10**45)
        first = next(iterator)  # returns immediately, no full enumeration
        self.assertEqual(first.tick, Fraction(0))
        second = next(iterator)
        self.assertEqual(second.tick, Fraction((10**12 + 39) * (10**12 + 61)))


class BruteForceCrossCheckTest(unittest.TestCase):
    def cross_check(self, streams, lo, hi):
        engine = Engine(streams)
        naive = set(streams[0].triggers_in(lo, hi))
        for stream in streams[1:]:
            naive &= set(stream.triggers_in(lo, hi))
        expected = sorted(naive)
        got = [p.tick for p in engine.iter_common(lo, hi)]
        self.assertEqual(got, expected)
        self.assertEqual(engine.count(lo, hi), len(expected))
        for k, point in enumerate(expected):
            self.assertEqual(engine.kth(k, lo, hi).tick, point)
        with self.assertRaises(IndexError):
            engine.kth(len(expected), lo, hi)

    def test_mixed_rules_small_window(self):
        a = PeriodicStream("a", 2, 0)
        a.add_pause(5, 7)
        a.add_exception(9)
        a.add_phase_switch(11, 1)
        b = PeriodicStream("b", 3, 1)
        b.add_exception(8)
        c = PeriodicStream("c", Fraction(3, 2), Fraction(1, 2))
        self.cross_check([a, b, c], 0, 25)

    def test_rational_pauses_and_exceptions(self):
        d = PeriodicStream("d", Fraction(5, 2), Fraction(1, 4))
        d.add_pause(Fraction(3, 2), Fraction(9, 4))
        d.add_exception(Fraction(11, 4))
        e = PeriodicStream("e", Fraction(7, 3), 1)
        e.add_phase_switch(Fraction(13, 3), Fraction(1, 3))
        self.cross_check([d, e], 0, 12)
        self.cross_check([d, e], Fraction(1, 2), Fraction(41, 4))


class CheckpointTest(unittest.TestCase):
    def test_checkpoint_binds_rule_versions(self):
        a = PeriodicStream("a", 2, 0)
        b = PeriodicStream("b", 3, 1)
        checkpoint = Checkpoint.capture([a, b])
        engine = Engine([a, b], checkpoint=checkpoint)
        self.assertEqual(engine.count(0, 20), 3)
        a.add_pause(4, 5)  # bumps a's version after the checkpoint
        with self.assertRaises(CheckpointMismatch):
            engine.count(0, 20)
        with self.assertRaises(CheckpointMismatch):
            Engine([a, b], checkpoint=checkpoint)
        fresh = Checkpoint.capture([a, b])
        engine = Engine([a, b], checkpoint=fresh)
        self.assertEqual(engine.count(0, 20), 2)

    def test_checkpoint_records_all_versions(self):
        a = PeriodicStream("a", 2, 0)
        b = PeriodicStream("b", 3, 1)
        a.add_exception(7)
        b.add_phase_switch(9, 1)
        checkpoint = Checkpoint.capture([a, b])
        self.assertEqual(
            checkpoint.versions, (("a", a.version), ("b", b.version))
        )


if __name__ == "__main__":
    unittest.main()
