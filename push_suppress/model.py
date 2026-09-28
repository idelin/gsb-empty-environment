"""Data model and JSON parsing for the push suppression problem.

Time is discrete and finite. A notification may be sent at any integer
time ``t`` with ``start <= t <= end`` (both bounds inclusive).

Notification IDs within one instance must be mutually comparable
(all ``str`` or all ``int``); they are used for deterministic
tie-breaking.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Dict, Hashable, List, Optional


@dataclass(frozen=True)
class Notification:
    """A candidate notification that may be sent at most once."""

    id: Hashable
    user: str
    channel: str
    profit: float
    start: int
    end: int  # inclusive
    cost: float = 0.0
    mutex_group: Optional[str] = None  # campaign ID; at most one send per group

    def times(self):
        return range(self.start, self.end + 1)


@dataclass(frozen=True)
class Send:
    """One scheduled send: notification ``notification_id`` at ``time``."""

    notification_id: Hashable
    time: int


@dataclass
class Instance:
    """A problem instance.

    Attributes:
        notifications: candidate notifications.
        window_length: length L of the per-user rolling half-open window.
        window_max_sends: at most this many sends to one user in any
            half-open interval ``[t, t + L)``.
        channel_capacity: per-channel per-time-slot send capacity.
            Channels missing from the mapping are unlimited.
        budget: optional global budget on the sum of send costs
            (``None`` means unlimited).
        time_horizon: optional number of discrete time slots ``[0, H)``;
            when given, every send time must lie inside it.
    """

    notifications: List[Notification]
    window_length: int = 3
    window_max_sends: int = 1
    channel_capacity: Dict[str, int] = field(default_factory=dict)
    budget: Optional[float] = None
    time_horizon: Optional[int] = None

    def by_id(self) -> Dict[Hashable, Notification]:
        return {n.id: n for n in self.notifications}


def _require(cond: bool, msg: str) -> None:
    if not cond:
        raise ValueError(msg)


def parse_instance(data: Dict[str, Any]) -> Instance:
    """Parse and validate an instance from a decoded JSON object."""
    _require(isinstance(data, dict), "instance must be a JSON object")
    raw_notifications = data.get("notifications")
    _require(isinstance(raw_notifications, list), "'notifications' must be a list")

    notifications: List[Notification] = []
    seen_ids = set()
    for i, raw in enumerate(raw_notifications):
        _require(isinstance(raw, dict), f"notifications[{i}] must be an object")
        for key in ("id", "user", "channel", "profit", "window"):
            _require(key in raw, f"notifications[{i}] missing '{key}'")
        window = raw["window"]
        _require(
            isinstance(window, list) and len(window) == 2,
            f"notifications[{i}].window must be [start, end]",
        )
        start, end = window
        _require(
            isinstance(start, int) and isinstance(end, int) and start <= end,
            f"notifications[{i}].window must be integers with start <= end",
        )
        nid = raw["id"]
        _require(
            isinstance(nid, (str, int)) and not isinstance(nid, bool),
            f"notifications[{i}].id must be a string or integer",
        )
        _require(nid not in seen_ids, f"duplicate notification id {nid!r}")
        seen_ids.add(nid)
        notifications.append(
            Notification(
                id=nid,
                user=str(raw["user"]),
                channel=str(raw["channel"]),
                profit=float(raw["profit"]),
                start=start,
                end=end,
                cost=float(raw.get("cost", 0.0)),
                mutex_group=(
                    None if raw.get("mutex_group") in (None, "")
                    else str(raw["mutex_group"])
                ),
            )
        )

    window_cfg = data.get("user_window", {})
    _require(isinstance(window_cfg, dict), "'user_window' must be an object")
    window_length = int(window_cfg.get("length", 3))
    window_max_sends = int(window_cfg.get("max_sends", 1))
    _require(window_length >= 1, "user_window.length must be >= 1")
    _require(window_max_sends >= 1, "user_window.max_sends must be >= 1")

    capacity = data.get("channel_capacity", {})
    _require(isinstance(capacity, dict), "'channel_capacity' must be an object")
    channel_capacity = {}
    for channel, cap in capacity.items():
        cap = int(cap)
        _require(cap >= 0, f"capacity for channel {channel!r} must be >= 0")
        channel_capacity[str(channel)] = cap

    budget = data.get("budget")
    if budget is not None:
        budget = float(budget)
        _require(budget >= 0, "budget must be >= 0")

    horizon = data.get("time_horizon")
    if horizon is not None:
        horizon = int(horizon)
        _require(horizon >= 1, "time_horizon must be >= 1")
        for n in notifications:
            _require(
                0 <= n.start and n.end < horizon,
                f"notification {n.id!r} window [{n.start}, {n.end}] "
                f"outside time horizon [0, {horizon})",
            )

    return Instance(
        notifications=notifications,
        window_length=window_length,
        window_max_sends=window_max_sends,
        channel_capacity=channel_capacity,
        budget=budget,
        time_horizon=horizon,
    )


def parse_sends(data: Any) -> List[Send]:
    """Parse a schedule from a decoded JSON object.

    Accepts either ``{"sends": [{"id": ..., "time": ...}, ...]}`` or a
    bare list of ``{"id": ..., "time": ...}`` objects.
    """
    if isinstance(data, dict):
        data = data.get("sends", [])
    _require(isinstance(data, list), "schedule must be a list or {'sends': [...]}")
    sends: List[Send] = []
    for i, raw in enumerate(data):
        _require(isinstance(raw, dict), f"sends[{i}] must be an object")
        _require("id" in raw and "time" in raw, f"sends[{i}] needs 'id' and 'time'")
        t = raw["time"]
        _require(isinstance(t, int) and not isinstance(t, bool),
                 f"sends[{i}].time must be an integer")
        sends.append(Send(notification_id=raw["id"], time=t))
    return sends
