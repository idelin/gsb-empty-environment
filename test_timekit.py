import unittest

from timekit import (
    Duration,
    Instant,
    InstantInterval,
    InvalidDatePolicy,
    LiveTransitionTable,
    Transition,
    TransitionTable,
    WallClock,
    WallInterval,
    build_transitions,
    daily,
    map_instant,
    map_local_interval,
    map_local_point,
    map_utc_interval,
    monthly,
    resolve_local_point,
    yearly,
)
from timekit.table import instant_at


def wall(year: int, month: int, day: int, hour: int, minute: int) -> WallClock:
    return WallClock.from_components(year, month, day, hour, minute)


class SyntheticOffsetTableTests(unittest.TestCase):
    def setUp(self) -> None:
        spring = Transition(
            instant_at(wall(2024, 3, 10, 2, 0), Duration.hours(-5)),
            Duration.hours(-4),
            "spring",
        )
        fall = Transition(
            instant_at(wall(2024, 11, 3, 2, 0), Duration.hours(-4)),
            Duration.hours(-5),
            "fall",
        )
        self.spring_table = TransitionTable(Duration.hours(-5), (spring,), 1)
        self.fall_table = TransitionTable(Duration.hours(-4), (fall,), 1)

    def test_utc_to_wall_and_back_preserves_earlier_later_identity(self) -> None:
        early = instant_at(wall(2024, 11, 3, 1, 0), Duration.hours(-4))
        late = instant_at(wall(2024, 11, 3, 1, 0), Duration.hours(-5))
        repeated_wall = wall(2024, 11, 3, 1, 0)
        self.assertEqual(map_instant(early, self.fall_table), repeated_wall)
        self.assertEqual(map_instant(late, self.fall_table), repeated_wall)
        self.assertEqual(
            resolve_local_point(repeated_wall, self.fall_table, "earlier"),
            early,
        )
        self.assertEqual(
            resolve_local_point(repeated_wall, self.fall_table, "later"),
            late,
        )

    def test_repeated_wall_interval_has_two_candidate_utc_intervals(self) -> None:
        mapping = map_local_interval(
            WallInterval(wall(2024, 11, 3, 1, 15), wall(2024, 11, 3, 1, 45)),
            self.fall_table,
        )
        self.assertEqual(len(mapping.candidates), 2)
        self.assertEqual(len(mapping.utc_union), 2)
        self.assertEqual(
            [candidate.utc_interval.start for candidate in mapping.candidates],
            [
                instant_at(wall(2024, 11, 3, 1, 15), Duration.hours(-4)),
                instant_at(wall(2024, 11, 3, 1, 15), Duration.hours(-5)),
            ],
        )
        self.assertEqual(mapping.missing_wall_parts, ())
        self.assertEqual(
            [candidate.evidence.transition_id for candidate in mapping.candidates],
            [None, "fall"],
        )

    def test_forward_spring_gap_reports_missing_parts_and_no_silent_shift(self) -> None:
        point = wall(2024, 3, 10, 2, 30)
        point_mapping = map_local_point(point, self.spring_table)
        self.assertEqual(point_mapping.candidates, ())
        self.assertIsNone(point_mapping.selected)
        self.assertEqual(len(point_mapping.gaps), 1)
        self.assertEqual(point_mapping.gaps[0].transition_id, "spring")

        interval_mapping = map_local_interval(
            WallInterval(wall(2024, 3, 10, 1, 45), wall(2024, 3, 10, 3, 30)),
            self.spring_table,
        )
        self.assertEqual(len(interval_mapping.missing_wall_parts), 1)
        missing = interval_mapping.missing_wall_parts[0]
        self.assertEqual(missing.wall_interval.start, wall(2024, 3, 10, 2, 0))
        self.assertEqual(missing.wall_interval.end, wall(2024, 3, 10, 3, 0))
        self.assertEqual(len(interval_mapping.covered_wall_union), 2)

    def test_utc_interval_crossing_spring_reports_skipped_wall_gap(self) -> None:
        mapping = map_utc_interval(
            InstantInterval(
                instant_at(wall(2024, 3, 10, 1, 0), Duration.hours(-5)),
                instant_at(wall(2024, 3, 10, 4, 0), Duration.hours(-4)),
            ),
            self.spring_table,
        )
        self.assertEqual(len(mapping.wall_pieces), 2)
        self.assertEqual(len(mapping.skipped_wall_gaps), 1)
        self.assertEqual(mapping.skipped_wall_gaps[0].transition_id, "spring")

    def test_union_merges_touching_utc_intervals_across_gap(self) -> None:
        mapping = map_local_interval(
            WallInterval(wall(2024, 3, 10, 1, 30), wall(2024, 3, 10, 3, 30)),
            self.spring_table,
        )
        self.assertEqual(len(mapping.candidates), 2)
        self.assertEqual(len(mapping.utc_union), 1)
        self.assertEqual(
            mapping.utc_union[0].start,
            instant_at(wall(2024, 3, 10, 1, 30), Duration.hours(-5)),
        )
        self.assertEqual(
            mapping.utc_union[0].end,
            instant_at(wall(2024, 3, 10, 3, 30), Duration.hours(-4)),
        )
        self.assertEqual(len(mapping.missing_wall_parts), 1)


class MultipleFallbackTests(unittest.TestCase):
    def test_two_consecutive_fallbacks_can_emit_three_candidates(self) -> None:
        transitions = (
            Transition(
                instant_at(wall(2024, 1, 1, 1, 30), Duration.hours(-3)),
                Duration.hours(-4),
                "fall-1",
            ),
            Transition(
                instant_at(wall(2024, 1, 1, 1, 0), Duration.hours(-4)),
                Duration.hours(-5),
                "fall-2",
            ),
        )
        table = TransitionTable(Duration.hours(-3), transitions, 1)
        mapping = map_local_point(wall(2024, 1, 1, 0, 45), table)
        self.assertEqual(len(mapping.candidates), 3)
        self.assertEqual(
            [candidate.evidence.offset for candidate in mapping.candidates],
            [Duration.hours(-3), Duration.hours(-4), Duration.hours(-5)],
        )


class CalendarRecurrenceTests(unittest.TestCase):
    def test_daily_rule_steps_by_calendar_day_across_spring_gap(self) -> None:
        rule = daily(
            wall(2024, 3, 9, 2, 30),
            hour=2,
            minute=30,
            last=wall(2024, 3, 11, 2, 30),
        )
        events = rule.local_events()
        self.assertEqual(
            [event.date for event in events],
            [(2024, 3, 9), (2024, 3, 10), (2024, 3, 11)],
        )
        self.assertEqual({event.time_of_day[:2] for event in events}, {(2, 30)})

        built = build_transitions(
            Duration.hours(-5),
            [
                (
                    "spring",
                    daily(
                        wall(2024, 3, 10, 2, 0),
                        hour=2,
                        last=wall(2024, 3, 10, 2, 0),
                    ),
                    Duration.hours(-4),
                )
            ],
        )
        self.assertEqual(len(built.transitions), 1)
        self.assertEqual(
            built.transitions[0].at,
            instant_at(wall(2024, 3, 10, 2, 0), Duration.hours(-4)),
        )

    def test_monthly_and_yearly_steps_are_calendar_steps(self) -> None:
        monthly_rule = monthly(
            wall(2023, 1, 31, 12, 0),
            hour=12,
            last=wall(2023, 5, 31, 12, 0),
        )
        self.assertEqual(
            [event.date for event in monthly_rule.local_events()],
            [
                (2023, 1, 31),
                (2023, 2, 28),
                (2023, 3, 31),
                (2023, 4, 30),
                (2023, 5, 31),
            ],
        )

        yearly_rule = yearly(
            wall(2024, 2, 29, 12, 0),
            hour=12,
            last=wall(2028, 3, 1, 12, 0),
            invalid_date_policy=InvalidDatePolicy.SKIP,
        )
        self.assertEqual(
            [event.date for event in yearly_rule.local_events()],
            [(2024, 2, 29), (2028, 2, 29)],
        )

    def test_build_two_synthetic_dst_rules_and_deduplicates_identical_events(self) -> None:
        schedules = [
            (
                "spring",
                yearly(wall(2024, 3, 10, 2, 0), hour=2, last=wall(2024, 3, 10, 2, 0)),
                Duration.hours(-4),
            ),
            (
                "fall",
                yearly(wall(2024, 11, 3, 2, 0), hour=2, last=wall(2024, 11, 3, 2, 0)),
                Duration.hours(-5),
            ),
        ]
        built = build_transitions(Duration.hours(-5), schedules)
        self.assertEqual(len(built.transitions), 2)
        self.assertEqual(
            [transition.at for transition in built.transitions],
            sorted(transition.at for transition in built.transitions),
        )
        duplicate = build_transitions(
            Duration.hours(-5),
            [
                schedules[0],
                (
                    "spring-copy",
                    yearly(wall(2024, 3, 10, 2, 0), hour=2, last=wall(2024, 3, 10, 2, 0)),
                    Duration.hours(-4),
                ),
            ],
        )
        self.assertEqual(len(duplicate.transitions), 1)


class HotReloadTests(unittest.TestCase):
    def test_versioned_snapshot_and_partial_cache_invalidation(self) -> None:
        old_spring = Transition(
            instant_at(wall(2024, 3, 10, 2, 0), Duration.hours(-5)),
            Duration.hours(-4),
            "spring",
        )
        table = TransitionTable(Duration.hours(-5), (old_spring,), 1)
        live = LiveTransitionTable(table)

        affected_query = WallInterval(
            wall(2024, 3, 10, 2, 15),
            wall(2024, 3, 10, 2, 45),
        )
        unaffected_query = WallInterval(
            wall(2024, 3, 11, 2, 15),
            wall(2024, 3, 11, 2, 45),
        )
        first_affected = live.map_local_interval(affected_query)
        first_unaffected = live.map_local_interval(unaffected_query)
        self.assertEqual(live.cache.computations, 2)

        new_spring = Transition(
            instant_at(wall(2024, 3, 10, 2, 30), Duration.hours(-5)),
            Duration.hours(-4),
            "spring",
        )
        live.update((new_spring,), version=2)
        self.assertEqual(live.cache.invalidations, 1)
        second_affected = live.map_local_interval(affected_query)
        second_unaffected = live.map_local_interval(unaffected_query)
        self.assertEqual(live.cache.computations, 3)
        self.assertEqual(first_affected.table_version, 1)
        self.assertEqual(second_affected.table_version, 2)
        self.assertEqual(second_unaffected.table_version, 2)
        self.assertEqual(second_unaffected.utc_union, first_unaffected.utc_union)
        self.assertEqual(live.snapshot(1).transitions[0], old_spring)
        self.assertEqual(live.snapshot(2).transitions[0], new_spring)


class ExactRepresentationTests(unittest.TestCase):
    def test_large_epoch_nanoseconds_round_trip_exactly(self) -> None:
        huge = 10**30 + 123_456_789
        offset = Duration(987_654_321)
        instant = Instant(huge)
        table = TransitionTable(offset, (), 42)
        mapped = map_instant(instant, table)
        self.assertEqual(mapped.naive_nanoseconds, huge + offset.nanoseconds)
        mapping = map_local_interval(
            WallInterval(
                Duration(huge + offset.nanoseconds).to_wall(),
                Duration(huge + offset.nanoseconds + 10).to_wall(),
            ),
            table,
        )
        self.assertEqual(mapping.utc_union[0].start.unix_nanoseconds, huge)
        self.assertEqual(mapping.utc_union[0].end.unix_nanoseconds, huge + 10)
        self.assertEqual(mapping.table_version, 42)


if __name__ == "__main__":
    unittest.main()
