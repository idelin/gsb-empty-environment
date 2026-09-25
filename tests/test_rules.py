"""Periodic-rule generation tests."""

import unittest

from tztable import Duration, MonthlyRule, TransitionTable, WallClock, generate_transitions
from tztable.core import NANOS_PER_DAY


def utc_nanos(year, month, day, hour=0):
    return WallClock.of(year, month, day, hour).to_instant_equiv().epoch_nanos


class TestCalendarGeneration(unittest.TestCase):
    def test_last_sunday_rules_follow_calendar_not_fixed_duration(self):
        base = TransitionTable(
            version="v1", base_offset=Duration.of(hours=1), transitions=()
        )
        spring = MonthlyRule(
            month=3, weekday=6, ordinal=-1, hour=2,
            offset_after=Duration.of(hours=2), name="spring-forward",
        )
        fall = MonthlyRule(
            month=10, weekday=6, ordinal=-1, hour=3,
            offset_after=Duration.of(hours=1), name="fall-back",
        )
        # Both rules expand together so each year's transitions interleave
        # in chronological order during disambiguation.
        table = generate_transitions(base, [spring, fall], 2024, 2026, version="v2")

        springs = [t for t in table.transitions if t.offset_after == Duration.of(hours=2)]
        falls = [t for t in table.transitions if t.offset_after == Duration.of(hours=1)]
        # Last Sundays of March: 31st/30th/29th at 02:00 local under +1.
        self.assertEqual(
            [t.utc.epoch_nanos for t in springs],
            [utc_nanos(2024, 3, 31, 1), utc_nanos(2025, 3, 30, 1),
             utc_nanos(2026, 3, 29, 1)],
        )
        # Last Sundays of October: 27th/26th/25th at 03:00 local under +2.
        self.assertEqual(
            [t.utc.epoch_nanos for t in falls],
            [utc_nanos(2024, 10, 27, 1), utc_nanos(2025, 10, 26, 1),
             utc_nanos(2026, 10, 25, 1)],
        )
        # 2024 is a leap year: March-to-March spacing is 364 days, not a
        # fixed 365 * 24h -- calendar stepping, not duration arithmetic.
        spring_utcs = [t.utc.epoch_nanos for t in springs]
        self.assertEqual(spring_utcs[1] - spring_utcs[0], 364 * NANOS_PER_DAY)
        self.assertEqual(spring_utcs[2] - spring_utcs[1], 364 * NANOS_PER_DAY)
        # Each spring transition jumps 01:59:59.999999999 -> 03:00 local on
        # the expected calendar day (a one-hour gap, not a duration step).
        for transition, expected_day in zip(springs, (31, 30, 29)):
            before = table.instant_to_wall(
                transition.utc.minus(Duration.of(nanoseconds=1))
            )
            after = table.instant_to_wall(transition.utc)
            self.assertEqual(before.wall.to_ymd()[2], expected_day)
            self.assertEqual(before.wall.to_time(), (1, 59, 59, 999_999_999))
            self.assertEqual(after.wall.to_ymd()[2], expected_day)
            self.assertEqual(after.wall.to_time()[:2], (3, 0))

    def test_nth_weekday_rule(self):
        rule = MonthlyRule(
            month=11, weekday=0, ordinal=1, hour=9,
            offset_after=Duration.of(hours=-5),
        )
        # First Monday of November 2024 is the 4th; 2025 is the 3rd.
        self.assertEqual(
            rule.local_transition(2024), WallClock.of(2024, 11, 4, 9, 0)
        )
        self.assertEqual(
            rule.local_transition(2025), WallClock.of(2025, 11, 3, 9, 0)
        )

    def test_disambiguation_uses_offset_before_transition(self):
        # Fallback rule at 03:00 local while offset +2 is in effect:
        # 03:00 local is ambiguous (occurs at 01:00Z and 02:00Z); the
        # pre-transition offset (+2) selects 01:00Z.
        base = TransitionTable(
            version="v1",
            base_offset=Duration.of(hours=1),
            transitions=(
                # spring forward 2024-03-31T01:00Z -> +2
                __import__("tztable").Transition(
                    utc=__import__("tztable").Instant(utc_nanos(2024, 3, 31, 1)),
                    offset_after=Duration.of(hours=2),
                ),
            ),
        )
        rule = MonthlyRule(
            month=10, weekday=6, ordinal=-1, hour=3,
            offset_after=Duration.of(hours=1), name="fall-back",
        )
        table = generate_transitions(base, rule, 2024, 2024, version="v2")
        generated = table.transitions[-1]
        self.assertEqual(generated.utc.epoch_nanos, utc_nanos(2024, 10, 27, 1))
        # And the resulting overlap is real: 02:30 local occurs twice.
        self.assertEqual(
            len(table.wall_to_instants(WallClock.of(2024, 10, 27, 2, 30))), 2
        )


class TestRepeatedPeriodDedup(unittest.TestCase):
    def test_regenerating_overlapping_years_is_a_no_op(self):
        base = TransitionTable(
            version="v1", base_offset=Duration.of(hours=1), transitions=()
        )
        rule = MonthlyRule(
            month=3, weekday=6, ordinal=-1, hour=2,
            offset_after=Duration.of(hours=2),
        )
        first = generate_transitions(base, rule, 2024, 2026, version="v2")
        self.assertEqual(len(first.transitions), 3)
        # Re-generate an overlapping range: 2024..2026 dedupe, 2027 is new.
        second = generate_transitions(first, rule, 2025, 2027, version="v3")
        self.assertEqual(len(second.transitions), 4)
        self.assertEqual(second.transitions[:3], first.transitions)
        # Generating the exact same range changes nothing at all.
        third = generate_transitions(second, rule, 2024, 2027, version="v4")
        self.assertEqual(third.transitions, second.transitions)

    def test_regeneration_is_idempotent_with_overlapping_rules(self):
        # With a fall-back rule present, a naive re-disambiguation of the
        # fall wall time would resolve differently under the completed
        # table; dedup must still recognise the existing transitions.
        base = TransitionTable(
            version="v1", base_offset=Duration.of(hours=1), transitions=()
        )
        spring = MonthlyRule(
            month=3, weekday=6, ordinal=-1, hour=2,
            offset_after=Duration.of(hours=2),
        )
        fall = MonthlyRule(
            month=10, weekday=6, ordinal=-1, hour=3,
            offset_after=Duration.of(hours=1),
        )
        first = generate_transitions(base, [spring, fall], 2024, 2026, version="v2")
        self.assertEqual(len(first.transitions), 6)
        second = generate_transitions(first, [spring, fall], 2024, 2026, version="v3")
        self.assertEqual(second.transitions, first.transitions)
        # Extending the range only appends the genuinely new years.
        third = generate_transitions(second, [spring, fall], 2024, 2027, version="v4")
        self.assertEqual(len(third.transitions), 8)
        self.assertEqual(third.transitions[:6], first.transitions)


if __name__ == "__main__":
    unittest.main()
