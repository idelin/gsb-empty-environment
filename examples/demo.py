"""Runnable demo of the synthetic offset-transition table library.

Run from the repository root:  python3.11 examples/demo.py
"""

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from tztable import (  # noqa: E402
    ConversionService,
    Duration,
    Instant,
    MonthlyRule,
    TableRegistry,
    Transition,
    TransitionTable,
    WallClock,
    generate_transitions,
)


def banner(title):
    print(f"\n=== {title} ===")


def main():
    # ------------------------------------------------------------------
    banner("1. Build a synthetic table (fall-back overlap)")
    table = TransitionTable(
        version="v1",
        base_offset=Duration.of(hours=2),
        transitions=(
            Transition(
                utc=WallClock.of(2024, 10, 27).to_instant_equiv(),
                offset_after=Duration.of(hours=1),
            ),
        ),
    )
    print("table v1: base UTC+02:00, falls back to UTC+01:00 at 2024-10-27T00:00Z")
    print("local overlap: [2024-10-27T01:00, 2024-10-27T02:00) happens twice")

    banner("2. Wall -> all candidate instants, with evidence")
    wall = WallClock.of(2024, 10, 27, 1, 30)
    for c in table.wall_to_instants(wall):
        print(f"  {wall} -> {c.instant}  fold={c.fold}  [{c.evidence}]")

    banner("3. Local interval [01:15, 01:45) maps to two UTC intervals")
    mapping = table.map_wall_interval(
        WallClock.of(2024, 10, 27, 1, 15), WallClock.of(2024, 10, 27, 1, 45)
    )
    for cand in mapping.candidates:
        for iv in cand.utc_intervals:
            print(f"  candidate: UTC [{iv.start}, {iv.end})  [{cand.evidence}]")
    print(f"  missing local segments: {mapping.missing or 'none'}")

    banner("4. earlier/later round trip preserves identity")
    for policy in ("earlier", "later"):
        chosen = table.resolve_wall(wall, policy)
        reading = table.instant_to_wall(chosen.instant)
        back = table.wall_to_instants(reading.wall)[reading.fold]
        print(
            f"  {policy:7s}: {wall} -> {chosen.instant} (fold={chosen.fold}) "
            f"-> wall {reading.wall} (fold={reading.fold}) -> {back.instant}"
        )
        assert back.instant == chosen.instant

    banner("5. Spring-forward gap reports missing segments")
    gap_table = TransitionTable(
        version="gap-v1",
        base_offset=Duration.of(hours=1),
        transitions=(
            Transition(
                utc=WallClock.of(2024, 3, 31, 1).to_instant_equiv(),
                offset_after=Duration.of(hours=2),
            ),
        ),
    )
    mapping = gap_table.map_wall_interval(
        WallClock.of(2024, 3, 31, 1, 30), WallClock.of(2024, 3, 31, 3, 30)
    )
    for cand in mapping.candidates:
        for iv in cand.utc_intervals:
            print(f"  candidate: UTC [{iv.start}, {iv.end})")
    for gap in mapping.missing:
        print(f"  MISSING local segment: [{gap.start}, {gap.end})")

    banner("6. Consecutive fall-backs: three candidates, not two")
    crazy = TransitionTable(
        version="crazy-v1",
        base_offset=Duration.of(hours=3),
        transitions=(
            Transition(
                utc=WallClock.of(2024, 1, 1).to_instant_equiv(),
                offset_after=Duration.of(hours=2),
            ),
            Transition(
                utc=WallClock.of(2024, 1, 1, 0, 30).to_instant_equiv(),
                offset_after=Duration.of(hours=1),
            ),
        ),
    )
    wall3 = WallClock.of(2024, 1, 1, 2, 15)
    for c in crazy.wall_to_instants(wall3):
        print(f"  {wall3} -> {c.instant}  fold={c.fold}")

    banner("7. Periodic rules: calendar stepping, then disambiguation")
    base = TransitionTable(
        version="rules-v1", base_offset=Duration.of(hours=1), transitions=()
    )
    spring = MonthlyRule(
        month=3, weekday=6, ordinal=-1, hour=2,
        offset_after=Duration.of(hours=2), name="spring-forward",
    )
    fall = MonthlyRule(
        month=10, weekday=6, ordinal=-1, hour=3,
        offset_after=Duration.of(hours=1), name="fall-back",
    )
    ruled = generate_transitions(base, [spring, fall], 2024, 2026, version="rules-v2")
    for t in ruled.transitions:
        reading = ruled.instant_to_wall(t.utc.minus(Duration.of(nanoseconds=1)))
        print(f"  transition at {t.utc}  (wall just before: {reading.wall})")
    again = generate_transitions(ruled, [spring, fall], 2024, 2026, version="rules-v3")
    print(f"  re-generating same years: {len(ruled.transitions)} -> "
          f"{len(again.transitions)} transitions (deduplicated)")

    banner("8. Hot reload: versions are immutable snapshots")
    registry = TableRegistry()
    registry.publish(table)  # v1 with the fall-back
    service = ConversionService(registry)
    ambiguous = WallClock.of(2024, 10, 27, 1, 30)
    print(f"  current={registry.current_version}: "
          f"{len(service.wall_to_instants(ambiguous))} candidates for {ambiguous}")
    registry.publish(
        TransitionTable(version="v2", base_offset=Duration.of(hours=2))
    )
    print(f"  current={registry.current_version}: "
          f"{len(service.wall_to_instants(ambiguous))} candidate for {ambiguous}")
    pinned = service.wall_to_instants(ambiguous, version="v1")
    print(f"  pinned v1 snapshot still yields {len(pinned)} candidates")

    banner("9. Large-epoch precision (integer nanoseconds)")
    huge = Instant(2**100 + 123_456_789)
    reading = table.instant_to_wall(huge)
    print(f"  instant 2**100 + 123456789 -> wall {reading.wall} "
          f"(offset {reading.offset.format_offset()})")
    back = table.wall_to_instants(reading.wall)[0]
    print(f"  round trip exact: {back.instant == huge}")


if __name__ == "__main__":
    main()
