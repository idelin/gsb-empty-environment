"""Build events.

Every event carries a unique ``event_id`` so re-submitted events can be
applied idempotently.  Events may arrive in any order and may be duplicated;
the reducer derives state from the *set* of events, never from arrival order.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Mapping

EVENT_TYPES = frozenset(
    {
        "node_started",
        "artifact_written",
        "committed",
        "build_succeeded",
        "build_failed",
    }
)


class EventError(ValueError):
    """Raised when an event record is malformed."""


@dataclass(frozen=True)
class Event:
    event_id: str
    type: str
    node: str
    attempt: int
    payload: Mapping[str, Any] = field(default_factory=dict)

    @classmethod
    def from_dict(cls, raw: Mapping[str, Any]) -> "Event":
        if not isinstance(raw, Mapping):
            raise EventError(f"event must be an object, got {type(raw).__name__}")

        event_id = raw.get("event_id")
        if not isinstance(event_id, str) or not event_id:
            raise EventError("event requires a non-empty string 'event_id'")

        etype = raw.get("type")
        if etype not in EVENT_TYPES:
            raise EventError(
                f"event {event_id!r}: unknown type {etype!r}; "
                f"expected one of {sorted(EVENT_TYPES)}"
            )

        node = raw.get("node")
        if not isinstance(node, str) or not node:
            raise EventError(f"event {event_id!r}: requires a non-empty string 'node'")

        attempt = raw.get("attempt")
        if not isinstance(attempt, int) or isinstance(attempt, bool) or attempt < 1:
            raise EventError(f"event {event_id!r}: 'attempt' must be an integer >= 1")

        payload = raw.get("payload", {})
        if not isinstance(payload, Mapping):
            raise EventError(f"event {event_id!r}: 'payload' must be an object")

        if etype == "artifact_written":
            artifact = payload.get("artifact")
            if not isinstance(artifact, str) or not artifact:
                raise EventError(
                    f"event {event_id!r}: artifact_written requires payload.artifact"
                )
        elif etype == "committed":
            commit = payload.get("commit")
            if not isinstance(commit, str) or not commit:
                raise EventError(
                    f"event {event_id!r}: committed requires payload.commit"
                )

        return cls(
            event_id=event_id,
            type=etype,
            node=node,
            attempt=attempt,
            payload=dict(payload),
        )
