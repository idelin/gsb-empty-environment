"""Windowed queries: pagination with versioned cursors and k-th occurrence.

Cursors are opaque tokens that bind together the schedule definition, the
offset-table version, the query window and the last instant already
returned.  Reusing a cursor after any of those change raises
:class:`StaleCursorError` instead of silently continuing against new data.
"""

from __future__ import annotations

import base64
import json
from dataclasses import dataclass

from .rules import Window
from .time_model import Instant


class StaleCursorError(ValueError):
    """Raised when a cursor does not match the schedule, table or window."""


@dataclass(frozen=True)
class Page:
    events: list
    next_cursor: str | None
    jumps: int


def _encode(payload: dict) -> str:
    raw = json.dumps(payload, separators=(",", ":"), sort_keys=True).encode("utf-8")
    return base64.urlsafe_b64encode(raw).decode("ascii").rstrip("=")


def _decode(token: str) -> dict:
    pad = "=" * (-len(token) % 4)
    raw = base64.urlsafe_b64decode(token + pad)
    return json.loads(raw.decode("utf-8"))


def query_page(
    node,
    window: Window,
    *,
    limit: int,
    cursor: str | None = None,
) -> Page:
    """Return up to ``limit`` events and a cursor for the remainder."""
    if limit < 1:
        raise ValueError("limit must be >= 1")
    expected = {
        "fp": node.fingerprint,
        "ver": node.table.version,
        "win": [window.start.epoch_us, window.end.epoch_us],
    }
    after = window.start.epoch_us
    if cursor is not None:
        state = _decode(cursor)
        for key, value in expected.items():
            if state.get(key) != value:
                raise StaleCursorError(
                    "cursor is stale: schedule, table version or window changed"
                )
        after = state["after"]

    page_window = Window(Instant(after + 1), window.end)
    expansion = node.expand(page_window)
    taken = expansion.events[:limit]
    if len(expansion.events) > limit:
        state = dict(expected)
        state["after"] = taken[-1].instant.epoch_us
        next_cursor = _encode(state)
    else:
        next_cursor = None
    return Page(taken, next_cursor, expansion.jumps)


def _iter_anchors(node):
    """Earliest rule start wall time for every leaf schedule in the tree."""
    from .rules import Schedule
    from .composite import CompositeSchedule

    if isinstance(node, Schedule):
        yield node.rule.start
    elif isinstance(node, CompositeSchedule):
        for child in node.children:
            yield from _iter_anchors(child)
    else:
        raise TypeError(f"unsupported node: {node!r}")


def nth_event(node, k: int, *, horizon_months: int = 12000):
    """Return the k-th (1-based) event after the schedule anchor, or None.

    Monthly chunks are jumped over (not day-scanned); the search stops after
    ``horizon_months`` of empty-looking territory past the earliest anchor.
    """
    if k < 1:
        raise ValueError("k must be >= 1")
    import datetime as _dt

    from .time_model import Duration

    anchor_wall = min(_iter_anchors(node), key=lambda w: w.epoch_us)
    year, month = anchor_wall.year, anchor_wall.month
    count = 0
    explored = 0
    while explored < horizon_months:
        start_date = _dt.date(year, month, 1)
        if month == 12:
            end_date = _dt.date(year + 1, 1, 1)
        else:
            end_date = _dt.date(year, month + 1, 1)
        lo = node.table.resolve(
            _wall_from_date(start_date)
        ).instant
        hi = node.table.resolve(_wall_from_date(end_date)).instant
        chunk = Window(lo, hi)
        events = node.expand(chunk).events
        if count + len(events) >= k:
            return events[k - count - 1]
        count += len(events)
        explored += 1
        if month == 12:
            year, month = year + 1, 1
        else:
            month += 1
    return None


def _wall_from_date(date):
    from .time_model import WallTime

    return WallTime(date.year, date.month, date.day, 0)
