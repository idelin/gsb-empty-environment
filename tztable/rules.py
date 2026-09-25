"""Periodic transition rules.

Rules such as "last Sunday of March at 02:00 local" are expanded by
*calendar* stepping: each year's wall-clock transition is computed on the
proleptic Gregorian calendar first, and only then disambiguated against the
table built so far.  Fixed durations (e.g. adding 365 * 24h) are never used
as a substitute for calendar stepping -- 2024-03-31 + 1 year is
2025-03-31 on the calendar, not 2025-03-30 23:00 after some fixed span.

Generated transitions are deduplicated: re-expanding a rule over an
overlapping year range does not create duplicate transitions.
"""

from __future__ import annotations

from dataclasses import dataclass

from .core import Duration, WallClock, days_in_month
from .table import Transition, TransitionTable


@dataclass(frozen=True)
class MonthlyRule:
    """A yearly recurring transition on a weekday of a month.

    ``ordinal`` selects which weekday: 1..4 for the nth one, -1 for the
    last.  The local time of day is interpreted in the offset in effect
    immediately before the transition.
    """

    month: int
    weekday: int  # 0 = Monday ... 6 = Sunday
    ordinal: int  # 1..4 = nth weekday of month, -1 = last
    offset_after: Duration
    hour: int = 0
    minute: int = 0
    second: int = 0
    nanosecond: int = 0
    name: str = ""

    def __post_init__(self) -> None:
        if not 1 <= self.month <= 12:
            raise ValueError(f"month out of range: {self.month}")
        if not 0 <= self.weekday <= 6:
            raise ValueError(f"weekday out of range: {self.weekday}")
        if self.ordinal != -1 and not 1 <= self.ordinal <= 4:
            raise ValueError(f"ordinal out of range: {self.ordinal}")

    def local_transition(self, year: int) -> WallClock:
        """Calendar-computed local transition wall time for ``year``."""
        if self.ordinal == -1:
            day = days_in_month(year, self.month)
            candidate = WallClock.of(
                year, self.month, day,
                self.hour, self.minute, self.second, self.nanosecond,
            )
            shift = (candidate.weekday() - self.weekday) % 7
            return candidate.plus_days(-shift)
        candidate = WallClock.of(
            year, self.month, 1,
            self.hour, self.minute, self.second, self.nanosecond,
        )
        shift = (self.weekday - candidate.weekday()) % 7
        return candidate.plus_days(shift + 7 * (self.ordinal - 1))


def generate_transitions(
    table: TransitionTable,
    rules: "MonthlyRule | list[MonthlyRule] | tuple[MonthlyRule, ...]",
    start_year: int,
    end_year: int,
    *,
    version: str | None = None,
) -> TransitionTable:
    """Expand ``rules`` for years [start_year, end_year] into a new table.

    Every rule/year pair is first computed as a wall-clock time on the local
    calendar; the pending transitions are then applied one at a time in
    chronological order, each disambiguated against the table built so far
    (the offset in effect just before the transition is used; a wall time
    inside a gap shifts to the gap's UTC boundary).  This interleaving
    matters: a spring-forward rule and a fall-back rule only produce
    correct UTC instants when each sees the other's earlier transitions.

    Transitions identical to existing ones are deduplicated, so
    re-generating overlapping year ranges is a no-op.  The input ``table``
    is not modified; a new versioned snapshot is returned.
    """
    if end_year < start_year:
        raise ValueError("end_year must be >= start_year")
    if isinstance(rules, MonthlyRule):
        rules = (rules,)

    pending: list[tuple[WallClock, MonthlyRule]] = [
        (rule.local_transition(year), rule)
        for rule in rules
        for year in range(start_year, end_year + 1)
    ]

    merged = list(table.transitions)
    known = {(t.utc, t.offset_after) for t in merged}
    current = table

    def already_explained(wall: WallClock, rule: MonthlyRule) -> bool:
        # An existing transition "explains" this rule occurrence when, in
        # the offset in effect just before it, its local time equals the
        # rule's calendar wall time.  This keeps re-generation idempotent
        # even after later transitions change how that wall time resolves.
        for existing in current.transitions:
            if existing.offset_after != rule.offset_after:
                continue
            just_before = current.instant_to_wall(
                existing.utc.minus(Duration.of(nanoseconds=1))
            )
            local = existing.utc.plus(just_before.offset).to_wall()
            if local == wall:
                return True
        return False

    while pending:
        # Disambiguate every pending wall time against the current table,
        # then apply the chronologically earliest one first.
        def to_utc(item: tuple[WallClock, MonthlyRule]):
            wall, _rule = item
            candidates = current.wall_to_instants(wall)
            if candidates:
                # Offset in effect just before: the latest occurrence.
                return candidates[-1].instant
            # Wall time fell into a gap: use the UTC instant of the
            # transition that created the gap.
            gap_end = current._gap_end_after(wall)
            return current.wall_to_instants(gap_end)[0].instant

        wall, rule = min(pending, key=lambda item: to_utc(item).epoch_nanos)
        pending.remove((wall, rule))
        if already_explained(wall, rule):
            continue  # deduplicate repeated periods
        transition = Transition(utc=to_utc((wall, rule)), offset_after=rule.offset_after)
        if (transition.utc, transition.offset_after) in known:
            continue  # deduplicate repeated periods
        known.add((transition.utc, transition.offset_after))
        merged.append(transition)
        merged.sort(key=lambda t: t.utc.epoch_nanos)
        current = TransitionTable(
            version="_pending",
            base_offset=table.base_offset,
            transitions=tuple(merged),
        )

    return TransitionTable(
        version=version if version is not None else table.version,
        base_offset=table.base_offset,
        transitions=tuple(merged),
    )
