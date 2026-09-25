"""Runnable sample: synthetic offset table, forward and inverse effective time.

Run with:  python3.11 demo.py
"""

from datetime import datetime, time, timezone

from schedule import (ClosedInterval, DailyWindow, Schedule,
                      add_effective_duration, effective_duration)
from timepoint import (Duration, Instant, OffsetTable, OffsetTransition,
                       WallTime)

HOUR = 3600


def utc(iso: str) -> Instant:
    return Instant.from_utc(datetime.fromisoformat(iso).replace(tzinfo=timezone.utc))


def build_table() -> OffsetTable:
    """Synthetic zone: UTC+0, springs forward to UTC+1 on 2024-03-31 02:00Z,
    falls back to UTC+0 on 2024-10-27 02:00Z."""
    return OffsetTable(
        base_offset=Duration(0),
        transitions=(
            OffsetTransition(at=utc("2024-03-31T02:00:00"),
                             offset_after=Duration(HOUR)),
            OffsetTransition(at=utc("2024-10-27T02:00:00"),
                             offset_after=Duration(0)),
        ),
    )


def show(label: str, value: object) -> None:
    print(f"  {label:<58} {value}")


def main() -> None:
    table = build_table()
    window = DailyWindow(opens=time(1, 0), closes=time(4, 0))

    print("== 1. Spring forward: open 01:00-04:00 wall, offset +1h at 02:00Z ==")
    schedule = Schedule(table, [window])
    start = table.to_instant(WallTime.of(2024, 3, 31, 1, 0))
    end = table.to_instant(WallTime.of(2024, 3, 31, 4, 0))
    show("wall 01:00 -> instant", start)
    show("wall 04:00 -> instant", end)
    show("effective_duration(01:00, 04:00) on spring-forward day",
         effective_duration(schedule, start, end))

    print("== 2. Fall back: open 01:00-04:00 wall, offset -1h at 02:00Z ==")
    start = table.to_instant(WallTime.of(2024, 10, 27, 1, 0))
    end = table.to_instant(WallTime.of(2024, 10, 27, 4, 0))
    show("effective_duration(01:00, 04:00) on fall-back day",
         effective_duration(schedule, start, end))

    print("== 3. Overlapping closed intervals are deducted only once ==")
    closed = [
        ClosedInterval.from_wall(table, WallTime.of(2024, 5, 6, 2, 0),
                                 WallTime.of(2024, 5, 6, 3, 0)),
        ClosedInterval.from_wall(table, WallTime.of(2024, 5, 6, 2, 30),
                                 WallTime.of(2024, 5, 6, 3, 30)),
    ]
    schedule = Schedule(table, [window], closed)
    start = table.to_instant(WallTime.of(2024, 5, 6, 1, 0))
    end = table.to_instant(WallTime.of(2024, 5, 6, 4, 0))
    show("closed 02:00-03:00 and 02:30-03:30 (overlap 30m)", "merged 02:00-03:30")
    show("effective_duration(01:00, 04:00) = 3h - 1.5h",
         effective_duration(schedule, start, end))

    print("== 4. Inverse: add effective duration, skipping closed time ==")
    closed = [ClosedInterval.from_wall(table, WallTime.of(2024, 5, 6, 2, 0),
                                       WallTime.of(2024, 5, 6, 2, 30))]
    schedule = Schedule(table, [window], closed)
    start = table.to_instant(WallTime.of(2024, 5, 6, 1, 0))
    result = add_effective_duration(schedule, start, Duration.of(hours=1, minutes=30))
    show("start 01:00 wall + 1h30m effective (closed 02:00-02:30)",
         f"{result} = wall {table.to_wall(result).naive.time()}")

    print("== 5. Inverse landing exactly on a closed interval jumps forward ==")
    result = add_effective_duration(schedule, start, Duration.of(hours=1))
    show("start 01:00 wall + 1h effective (raw end 02:00 is closed)",
         f"{result} = wall {table.to_wall(result).naive.time()}")

    print("== 6. Inverse across an offset transition (real duration) ==")
    schedule = Schedule(table, [window])
    start = table.to_instant(WallTime.of(2024, 3, 31, 1, 0))
    result = add_effective_duration(schedule, start, Duration.of(hours=2))
    show("spring-forward day: 01:00 wall + 2h effective",
         f"{result} = wall {table.to_wall(result).naive.time()}")

    print("== 7. Inverse spanning midnight ==")
    start = table.to_instant(WallTime.of(2024, 5, 6, 2, 0))
    result = add_effective_duration(schedule, start, Duration.of(hours=4))
    show("start 05-06 02:00 wall + 4h effective (window 01:00-04:00 daily)",
         f"{result} = wall {table.to_wall(result)}")


if __name__ == "__main__":
    main()
