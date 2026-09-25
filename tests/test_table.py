"""Transition-table conversion tests."""

import unittest

from tztable import (
    AmbiguousTimeError,
    Duration,
    GapTimeError,
    Instant,
    Transition,
    TransitionTable,
    WallClock,
)


def utc(text_date, hour=0, minute=0):
    """Build an Instant from a UTC date/time."""
    year, month, day = text_date
    return WallClock.of(year, month, day, hour, minute).to_instant_equiv()


def make_fallback_table():
    # Offset +2 until 2024-10-27T00:00Z, then +1.
    # Local overlap: [2024-10-27T01:00, 2024-10-27T02:00) happens twice.
    return TransitionTable(
        version="fallback-v1",
        base_offset=Duration.of(hours=2),
        transitions=(
            Transition(utc=utc((2024, 10, 27)), offset_after=Duration.of(hours=1)),
        ),
    )


def make_gap_table():
    # Offset +1 until 2024-03-31T01:00Z, then +2.
    # Local gap: [2024-03-31T02:00, 2024-03-31T03:00) does not exist.
    return TransitionTable(
        version="gap-v1",
        base_offset=Duration.of(hours=1),
        transitions=(
            Transition(utc=utc((2024, 3, 31), 1), offset_after=Duration.of(hours=2)),
        ),
    )


def make_double_fallback_table():
    # Two backward transitions 30 UTC-minutes apart: +3 -> +2 -> +1.
    # Local times in [01:30, 02:30) occur twice, and [02:00, 02:30) three
    # times -- a caller assuming "at most two candidates" would be wrong.
    return TransitionTable(
        version="double-fallback-v1",
        base_offset=Duration.of(hours=3),
        transitions=(
            Transition(utc=utc((2024, 1, 1)), offset_after=Duration.of(hours=2)),
            Transition(
                utc=utc((2024, 1, 1), 0, 30), offset_after=Duration.of(hours=1)
            ),
        ),
    )


class TestFallbackOverlap(unittest.TestCase):
    def setUp(self):
        self.table = make_fallback_table()

    def test_ambiguous_wall_has_two_candidates_with_evidence(self):
        wall = WallClock.of(2024, 10, 27, 1, 30)
        candidates = self.table.wall_to_instants(wall)
        self.assertEqual(len(candidates), 2)
        self.assertEqual(candidates[0].instant, utc((2024, 10, 26), 23, 30))
        self.assertEqual(candidates[0].offset, Duration.of(hours=2))
        self.assertEqual(candidates[0].fold, 0)
        self.assertEqual(candidates[1].instant, utc((2024, 10, 27), 0, 30))
        self.assertEqual(candidates[1].offset, Duration.of(hours=1))
        self.assertEqual(candidates[1].fold, 1)
        for candidate in candidates:
            self.assertIn("occurrence", candidate.evidence)
            self.assertIn("offset period", candidate.evidence)

    def test_interval_inside_overlap_yields_two_utc_intervals(self):
        mapping = self.table.map_wall_interval(
            WallClock.of(2024, 10, 27, 1, 15), WallClock.of(2024, 10, 27, 1, 45)
        )
        self.assertEqual(len(mapping.candidates), 2)
        self.assertEqual(mapping.missing, ())
        first, second = mapping.candidates
        self.assertEqual(
            first.utc_intervals[0].start, utc((2024, 10, 26), 23, 15)
        )
        self.assertEqual(first.utc_intervals[0].end, utc((2024, 10, 26), 23, 45))
        self.assertEqual(second.utc_intervals[0].start, utc((2024, 10, 27), 0, 15))
        self.assertEqual(second.utc_intervals[0].end, utc((2024, 10, 27), 0, 45))
        self.assertNotEqual(first.offset, second.offset)

    def test_earlier_later_round_trip_preserves_identity(self):
        wall = WallClock.of(2024, 10, 27, 1, 30)
        for policy, expected_fold in (("earlier", 0), ("later", 1)):
            chosen = self.table.resolve_wall(wall, policy)
            self.assertEqual(chosen.fold, expected_fold)
            reading = self.table.instant_to_wall(chosen.instant)
            self.assertEqual(reading.wall, wall)
            self.assertEqual(reading.fold, chosen.fold)
            back = self.table.wall_to_instants(reading.wall)[reading.fold]
            self.assertEqual(back.instant, chosen.instant)

    def test_strict_resolution_raises_on_overlap(self):
        with self.assertRaises(AmbiguousTimeError):
            self.table.resolve_wall(WallClock.of(2024, 10, 27, 1, 30), "strict")


class TestSpringForwardGap(unittest.TestCase):
    def setUp(self):
        self.table = make_gap_table()

    def test_gap_wall_has_no_candidates(self):
        wall = WallClock.of(2024, 3, 31, 2, 30)
        self.assertEqual(self.table.wall_to_instants(wall), ())
        with self.assertRaises(GapTimeError):
            self.table.resolve_wall(wall, "strict")

    def test_compatible_resolution_shifts_out_of_gap(self):
        resolved = self.table.resolve_wall(
            WallClock.of(2024, 3, 31, 2, 30), "compatible"
        )
        self.assertEqual(resolved.instant, utc((2024, 3, 31), 1, 0))
        reading = self.table.instant_to_wall(resolved.instant)
        self.assertEqual(reading.wall, WallClock.of(2024, 3, 31, 3, 0))

    def test_interval_spanning_gap_reports_missing_segment(self):
        mapping = self.table.map_wall_interval(
            WallClock.of(2024, 3, 31, 2, 30), WallClock.of(2024, 3, 31, 3, 30)
        )
        self.assertEqual(len(mapping.missing), 1)
        gap = mapping.missing[0]
        self.assertEqual(gap.start, WallClock.of(2024, 3, 31, 2, 30))
        self.assertEqual(gap.end, WallClock.of(2024, 3, 31, 3, 0))
        self.assertEqual(len(mapping.candidates), 1)
        interval = mapping.candidates[0].utc_intervals[0]
        self.assertEqual(interval.start, utc((2024, 3, 31), 1, 0))
        self.assertEqual(interval.end, utc((2024, 3, 31), 1, 30))


class TestConsecutiveFallbacks(unittest.TestCase):
    def setUp(self):
        self.table = make_double_fallback_table()

    def test_three_candidates_not_capped_at_two(self):
        wall = WallClock.of(2024, 1, 1, 2, 15)
        candidates = self.table.wall_to_instants(wall)
        self.assertEqual(len(candidates), 3)
        self.assertEqual(
            [c.instant for c in candidates],
            [
                utc((2023, 12, 31), 23, 15),
                utc((2024, 1, 1), 0, 15),
                utc((2024, 1, 1), 1, 15),
            ],
        )
        self.assertEqual([c.fold for c in candidates], [0, 1, 2])

    def test_round_trip_identity_for_every_occurrence(self):
        wall = WallClock.of(2024, 1, 1, 2, 15)
        for candidate in self.table.wall_to_instants(wall):
            reading = self.table.instant_to_wall(candidate.instant)
            self.assertEqual(reading.wall, wall)
            self.assertEqual(reading.fold, candidate.fold)
            back = self.table.wall_to_instants(reading.wall)[reading.fold]
            self.assertEqual(back.instant, candidate.instant)


class TestIntervalUnionCoverage(unittest.TestCase):
    def test_union_covers_every_local_instant_across_gap_and_overlap(self):
        table = TransitionTable(
            version="full-year",
            base_offset=Duration.of(hours=1),
            transitions=(
                Transition(
                    utc=utc((2024, 3, 31), 1), offset_after=Duration.of(hours=2)
                ),
                Transition(
                    utc=utc((2024, 10, 27), 1), offset_after=Duration.of(hours=1)
                ),
            ),
        )
        start = WallClock.of(2024, 3, 31, 0, 0)
        end = WallClock.of(2024, 10, 27, 12, 0)
        mapping = table.map_wall_interval(start, end)

        # Local coverage: candidate local ranges + missing == [start, end).
        pieces = sorted(
            [(c.local_start, c.local_end) for c in mapping.candidates]
            + [(g.start, g.end) for g in mapping.missing]
        )
        cursor = start
        for lo, hi in pieces:
            # Overlaps mean pieces may overlap each other; the invariant is
            # that no hole exists between what is covered so far and the
            # next piece (gaps appear only as reported `missing` ranges).
            self.assertLessEqual(lo, cursor)
            cursor = max(cursor, hi)
        self.assertEqual(cursor, end)

        # Sampled check: every local instant is covered or reported missing.
        step = Duration.of(seconds=613).nanos
        nanos = start.to_instant_equiv().epoch_nanos
        stop = end.to_instant_equiv().epoch_nanos
        while nanos < stop:
            probe = Instant(nanos).to_wall()
            covered = table.wall_to_instants(probe)
            if not covered:
                self.assertTrue(
                    any(g.start <= probe < g.end for g in mapping.missing),
                    f"{probe} neither covered nor reported missing",
                )
            nanos += step

        # UTC union is contiguous and spans gap..overlap adjusted range.
        union = mapping.utc_union
        self.assertEqual(len(union), 1)
        self.assertEqual(union[0].start, utc((2024, 3, 30), 23, 0))
        self.assertEqual(union[0].end, utc((2024, 10, 27), 11, 0))

    def test_plain_interval_maps_to_single_contiguous_union(self):
        table = make_fallback_table()
        mapping = table.map_wall_interval(
            WallClock.of(2024, 10, 26, 12, 0), WallClock.of(2024, 10, 26, 18, 0)
        )
        self.assertEqual(len(mapping.candidates), 1)
        self.assertEqual(mapping.missing, ())
        union = mapping.utc_union
        self.assertEqual(len(union), 1)
        self.assertEqual(
            union[0].end.epoch_nanos - union[0].start.epoch_nanos,
            6 * 3_600_000_000_000,
        )


class TestManyTransitions(unittest.TestCase):
    def test_hundred_transitions_resolve_correctly(self):
        transitions = []
        for i in range(100):
            day = WallClock(2 * i, 0)  # every other day from 1970-01-01
            transitions.append(
                Transition(
                    utc=day.to_instant_equiv(),
                    offset_after=Duration.of(minutes=(i % 5) - 2),
                )
            )
        table = TransitionTable(
            version="many", base_offset=Duration.of(minutes=-3),
            transitions=tuple(transitions),
        )
        for i, transition in enumerate(transitions):
            before = table.instant_to_wall(transition.utc.minus(Duration.of(nanoseconds=1)))
            after = table.instant_to_wall(transition.utc)
            expected_before = Duration.of(minutes=((i - 1) % 5) - 2) if i else Duration.of(minutes=-3)
            self.assertEqual(before.offset, expected_before)
            self.assertEqual(after.offset, transition.offset_after)
            self.assertEqual(
                after.wall.to_instant_equiv().epoch_nanos
                - before.wall.to_instant_equiv().epoch_nanos,
                transition.offset_after.nanos - expected_before.nanos + 1,
            )


class TestLargeEpochWithTable(unittest.TestCase):
    def test_conversion_at_huge_epoch_keeps_nanosecond_precision(self):
        table = make_fallback_table()
        nanos = 2**100 + 123_456_789
        instant = Instant(nanos)
        reading = table.instant_to_wall(instant)
        self.assertEqual(reading.offset, Duration.of(hours=1))
        self.assertEqual(
            reading.wall, instant.plus(Duration.of(hours=1)).to_wall()
        )
        candidates = table.wall_to_instants(reading.wall)
        self.assertEqual(len(candidates), 1)
        self.assertEqual(candidates[0].instant, instant)


if __name__ == "__main__":
    unittest.main()
