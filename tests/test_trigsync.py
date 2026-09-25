import unittest
from fractions import Fraction

from trigsync import (
    Congruence,
    Duration,
    ExceptionRule,
    Instant,
    Intersection,
    NoCommonSolution,
    OffsetEntry,
    OffsetTable,
    PauseRule,
    PhaseRule,
    Stream,
    TickScale,
    WallClock,
    crt_all,
)


def naive_fires(stream, t):
    """参照实现：直接按定义判断流在时刻 t 是否触发。"""
    if any(ex.at == t for ex in stream.exceptions):
        return True
    rule = stream.active_phase(t)
    if rule is None:
        return False
    if (t - rule.phase) % rule.period != 0:
        return False
    return not any(pa.start <= t < pa.end for pa in stream.pauses)


def naive_common(streams, lo, hi):
    return [t for t in range(lo, hi) if all(naive_fires(st, Fraction(t)) for st in streams)]


class CrtTests(unittest.TestCase):
    def test_coprime(self):
        result = crt_all([Congruence(0, 2), Congruence(1, 3)])
        self.assertEqual((result.residue, result.modulus), (4, 6))

    def test_non_coprime_compatible(self):
        result = crt_all([Congruence(4, 6), Congruence(1, 3)])
        self.assertEqual((result.residue, result.modulus), (4, 6))

    def test_conflict_evidence(self):
        with self.assertRaises(NoCommonSolution) as ctx:
            crt_all([Congruence(0, 2), Congruence(1, 2)])
        exc = ctx.exception
        self.assertEqual(len(exc.conflicts), 1)
        conflict = exc.conflicts[0]
        self.assertEqual(conflict.divisor, 2)
        self.assertEqual(conflict.residue_gap, 1)
        self.assertIn("gcd(2, 2) = 2", exc.explain())

    def test_three_streams(self):
        result = crt_all([Congruence(0, 2), Congruence(1, 3), Congruence(2, 5)])
        self.assertEqual((result.residue, result.modulus), (22, 30))

    def test_huge_moduli_no_overflow(self):
        m1, m2 = 2**40, 3**30
        result = crt_all([Congruence(0, m1), Congruence(1, m2)])
        self.assertEqual(result.residue % m1, 0)
        self.assertEqual(result.residue % m2, 1)
        self.assertEqual(result.modulus, m1 * m2)


class TimebaseTests(unittest.TestCase):
    def test_type_algebra(self):
        self.assertEqual(Instant(5) + Duration(2), Instant(7))
        self.assertEqual(Duration(2) + Instant(5), Instant(7))
        self.assertEqual(Instant(7) - Duration(2), Instant(5))
        self.assertEqual(Instant(7) - Instant(5), Duration(2))
        self.assertEqual(WallClock(10) + Duration(3), WallClock(13))
        self.assertEqual(WallClock(10) - WallClock(4), Duration(6))
        self.assertEqual(Duration(2) * 3, Duration(6))

    def test_illegal_algebra_rejected(self):
        with self.assertRaises(TypeError):
            Instant(1) + Instant(2)
        with self.assertRaises(TypeError):
            WallClock(1) + WallClock(2)
        with self.assertRaises(TypeError):
            Instant(1) + WallClock(2)

    def test_float_rejected(self):
        with self.assertRaises(TypeError):
            Instant(1.5)

    def test_offset_table(self):
        table = OffsetTable([
            OffsetEntry(0, 1_000_000, 1),
            OffsetEntry(100, 1_000_100, 2),
        ])
        self.assertEqual(table.to_wall(Instant(50)), WallClock(1_000_050))
        self.assertEqual(table.to_wall(Instant(150)), WallClock(1_000_200))
        self.assertEqual(table.to_instant(WallClock(1_000_200)), Instant(150))
        self.assertEqual(table.to_instant(table.to_wall(Instant(333))), Instant(333))

    def test_tick_scale_exact(self):
        scale = TickScale([2, 4])
        self.assertEqual(scale.denominator, 4)
        self.assertEqual(scale.to_ticks(Fraction(5, 4)), 5)
        with self.assertRaises(ValueError):
            scale.to_ticks(Fraction(1, 3))

    def test_tick_scale_huge_denominators(self):
        scale = TickScale([10**12, 3**10])
        self.assertEqual(scale.to_ticks(Fraction(1, 10**12 * 3**10)), 1)
        self.assertEqual(scale.to_time(1), Fraction(1, scale.denominator))


class EngineTests(unittest.TestCase):
    def test_first_intersection_then_pause(self):
        a = Stream("A", (PhaseRule(1, 0, 2, 0),))
        b = Stream("B", (PhaseRule(1, 0, 3, 1),))
        eng = Intersection([a, b], 0, 30)
        self.assertEqual(eng.first(), Instant(4))
        self.assertEqual(
            [p.value for p in eng.iter_points()],
            [Fraction(4), Fraction(10), Fraction(16), Fraction(22), Fraction(28)],
        )
        a_paused = Stream("A", (PhaseRule(1, 0, 2, 0),), pauses=(PauseRule(1, 4, 5),))
        eng2 = Intersection([a_paused, b], 0, 30)
        self.assertEqual(eng2.first(), Instant(10))
        self.assertEqual(eng2.count(), 4)

    def test_bruteforce_crosscheck(self):
        configs = [
            (
                [Stream("A", (PhaseRule(1, 0, 2, 0),)), Stream("B", (PhaseRule(1, 0, 3, 1),))],
                0, 60,
            ),
            (
                [
                    Stream("A", (PhaseRule(1, 0, 2, 0),),
                           pauses=(PauseRule(1, 14, 17),),
                           exceptions=(ExceptionRule(1, 7), ExceptionRule(1, 8))),
                    Stream("B", (PhaseRule(1, 0, 3, 1),),
                           exceptions=(ExceptionRule(1, 15),)),
                ],
                0, 60,
            ),
            (
                [
                    Stream("A", (PhaseRule(1, 0, 2, 0),)),
                    Stream("B", (PhaseRule(1, 0, 3, 1),)),
                    Stream("C", (PhaseRule(1, 0, 5, 2),)),
                ],
                0, 120,
            ),
            (
                [
                    Stream("A", (PhaseRule(1, 0, 2, 0), PhaseRule(2, 10, 2, 1))),
                    Stream("B", (PhaseRule(1, 0, 4, 0),)),
                ],
                0, 40,
            ),
        ]
        for streams, lo, hi in configs:
            with self.subTest(lo=lo, hi=hi, n=len(streams)):
                eng = Intersection(streams, lo, hi)
                expected = [Fraction(t) for t in naive_common(streams, lo, hi)]
                got = [p.value for p in eng.iter_points()]
                self.assertEqual(got, expected)
                self.assertEqual(eng.count(), len(expected))
                for k, point in enumerate(expected):
                    self.assertEqual(eng.kth(k), Instant(point))

    def test_phase_switch(self):
        a = Stream("A", (PhaseRule(1, 0, 2, 0), PhaseRule(2, 10, 2, 1)))
        b = Stream("B", (PhaseRule(1, 0, 4, 0),))
        eng = Intersection([a, b], 0, 20)
        self.assertEqual(
            [p.value for p in eng.iter_points()],
            [Fraction(0), Fraction(4), Fraction(8)],
        )

    def test_exceptions_must_be_common(self):
        a = Stream(
            "A",
            (PhaseRule(1, 0, 2, 0),),
            exceptions=(ExceptionRule(1, 7), ExceptionRule(1, 8), ExceptionRule(1, 15)),
        )
        b = Stream(
            "B",
            (PhaseRule(1, 0, 3, 1),),
            exceptions=(ExceptionRule(1, 9), ExceptionRule(1, 15)),
        )
        eng = Intersection([a, b], 0, 30)
        # 7: B 同余命中 -> 共同；8/9: 单边例外 -> 拒绝；15: 双方例外 -> 共同
        self.assertEqual(
            [p.value for p in eng.iter_points()],
            [Fraction(4), Fraction(7), Fraction(10), Fraction(15), Fraction(16), Fraction(22), Fraction(28)],
        )

    def test_exception_during_pause_still_fires(self):
        a = Stream(
            "A",
            (PhaseRule(1, 0, 2, 0),),
            pauses=(PauseRule(1, 16, 18),),
            exceptions=(ExceptionRule(1, 17),),
        )
        b = Stream("B", (PhaseRule(1, 0, 3, 1),), exceptions=(ExceptionRule(1, 17),))
        eng = Intersection([a, b], 0, 30)
        points = [p.value for p in eng.iter_points()]
        self.assertNotIn(Fraction(16), points)  # 暂停抑制周期点
        self.assertIn(Fraction(17), points)     # 双方例外仍然共同触发

    def test_rational_periods_exact(self):
        a = Stream("A", (PhaseRule(1, 0, Fraction(3, 2), Fraction(1, 2)),))
        b = Stream("B", (PhaseRule(1, 0, Fraction(5, 4), 0),))
        eng = Intersection([a, b], 0, 20)
        self.assertEqual(eng.scale.denominator, 4)
        points = [p.value for p in eng.iter_points()]
        self.assertEqual(points, [Fraction(5), Fraction(25, 2)])
        self.assertTrue(all(isinstance(p, Fraction) for p in points))

    def test_huge_window_no_tick_scan(self):
        a = Stream("A", (PhaseRule(1, 0, 2, 0),))
        b = Stream("B", (PhaseRule(1, 0, 3, 1),))
        n = 10**15
        eng = Intersection([a, b], 0, n)
        expected = (n - 4 + 5) // 6  # ceil((n-4)/6)
        self.assertEqual(eng.count(), expected)
        self.assertEqual(eng.kth(0), Instant(4))
        self.assertEqual(eng.kth(1), Instant(10))
        last = eng.kth(expected - 1)
        self.assertEqual((last.value - 4) % 6, 0)
        self.assertLess(last.value, n)
        self.assertGreaterEqual(last.value + 6, n)

    def test_huge_window_with_pause(self):
        a = Stream("A", (PhaseRule(1, 0, 2, 0),), pauses=(PauseRule(1, 10, 4 * 10**14),))
        b = Stream("B", (PhaseRule(1, 0, 3, 1),))
        n = 10**15
        eng = Intersection([a, b], 0, n)
        total = (n - 4 + 5) // 6
        removed = (4 * 10**14 - 10 + 5) // 6  # 暂停区间内的 4+6j 点数
        self.assertEqual(eng.count(), total - removed)
        self.assertEqual(eng.kth(0), Instant(4))
        # 暂停结束后的第一个交点
        j = -((-(4 * 10**14 - 4)) // 6)
        self.assertEqual(eng.kth(1), Instant(4 + 6 * j))

    def test_no_solution_report(self):
        a = Stream("A", (PhaseRule(1, 0, 2, 0),))
        b = Stream("B", (PhaseRule(1, 0, 2, 1),))
        eng = Intersection([a, b], 0, 10)
        self.assertEqual(eng.count(), 0)
        self.assertIsNone(eng.first())
        self.assertEqual(len(eng.reports), 1)
        self.assertIn("gcd(2, 2) = 2", eng.reports[0].detail)
        with self.assertRaises(IndexError):
            eng.kth(0)

    def test_kth_out_of_range(self):
        a = Stream("A", (PhaseRule(1, 0, 2, 0),))
        b = Stream("B", (PhaseRule(1, 0, 3, 1),))
        eng = Intersection([a, b], 0, 30)
        self.assertEqual(eng.kth(2), Instant(16))
        with self.assertRaises(IndexError):
            eng.kth(5)
        with self.assertRaises(IndexError):
            eng.kth(-1)

    def test_checkpoint_binds_rule_versions(self):
        a1 = Stream("A", (PhaseRule(1, 0, 2, 0),), pauses=(PauseRule(1, 4, 5),))
        b = Stream("B", (PhaseRule(1, 0, 3, 1),))
        eng1 = Intersection([a1, b], 0, 30)
        eng1_again = Intersection([a1, b], 0, 30)
        self.assertEqual(eng1.checkpoint.digest(), eng1_again.checkpoint.digest())

        a2 = Stream("A", (PhaseRule(1, 0, 2, 0),), pauses=(PauseRule(2, 4, 5),))
        eng2 = Intersection([a2, b], 0, 30)
        self.assertNotEqual(eng1.checkpoint.digest(), eng2.checkpoint.digest())

        a3 = Stream("A", (PhaseRule(1, 0, 2, 0),), pauses=(PauseRule(1, 4, 6),))
        eng3 = Intersection([a3, b], 0, 30)
        self.assertNotEqual(eng1.checkpoint.digest(), eng3.checkpoint.digest())

    def test_inactive_stream_segment_reported(self):
        a = Stream("A", (PhaseRule(1, 10, 2, 0),))
        b = Stream("B", (PhaseRule(1, 0, 3, 1),))
        eng = Intersection([a, b], 0, 20)
        self.assertTrue(any("无生效相位规则" in r.detail for r in eng.reports))
        self.assertEqual(eng.first(), Instant(10))


if __name__ == "__main__":
    unittest.main()
