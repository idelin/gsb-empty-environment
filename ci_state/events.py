"""Build-event model.

Supported event kinds (all carry ``id``, ``node``, ``attempt``, ``ts``):

* ``node.started``        - attempt execution began
* ``artifact.written``    - an artifact was produced, additionally ``artifact``
* ``artifact.committed``  - an artifact was committed to the output store,
                            additionally ``artifact`` (optionally a list via
                            ``artifacts``)
* ``node.succeeded``      - the worker reported success
* ``node.failed``         - the worker reported failure, optional ``reason``

Events may arrive in any order; the engine is responsible for ordering them
logically. The same event ``id`` seen more than once is a duplicate and must
have no effect beyond the first application.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, FrozenSet, Optional

NODE_STARTED = "node.started"
ARTIFACT_WRITTEN = "artifact.written"
ARTIFACT_COMMITTED = "artifact.committed"
NODE_SUCCEEDED = "node.succeeded"
NODE_FAILED = "node.failed"

EVENT_KINDS = frozenset(
    {
        NODE_STARTED,
        ARTIFACT_WRITTEN,
        ARTIFACT_COMMITTED,
        NODE_SUCCEEDED,
        NODE_FAILED,
    }
)
ARTIFACT_KINDS = frozenset({ARTIFACT_WRITTEN, ARTIFACT_COMMITTED})


class EventError(ValueError):
    """Raised when a raw event mapping is malformed."""


def _require(data: dict, key: str) -> Any:
    if key not in data:
        raise EventError(f"event missing required field: {key!r}")
    return data[key]


def _artifact_names(raw: Any, kind: str) -> FrozenSet[str]:
    if isinstance(raw, str):
        names = [raw]
    elif isinstance(raw, list) and raw and all(isinstance(x, str) for x in raw):
        names = list(raw)
    else:
        raise EventError(
            f"{kind} event requires a string 'artifact' (or non-empty "
            "'artifacts' list of strings)"
        )
    cleaned = [name.strip() for name in names]
    if any(not name for name in cleaned):
        raise EventError(f"{kind} event contains an empty artifact name")
    return frozenset(cleaned)


@dataclass(frozen=True)
class Event:
    """A single immutable build event."""

    id: str
    kind: str
    node: str
    attempt: int
    ts: float
    artifacts: FrozenSet[str] = field(default_factory=frozenset)
    reason: Optional[str] = None
    raw: dict = field(default_factory=dict, compare=False, repr=False)

    @classmethod
    def from_dict(cls, data: dict) -> "Event":
        if not isinstance(data, dict):
            raise EventError(f"event must be an object, got {type(data).__name__}")
        event_id = _require(data, "id")
        kind = _require(data, "kind")
        node = _require(data, "node")
        attempt = _require(data, "attempt")
        ts = _require(data, "ts")
        if not isinstance(event_id, str) or not event_id.strip():
            raise EventError("event 'id' must be a non-empty string")
        if not isinstance(kind, str) or kind not in EVENT_KINDS:
            raise EventError(
                f"event {event_id!r}: unknown kind {kind!r}; "
                f"expected one of {sorted(EVENT_KINDS)}"
            )
        if not isinstance(node, str) or not node.strip():
            raise EventError(f"event {event_id!r}: 'node' must be a non-empty string")
        if not isinstance(attempt, int) or isinstance(attempt, bool) or attempt < 1:
            raise EventError(
                f"event {event_id!r}: 'attempt' must be a positive integer"
            )
        if isinstance(ts, bool) or not isinstance(ts, (int, float)):
            raise EventError(f"event {event_id!r}: 'ts' must be a number")
        artifacts = frozenset()
        if kind in ARTIFACT_KINDS:
            if "artifact" in data:
                artifacts = _artifact_names(data["artifact"], kind)
            elif "artifacts" in data:
                artifacts = _artifact_names(data["artifacts"], kind)
            else:
                raise EventError(
                    f"event {event_id!r}: {kind} requires 'artifact'"
                )
        reason = data.get("reason")
        if reason is not None and not isinstance(reason, str):
            raise EventError(f"event {event_id!r}: 'reason' must be a string")
        return cls(
            id=event_id,
            kind=kind,
            node=node,
            attempt=attempt,
            ts=float(ts),
            artifacts=artifacts,
            reason=reason,
            raw=dict(data),
        )
