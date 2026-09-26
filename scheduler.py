"""Continuous track scheduling with safety buffers and maintenance blackouts.

An operation must occupy one compatible track continuously for its duration,
plus a safety buffer before and after. Buffers consume track capacity.
All intervals are half-open [start, end): touching boundaries is not a
conflict. Numbers are exact rationals (fractions.Fraction); no solver is used.

Input instance (JSON):
{
  "operation": {"id": "op1", "duration": "3",
                "pre_buffer": "1/2", "post_buffer": "1/2"},
  "horizon":   {"start": 0, "end": 24},          # optional; end may be null
  "budget":    "20",                              # optional latest op end
  "tracks": [
    {"id": "T1",
     "maintenance": [["6", "9"]],
     "bookings": [{"start": "2", "end": "4",
                   "pre_buffer": 0, "post_buffer": 0}]}
  ]
}

Output (JSON): feasibility, optimal (earliest-start) placement, budget
status (met / violated / undecided), per-track candidates, and conflict
evidence when infeasible.
"""

from __future__ import annotations

import json
import sys
from fractions import Fraction


# ---------------------------------------------------------------- numbers

def parse_number(value, field="value"):
    """Parse an int, float, or 'p'/'p/q'/'x.y' string into an exact Fraction."""
    if isinstance(value, Fraction):
        return value
    if isinstance(value, bool):
        raise ValueError(f"{field}: boolean is not a number")
    if isinstance(value, int):
        return Fraction(value)
    if isinstance(value, float):
        return Fraction(str(value))
    if isinstance(value, str):
        try:
            return Fraction(value)
        except (ValueError, ZeroDivisionError) as exc:
            raise ValueError(f"{field}: cannot parse {value!r}") from exc
    raise ValueError(f"{field}: unsupported number {value!r}")


def fmt(q):
    """Format a Fraction as 'p' or 'p/q'; None (unbounded) as null."""
    if q is None:
        return None
    if q.denominator == 1:
        return str(q.numerator)
    return f"{q.numerator}/{q.denominator}"


# ---------------------------------------------------------------- intervals

def overlaps(a_start, a_end, b_start, b_end):
    """Half-open overlap test: [a_start, a_end) vs [b_start, b_end)."""
    return a_start < b_end and b_start < a_end


def merge_intervals(intervals):
    """Merge overlapping or touching half-open intervals.

    `intervals` is a list of (start, end, label). Returns a list of
    (start, end, [labels]) sorted by start.
    """
    merged = []
    for start, end, label in sorted(intervals, key=lambda iv: (iv[0], iv[1])):
        if merged and start <= merged[-1][1]:
            last = merged[-1]
            if end > last[1]:
                merged[-1] = (last[0], end, last[2])
            merged[-1][2].append(label)
        else:
            merged.append((start, end, [label]))
    return merged


# ---------------------------------------------------------------- solving

def _parse_operation(raw):
    op = raw.get("operation")
    if not isinstance(op, dict):
        raise ValueError("instance.operation is required")
    duration = parse_number(op.get("duration"), "operation.duration")
    if duration <= 0:
        raise ValueError("operation.duration must be positive")
    pre = parse_number(op.get("pre_buffer", 0), "operation.pre_buffer")
    post = parse_number(op.get("post_buffer", 0), "operation.post_buffer")
    if pre < 0 or post < 0:
        raise ValueError("operation buffers must be non-negative")
    return {
        "id": op.get("id", "op"),
        "duration": duration,
        "pre_buffer": pre,
        "post_buffer": post,
    }


def _parse_horizon(raw):
    hz = raw.get("horizon") or {}
    start = parse_number(hz.get("start", 0), "horizon.start")
    end_raw = hz.get("end")
    end = parse_number(end_raw, "horizon.end") if end_raw is not None else None
    if end is not None and end <= start:
        raise ValueError("horizon.end must be greater than horizon.start")
    return start, end


def _track_forbidden(track, horizon_start, horizon_end):
    """Collect this track's capacity-consuming intervals, clipped to horizon."""
    forbidden = []
    for i, win in enumerate(track.get("maintenance") or []):
        if not (isinstance(win, (list, tuple)) and len(win) == 2):
            raise ValueError(f"track {track.get('id')}: maintenance[{i}] must be [start, end]")
        ws = parse_number(win[0], "maintenance start")
        we = parse_number(win[1], "maintenance end")
        if we <= ws:
            raise ValueError(f"track {track.get('id')}: maintenance[{i}] end <= start")
        forbidden.append((ws, we, f"maintenance[{fmt(ws)},{fmt(we)})"))
    for i, bk in enumerate(track.get("bookings") or []):
        bs = parse_number(bk.get("start"), "booking.start")
        be = parse_number(bk.get("end"), "booking.end")
        bpre = parse_number(bk.get("pre_buffer", 0), "booking.pre_buffer")
        bpost = parse_number(bk.get("post_buffer", 0), "booking.post_buffer")
        if be <= bs:
            raise ValueError(f"track {track.get('id')}: bookings[{i}] end <= start")
        if bpre < 0 or bpost < 0:
            raise ValueError(f"track {track.get('id')}: bookings[{i}] buffers must be non-negative")
        rs, re_ = bs - bpre, be + bpost
        label = f"booking[{fmt(bs)},{fmt(be)})+buffers[{fmt(rs)},{fmt(re_)})"
        forbidden.append((rs, re_, label))
    clipped = []
    for start, end, label in forbidden:
        cs = max(start, horizon_start)
        ce = min(end, horizon_end) if horizon_end is not None else end
        if cs < ce:
            clipped.append((cs, ce, label))
    return clipped


def _free_gaps(merged_forbidden, horizon_start, horizon_end):
    """Complement of merged forbidden intervals within the horizon."""
    gaps = []
    cursor = horizon_start
    for start, end, _labels in merged_forbidden:
        if start > cursor:
            gaps.append((cursor, start))
        if end > cursor:
            cursor = end
    if horizon_end is None or cursor < horizon_end:
        gaps.append((cursor, horizon_end))
    return gaps


def _gap_fits(gap, required):
    gstart, gend = gap
    return gend is None or gend - gstart >= required


def solve(instance):
    """Solve one instance; return a JSON-serializable result dict."""
    op = _parse_operation(instance)
    horizon_start, horizon_end = _parse_horizon(instance)
    budget_raw = instance.get("budget")
    budget = parse_number(budget_raw, "budget") if budget_raw is not None else None

    duration = op["duration"]
    pre = op["pre_buffer"]
    post = op["post_buffer"]
    required = pre + duration + post  # reserved length, buffers included

    tracks = instance.get("tracks")
    if tracks is None:
        tracks = []
    if not tracks:
        return {
            "operation": op["id"],
            "feasible": False,
            "optimal": None,
            "budget_status": "undecided",
            "candidates": [],
            "evidence": {
                "reason": "no compatible tracks: the compatible track set is empty",
                "required_reserved_length": fmt(required),
                "tracks": [],
            },
        }

    candidates = []
    track_reports = []
    for track in tracks:
        tid = track.get("id")
        forbidden = _track_forbidden(track, horizon_start, horizon_end)
        merged = merge_intervals(forbidden)
        gaps = _free_gaps(merged, horizon_start, horizon_end)

        best = None
        for gap in gaps:
            if _gap_fits(gap, required):
                best = gap[0] + pre  # reserved start = gap start; op starts after pre-buffer
                break
        if best is not None:
            candidates.append({
                "track": tid,
                "start": best,
                "end": best + duration,
                "reserved_start": best - pre,
                "reserved_end": best + duration + post,
            })

        track_reports.append({
            "track": tid,
            "required_reserved_length": fmt(required),
            "forbidden": [
                {"interval": [fmt(s), fmt(e)], "sources": labels}
                for s, e, labels in merged
            ],
            "gaps": [
                {
                    "interval": [fmt(gs), fmt(ge)],
                    "length": fmt(ge - gs) if ge is not None else None,
                    "fits": _gap_fits((gs, ge), required),
                }
                for gs, ge in gaps
            ],
            "earliest_start": fmt(best) if best is not None else None,
        })

    if not candidates:
        for report in track_reports:
            report["reason"] = (
                f"no free gap fits reserved length {fmt(required)} "
                "(duration + safety buffers)"
            )
        return {
            "operation": op["id"],
            "feasible": False,
            "optimal": None,
            "budget_status": "undecided",
            "candidates": [],
            "evidence": {
                "reason": "no compatible track has a free gap fitting the operation",
                "required_reserved_length": fmt(required),
                "tracks": track_reports,
            },
        }

    # Optimal: earliest operation start; deterministic tie-break by track id.
    optimal = min(candidates, key=lambda c: (c["start"], str(c["track"])))

    if budget is None:
        budget_status = "undecided"
    elif optimal["end"] <= budget:
        budget_status = "met"
    else:
        budget_status = "violated"

    return {
        "operation": op["id"],
        "feasible": True,
        "optimal": {
            "track": optimal["track"],
            "start": fmt(optimal["start"]),
            "end": fmt(optimal["end"]),
            "reserved_start": fmt(optimal["reserved_start"]),
            "reserved_end": fmt(optimal["reserved_end"]),
        },
        "budget": fmt(budget),
        "budget_status": budget_status,
        "candidates": [
            {
                "track": c["track"],
                "start": fmt(c["start"]),
                "end": fmt(c["end"]),
                "reserved_start": fmt(c["reserved_start"]),
                "reserved_end": fmt(c["reserved_end"]),
            }
            for c in sorted(candidates, key=lambda c: (c["start"], str(c["track"])))
        ],
        "evidence": None,
    }


# ---------------------------------------------------------------- CLI

def main(argv=None):
    argv = list(sys.argv[1:] if argv is None else argv)
    try:
        if argv:
            with open(argv[0], "r", encoding="utf-8") as fh:
                instance = json.load(fh)
        else:
            instance = json.load(sys.stdin)
    except (OSError, json.JSONDecodeError) as exc:
        print(f"error: cannot read instance: {exc}", file=sys.stderr)
        return 2
    try:
        result = solve(instance)
    except ValueError as exc:
        print(f"error: invalid instance: {exc}", file=sys.stderr)
        return 2
    print(json.dumps(result, indent=2, ensure_ascii=False))
    return 0 if result["feasible"] else 1


if __name__ == "__main__":
    sys.exit(main())
