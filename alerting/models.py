from __future__ import annotations

import enum
import math
from dataclasses import dataclass


class FaultState(str, enum.Enum):
    """Truth-layer state of a rule at a given event time."""

    OK = "ok"
    PENDING = "pending"
    FIRING = "firing"
    SUPPRESSED = "suppressed"
    RESOLVED = "resolved"


class NotificationKind(str, enum.Enum):
    """Notification-layer event kinds (append-only)."""

    OPEN = "OPEN"          # alert started firing (and is not suppressed)
    RESOLVED = "RESOLVED"  # alert recovered
    REVOKED = "REVOKED"    # correction: a previous notification was wrong


@dataclass(frozen=True)
class Sample:
    """A metric sample keyed by event time.

    ``seq`` is the ingestion sequence; for two samples sharing the same
    ``event_time`` the one with the higher ``seq`` wins (late correction).
    """

    event_time: float
    value: float
    seq: int = 0


@dataclass(frozen=True)
class RuleVersion:
    """One immutable version of an alerting rule."""

    rule_id: str
    version: int
    enter_threshold: float
    exit_threshold: float
    enter_duration: float
    exit_duration: float = 0.0


@dataclass(frozen=True)
class SuppressionWindow:
    """One immutable version of a suppression rule.

    ``rule_id=None`` suppresses every rule.  Covers [start, end).
    """

    sup_id: str
    rule_id: str | None
    start: float
    end: float
    version: int = 1

    def covers(self, rule_id: str, t: float) -> bool:
        return (self.rule_id is None or self.rule_id == rule_id) and self.start <= t < self.end


@dataclass(frozen=True)
class Episode:
    """A firing interval of the truth layer. ``resolve_time=None`` = still firing."""

    rule_id: str
    fire_start: float
    resolve_time: float | None

    @property
    def end(self) -> float:
        return math.inf if self.resolve_time is None else self.resolve_time


@dataclass(frozen=True)
class DesiredNotification:
    """What the notification layer *should* have told the world."""

    rule_id: str
    fire_start: float
    open_time: float  # may be deferred past a suppression window
    resolve_time: float | None
