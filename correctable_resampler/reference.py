from __future__ import annotations

from bisect import bisect_left
from dataclasses import dataclass
from decimal import Decimal
from fractions import Fraction
from typing import Optional

from .arithmetic import (
    ExactTime,
    grid_time,
    interpolate_linear,
    round_output,
    to_fraction,
)


@dataclass(frozen=True)
class ReferencePoint:
    index: int
    time: Fraction
    mode: str
    basis: str
    valid: bool
    value: Optional[Decimal]
    sample_ids: tuple[str, ...]
    reason: Optional[str]


def replay_grid(
    events: list[tuple],
    origin: ExactTime,
    period: ExactTime,
    mode: str,
    max_missing_interval: ExactTime,
    indices: list[int],
) -> dict[int, ReferencePoint]:
    start = to_fraction(origin)
    step = to_fraction(period)
    missing_limit = to_fraction(max_missing_interval)
    inserts: dict[str, tuple[Fraction, Fraction, int]] = {}
    retracted: set[str] = set()
    breakpoints: set[Fraction] = set()

    for event in events:
        kind = event[0]
        if kind == "sample":
            _, sample_id, sample_time, value = event
            inserts[sample_id] = (
                to_fraction(sample_time),
                to_fraction(value),
                len(inserts),
            )
        elif kind == "retract":
            retracted.add(event[1])
        elif kind == "breakpoint":
            breakpoints.add(to_fraction(event[1]))
        else:
            raise ValueError(f"unknown event: {kind}")

    active = {
        sample_id: item
        for sample_id, item in inserts.items()
        if sample_id not in retracted
    }
    by_time: dict[Fraction, str] = {}
    for sample_id, (sample_time, _, sequence) in active.items():
        current = by_time.get(sample_time)
        if current is None or active[current][2] < sequence:
            by_time[sample_time] = sample_id
    times = sorted(by_time)
    breakpoint_list = sorted(breakpoints)
    result: dict[int, ReferencePoint] = {}

    def unknown(index: int, point_time: Fraction, basis: str, reason: str, ids=()):
        return ReferencePoint(
            index, point_time, mode, basis, False, None, tuple(ids), reason
        )

    for index in indices:
        point_time = grid_time(start, step, index)
        if not times:
            result[index] = unknown(index, point_time, "none", "no_samples")
            continue
        direct = bisect_left(times, point_time)
        if direct < len(times) and times[direct] == point_time:
            sample_id = by_time[point_time]
            result[index] = ReferencePoint(
                index,
                point_time,
                mode,
                "observed",
                True,
                round_output(active[sample_id][1]),
                (sample_id,),
                None,
            )
            continue

        left_id = by_time[times[direct - 1]] if direct > 0 else None
        right_id = by_time[times[direct]] if direct < len(times) else None
        left_time = active[left_id][0] if left_id else None
        right_time = active[right_id][0] if right_id else None

        if mode == "linear":
            if left_id is None:
                result[index] = unknown(
                    index, point_time, "none", "before_first_sample"
                )
                continue
            if right_id is None:
                result[index] = unknown(
                    index, point_time, "none", "after_last_sample", (left_id,)
                )
                continue
            ids = (left_id, right_id)
            if any(left_time < point < right_time for point in breakpoint_list):
                result[index] = unknown(index, point_time, "linear", "breakpoint", ids)
                continue
            if right_time - left_time > missing_limit:
                result[index] = unknown(index, point_time, "linear", "gap_too_long", ids)
                continue
            value = interpolate_linear(
                point_time,
                left_time,
                active[left_id][1],
                right_time,
                active[right_id][1],
            )
            result[index] = ReferencePoint(
                index, point_time, mode, "linear", True, round_output(value), ids, None
            )
        else:
            if left_id is None:
                ids = (right_id,) if right_id else ()
                result[index] = unknown(
                    index, point_time, "none", "before_first_sample", ids
                )
                continue
            ids = (left_id,)
            if any(left_time < point <= point_time for point in breakpoint_list):
                result[index] = unknown(index, point_time, "step", "breakpoint", ids)
                continue
            if point_time - left_time > missing_limit:
                result[index] = unknown(index, point_time, "step", "gap_too_long", ids)
                continue
            result[index] = ReferencePoint(
                index,
                point_time,
                mode,
                "step",
                True,
                round_output(active[left_id][1]),
                ids,
                None,
            )
    return result
