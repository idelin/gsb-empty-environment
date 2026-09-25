from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

from timekit import (  # noqa: E402
    Duration,
    InstantInterval,
    LiveTransitionTable,
    Transition,
    TransitionTable,
    WallClock,
    WallInterval,
    build_transitions,
    map_local_interval,
    map_local_point,
    map_utc_interval,
    yearly,
)
from timekit.table import instant_at, wall_at  # noqa: E402


def wall(hour: int, minute: int, *, month: int = 11, day: int = 3) -> WallClock:
    return WallClock.from_components(2024, month, day, hour, minute)


def title(text: str) -> None:
    print(f"\n{text}\n{'-' * len(text)}")


def main() -> None:
    spring = Transition(
        instant_at(wall(2, 0, month=3, day=10), Duration.hours(-5)),
        Duration.hours(-4),
        "spring-2024",
    )
    fall = Transition(
        instant_at(wall(2, 0), Duration.hours(-4)),
        Duration.hours(-5),
        "fall-2024",
    )
    fall_table = TransitionTable(Duration.hours(-4), (fall,), version="v1")
    spring_table = TransitionTable(Duration.hours(-5), (spring,), version="v1")

    title("fall-back point and interval")
    repeated = wall(1, 15)
    point = map_local_point(repeated, fall_table)
    print("wall:", repeated)
    for candidate in point.candidates:
        print(
            "candidate UTC:",
            candidate.instant.unix_nanoseconds,
            "offset hours:",
            candidate.evidence.offset.nanoseconds // 3_600_000_000_000,
        )

    interval = WallInterval(wall(1, 15), wall(1, 45))
    mapped_interval = map_local_interval(interval, fall_table)
    print("wall interval UTC candidates:", len(mapped_interval.candidates))
    for candidate in mapped_interval.candidates:
        print(
            candidate.utc_interval.start.unix_nanoseconds,
            "->",
            candidate.utc_interval.end.unix_nanoseconds,
        )

    title("spring-forward missing wall-clock fragments")
    gap_interval = WallInterval(wall(1, 45, month=3, day=10), wall(3, 30, month=3, day=10))
    gap_mapping = map_local_interval(gap_interval, spring_table)
    for gap in gap_mapping.missing_wall_parts:
        print(
            "missing:",
            gap.wall_interval.start,
            "->",
            gap.wall_interval.end,
            "at transition",
            gap.transition_id,
        )

    utc_mapping = map_utc_interval(
        InstantInterval(
            instant_at(wall(1, 0, month=3, day=10), Duration.hours(-5)),
            instant_at(wall(4, 0, month=3, day=10), Duration.hours(-4)),
        ),
        spring_table,
    )
    print("UTC interval wall pieces:", len(utc_mapping.wall_pieces))
    print("skipped wall gaps:", [gap.transition_id for gap in utc_mapping.skipped_wall_gaps])

    title("calendar recurrence, then disambiguation")
    scheduled = build_transitions(
        Duration.hours(-5),
        [
            (
                "spring",
                yearly(wall(2, 0, month=3, day=10), 2, last=wall(2, 0, month=3, day=10)),
                Duration.hours(-4),
            ),
            (
                "fall",
                yearly(wall(2, 0), 2, last=wall(2, 0)),
                Duration.hours(-5),
            ),
        ],
    )
    for decision in scheduled.decisions:
        print(
            decision.transition_id,
            "nominal",
            decision.nominal,
            "-> UTC",
            decision.at.unix_nanoseconds,
            decision.evidence,
        )

    title("versioned hot update keeps historical snapshots")
    live = LiveTransitionTable(spring_table)
    before_update = live.map_local_interval(gap_interval)
    moved_spring = Transition(
        instant_at(wall(2, 30, month=3, day=10), Duration.hours(-5)),
        Duration.hours(-4),
        "spring-2024",
    )
    live.update((moved_spring,), version="v2")
    after_update = live.map_local_interval(gap_interval)
    print("snapshot version:", before_update.table_version)
    print("current version:", after_update.table_version)
    print("cached entries invalidated:", live.cache.invalidations)
    print(
        "historical snapshot transition:",
        live.snapshot("v1").transitions[0].at.unix_nanoseconds,
    )
    print(
        "current transition:",
        live.snapshot("v2").transitions[0].at.unix_nanoseconds,
    )
    print("sample direct wall_at:", wall_at(fall.at, Duration.hours(-4)))


if __name__ == "__main__":
    main()
