"""Data model and JSON input parsing for push suppression decisions.

Time is a finite discrete axis: valid send slots are the integers
``0 .. horizon-1``.  A notification is either not sent or sent at exactly
one slot inside its inclusive ``[release, deadline]`` window.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Mapping

UNSENT = -1


@dataclass(frozen=True)
class Notification:
    """A candidate push notification."""

    nid: str
    user: str
    channel: str
    revenue: float
    release: int
    deadline: int
    cost: float
    activity_id: str | None = None

    def options(self, horizon: int) -> list[int]:
        """Concrete send slots this notification may use, UNSENT added by solver."""
        lo = max(0, self.release)
        hi = min(horizon - 1, self.deadline)
        return list(range(lo, hi + 1))


@dataclass(frozen=True)
class Instance:
    """A decision instance: notifications, capacities and the rolling rule."""

    horizon: int
    notifications: tuple[Notification, ...]
    channel_capacity: Mapping[str, float]
    window_length: int = 3
    max_pushes_per_window: int = 1

    @property
    def by_id(self) -> dict[str, Notification]:
        return {n.nid: n for n in self.notifications}


class InputError(ValueError):
    """Raised when the input document is malformed."""


def _require(obj: Mapping[str, Any], key: str, where: str) -> Any:
    if key not in obj:
        raise InputError(f"missing required field {key!r} in {where}")
    return obj[key]


def _as_int(value: Any, field: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise InputError(f"field {field!r} must be an integer, got {value!r}")
    return value


def _as_number(value: Any, field: str) -> float:
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise InputError(f"field {field!r} must be a number, got {value!r}")
    return float(value)


def parse_instance(data: Mapping[str, Any]) -> Instance:
    """Validate and build an :class:`Instance` from a JSON-like mapping."""

    if not isinstance(data, Mapping):
        raise InputError("top-level input must be a JSON object")

    horizon = _as_int(_require(data, "horizon", "root"), "horizon")
    if horizon < 1:
        raise InputError("field 'horizon' must be >= 1")

    raw_notifications = _require(data, "notifications", "root")
    if not isinstance(raw_notifications, list):
        raise InputError("field 'notifications' must be a list")

    notifications: list[Notification] = []
    seen_ids: set[str] = set()
    for index, raw in enumerate(raw_notifications):
        where = f"notifications[{index}]"
        if not isinstance(raw, Mapping):
            raise InputError(f"{where} must be an object")
        nid = str(_require(raw, "id", where))
        if nid in seen_ids:
            raise InputError(f"duplicate notification id {nid!r}")
        seen_ids.add(nid)
        release = _as_int(_require(raw, "release", where), f"{where}.release")
        deadline = _as_int(_require(raw, "deadline", where), f"{where}.deadline")
        if release < 0 or deadline < 0:
            raise InputError(f"{where}: release/deadline must be >= 0")
        if release > deadline:
            raise InputError(f"{where}: release must be <= deadline")
        if deadline >= horizon:
            raise InputError(f"{where}: deadline must be < horizon ({horizon})")
        notifications.append(
            Notification(
                nid=nid,
                user=str(_require(raw, "user", where)),
                channel=str(_require(raw, "channel", where)),
                revenue=_as_number(_require(raw, "revenue", where), f"{where}.revenue"),
                release=release,
                deadline=deadline,
                cost=_as_number(_require(raw, "cost", where), f"{where}.cost"),
                activity_id=(
                    str(raw["activity_id"]) if raw.get("activity_id") is not None else None
                ),
            )
        )

    raw_capacity = data.get("channel_capacity", {})
    if not isinstance(raw_capacity, Mapping):
        raise InputError("field 'channel_capacity' must be an object")
    channel_capacity = {
        str(ch): _as_number(cap, f"channel_capacity.{ch}")
        for ch, cap in raw_capacity.items()
    }

    window_length = _as_int(data.get("window_length", 3), "window_length")
    if window_length < 1:
        raise InputError("field 'window_length' must be >= 1")
    max_pushes = _as_int(data.get("max_pushes_per_window", 1), "max_pushes_per_window")
    if max_pushes < 0:
        raise InputError("field 'max_pushes_per_window' must be >= 0")

    return Instance(
        horizon=horizon,
        notifications=tuple(notifications),
        channel_capacity=dict(channel_capacity),
        window_length=window_length,
        max_pushes_per_window=max_pushes,
    )
