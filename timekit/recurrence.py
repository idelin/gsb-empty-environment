from __future__ import annotations

from dataclasses import dataclass
from enum import Enum

from .convert import Disambiguation
from .model import (
    Duration,
    Instant,
    WallClock,
    days_in_month,
)
from .table import Transition, TransitionTable, instant_at


class Frequency(str, Enum):
    DAILY = "daily"
    MONTHLY = "monthly"
    YEARLY = "yearly"


class InvalidDatePolicy(str, Enum):
    CLAMP = "clamp"
    SKIP = "skip"


@dataclass(frozen=True, slots=True)
class RecurrenceRule:
    frequency: Frequency
    first: WallClock
    local_hour: int
    local_minute: int = 0
    local_second: int = 0
    local_nanosecond: int = 0
    last: WallClock | None = None
    month_day: int | None = None
    disambiguation: Disambiguation = "earlier"
    invalid_date_policy: InvalidDatePolicy = InvalidDatePolicy.CLAMP

    def __post_init__(self) -> None:
        if not isinstance(self.frequency, Frequency):
            raise TypeError("frequency must be a Frequency")
        if self.frequency is not Frequency.DAILY and self.month_day is None:
            object.__setattr__(self, "month_day", self.first.day)
        if self.frequency is Frequency.DAILY and self.month_day is not None:
            raise ValueError("daily recurrence cannot constrain month_day")
        if self.month_day is not None and not 1 <= self.month_day <= 31:
            raise ValueError("month_day must be in [1, 31]")
        if self.last is not None and self.last < self.first:
            raise ValueError("last must not precede first")
        if self.disambiguation not in ("earlier", "later", "raise", "gap"):
            raise ValueError("invalid disambiguation policy")
        if not isinstance(self.invalid_date_policy, InvalidDatePolicy):
            raise TypeError("invalid_date_policy must be an InvalidDatePolicy")
        for name, value, limit in (
            ("local_hour", self.local_hour, 24),
            ("local_minute", self.local_minute, 60),
            ("local_second", self.local_second, 60),
        ):
            if not 0 <= value < limit:
                raise ValueError(f"{name} must be in [0, {limit})")
        if not 0 <= self.local_nanosecond < 1_000_000_000:
            raise ValueError("local_nanosecond must be in [0, 1_000_000_000)")

    def local_events(self) -> list[WallClock]:
        if self.last is None:
            raise ValueError("local_events requires a finite last wall-clock time")
        return self.local_events_between(self.first, self.last.add_calendar_days(1))

    def local_events_between(
        self, start: WallClock, end: WallClock
    ) -> list[WallClock]:
        if end < start:
            raise ValueError("end must not precede start")
        result: list[WallClock] = []
        current = self._with_time(self.first.year, self.first.month, self.first.day)
        while current < end:
            if current >= start:
                result.append(current)
            current = self._next_valid(current, end)
        return result

    def _with_time(self, year: int, month: int, day: int) -> WallClock:
        return WallClock.from_components(
            year,
            month,
            day,
            self.local_hour,
            self.local_minute,
            self.local_second,
            self.local_nanosecond,
        )

    def _raw_next(self, current: WallClock) -> tuple[int, int, int]:
        if self.frequency is Frequency.DAILY:
            following = current.add_calendar_days(1)
            return following.year, following.month, following.day
        if self.frequency is Frequency.MONTHLY:
            following = current.add_calendar_months(1)
            return following.year, following.month, self.month_day or current.day
        following = current.add_calendar_years(1)
        return following.year, following.month, self.month_day or current.day

    def _next_valid(self, current: WallClock, end: WallClock) -> WallClock:
        year, month, day = self._raw_next(current)
        while day > days_in_month(year, month):
            if self.invalid_date_policy is InvalidDatePolicy.CLAMP:
                day = days_in_month(year, month)
                break
            probe = self._with_time(year, month, min(day, days_in_month(year, month)))
            if probe >= end:
                return probe
            year, month, day = self._raw_next(probe)
        return self._with_time(year, month, day)


@dataclass(frozen=True, slots=True)
class ScheduledTransition:
    nominal: WallClock
    at: Instant
    offset_after: Duration
    rule: RecurrenceRule
    transition_id: str
    choice_count: int
    evidence: str


@dataclass(frozen=True, slots=True)
class ScheduleBuildResult:
    transitions: tuple[Transition, ...]
    decisions: tuple[ScheduledTransition, ...]


def daily(first: WallClock, hour: int, minute: int = 0, **kwargs: object) -> RecurrenceRule:
    return RecurrenceRule(Frequency.DAILY, first, hour, minute, **kwargs)  # type: ignore[arg-type]


def monthly(
    first: WallClock,
    hour: int,
    minute: int = 0,
    month_day: int | None = None,
    **kwargs: object,
) -> RecurrenceRule:
    return RecurrenceRule(
        Frequency.MONTHLY,
        first,
        hour,
        minute,
        month_day=month_day,
        **kwargs,  # type: ignore[arg-type]
    )


def yearly(
    first: WallClock,
    hour: int,
    minute: int = 0,
    month_day: int | None = None,
    **kwargs: object,
) -> RecurrenceRule:
    return RecurrenceRule(
        Frequency.YEARLY,
        first,
        hour,
        minute,
        month_day=month_day,
        **kwargs,  # type: ignore[arg-type]
    )


def build_transitions(
    initial_offset: Duration,
    schedules: list[tuple[str, RecurrenceRule, Duration]],
) -> ScheduleBuildResult:
    decisions: list[ScheduledTransition] = []
    transitions: list[Transition] = []
    seen_ids: set[str] = set()
    emitted: set[tuple[int, int, int]] = set()

    for rule_index, (rule_id, rule, offset_after) in enumerate(schedules):
        provisional = TransitionTable(initial_offset, tuple(transitions), "scheduling")
        for nominal in rule.local_events():
            transition_id = _transition_id(rule_id, nominal, rule_index)
            if transition_id in seen_ids:
                raise DuplicateScheduledTransition(transition_id)
            seen_ids.add(transition_id)
            at, choice_count, evidence = _disambiguate(
                nominal, provisional, offset_after, rule
            )
            dedupe_key = (nominal.naive_nanoseconds, at.unix_nanoseconds, offset_after.nanoseconds)
            if dedupe_key in emitted:
                continue
            emitted.add(dedupe_key)
            transition = Transition(at, offset_after, transition_id)
            transitions.append(transition)
            provisional = TransitionTable(
                initial_offset,
                tuple(sorted(transitions, key=lambda item: item.at)),
                "scheduling",
            )
            decisions.append(
                ScheduledTransition(
                    nominal,
                    at,
                    offset_after,
                    rule,
                    transition_id,
                    choice_count,
                    evidence,
                )
            )

    transitions.sort(key=lambda item: item.at)
    decisions.sort(key=lambda item: item.at)
    if any(left.at == right.at for left, right in zip(transitions, transitions[1:])):
        raise ValueError("scheduled rules produce simultaneous transitions")
    return ScheduleBuildResult(tuple(transitions), tuple(decisions))


class DuplicateScheduledTransition(ValueError):
    pass


def _disambiguate(
    nominal: WallClock,
    table: TransitionTable,
    target_offset: Duration,
    rule: RecurrenceRule,
) -> tuple[Instant, int, str]:
    current_offset = table.offset_at(instant_at(nominal, target_offset))
    candidates = sorted(
        {
            instant_at(nominal, current_offset),
            instant_at(nominal, target_offset),
        }
    )
    if len(candidates) > 1:
        if rule.disambiguation == "raise":
            raise AmbiguousScheduledRule(nominal, candidates)
        if rule.disambiguation == "gap":
            raise AmbiguousScheduledRule(nominal, candidates)
        if rule.disambiguation == "later":
            return candidates[-1], len(candidates), "later-utc-candidate"
    selected = candidates[0]
    evidence = "unique-offset" if len(candidates) == 1 else "earlier-utc-candidate"
    return selected, len(candidates), evidence


class AmbiguousScheduledRule(ValueError):
    def __init__(self, nominal: WallClock, candidates: list[Instant]) -> None:
        super().__init__(
            f"scheduled wall time {nominal} has {len(candidates)} transition candidates"
        )
        self.nominal = nominal
        self.candidates = tuple(candidates)


def _transition_id(rule_id: str, wall: WallClock, rule_index: int) -> str:
    return (
        f"{rule_id}:{wall.year:04d}-{wall.month:02d}-{wall.day:02d}"
        f"T{wall.nanoseconds_within_day:013d}#{rule_index}"
    )
