"""JSON CLI for the track scheduler.

Usage: python3 cli.py scenario.json
Prints the scheduling result as JSON on stdout.
"""

from __future__ import annotations

import json
import sys

from scheduler import (
    ForbiddenInterval,
    Interval,
    KIND_MAINTENANCE,
    KIND_OCCUPANCY,
    Operation,
    Track,
    schedule,
    to_fraction,
)


def _intervals(raw_list, kind) -> tuple:
    out = []
    for i, raw in enumerate(raw_list or []):
        if isinstance(raw, dict):
            start, end = raw["start"], raw["end"]
            label = raw.get("label", f"{kind}-{i}")
        else:
            start, end = raw[0], raw[1]
            label = f"{kind}-{i}"
        out.append(
            ForbiddenInterval(
                interval=Interval(to_fraction(start), to_fraction(end)),
                kind=kind,
                label=label,
            )
        )
    return tuple(out)


def load_scenario(path: str) -> dict:
    with open(path, "r", encoding="utf-8") as fh:
        return json.load(fh)


def build_result(scenario: dict):
    op_raw = scenario["operation"]
    operation = Operation(
        name=op_raw.get("name", "op"),
        duration=to_fraction(op_raw["duration"]),
        safety_before=to_fraction(op_raw.get("safety_before", 0)),
        safety_after=to_fraction(op_raw.get("safety_after", 0)),
    )
    tracks = [
        Track(
            track_id=t["id"],
            maintenance=_intervals(t.get("maintenance"), KIND_MAINTENANCE),
            occupied=_intervals(t.get("occupied"), KIND_OCCUPANCY),
        )
        for t in scenario.get("tracks", [])
    ]
    horizon = scenario.get("horizon") or {}
    return schedule(
        operation,
        tracks,
        compatible_track_ids=scenario.get("compatible_tracks"),
        horizon_start=to_fraction(horizon.get("start", 0)),
        horizon_end=to_fraction(horizon["end"]) if "end" in horizon else None,
        budget=to_fraction(scenario["budget"]) if "budget" in scenario else None,
    )


def main(argv) -> int:
    if len(argv) != 2:
        print(__doc__.strip(), file=sys.stderr)
        return 2
    result = build_result(load_scenario(argv[1]))
    print(json.dumps(result.as_dict(), indent=2, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
