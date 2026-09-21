"""Build events and JSONL event-log loading.

Event kinds, in build-lifecycle order:

* ``started``           - a node attempt began
* ``artifacts_written`` - one or more artifacts were produced (with sha256)
* ``submitted``         - the attempt claims terminal success
* ``failed``            - the attempt reached terminal failure

Events carry an ``attempt`` index: each node may be retried with a higher
attempt number.  ``event_id`` identifies a single physical event and is used
for idempotent de-duplication.
"""

from __future__ import annotations

import json
from collections.abc import Iterable, Mapping
from dataclasses import dataclass
from pathlib import Path


STARTED = "started"
ARTIFACTS_WRITTEN = "artifacts_written"
SUBMITTED = "submitted"
FAILED = "failed"
EVENT_KINDS = frozenset({STARTED, ARTIFACTS_WRITTEN, SUBMITTED, FAILED})


class EventError(ValueError):
    """Raised when an event record cannot be parsed."""


@dataclass(frozen=True)
class Artifact:
    name: str
    sha256: str


@dataclass(frozen=True)
class Event:
    event_id: str
    node: str
    attempt: int
    kind: str
    timestamp: str
    artifacts: tuple[Artifact, ...] = ()
    reason: str = ""

    @property
    def identity(self) -> tuple[str, str, int, str]:
        """Business identity used to recognise the same event repeated."""
        return (self.event_id, self.node, self.attempt, self.kind)


_HEX64 = set("0123456789abcdefABCDEF")


def _field(data: Mapping[str, object], key: str, context: str) -> object:
    if key not in data:
        raise EventError(f"{context} is missing {key!r}")
    return data[key]


def _parse_artifacts(value: object, context: str) -> tuple[Artifact, ...]:
    if not isinstance(value, list):
        raise EventError(f"{context} artifacts must be an array")
    if not value:
        raise EventError(f"{context} artifacts_written must list at least one artifact")
    artifacts = []
    seen = set()
    for index, item in enumerate(value):
        where = f"{context} artifact #{index}"
        if not isinstance(item, Mapping):
            raise EventError(f"{where} must be an object")
        if set(item) != {"name", "sha256"}:
            raise EventError(f"{where} must have only name and sha256")
        name, digest = item["name"], item["sha256"]
        if not isinstance(name, str) or not name:
            raise EventError(f"{where} name must be a non-empty string")
        if name in seen:
            raise EventError(f"{where} name {name!r} is repeated within the event")
        seen.add(name)
        if not isinstance(digest, str) or len(digest) != 64 or any(c not in _HEX64 for c in digest):
            raise EventError(f"{where} sha256 must be a 64-character hex string")
        artifacts.append(Artifact(name=name, sha256=digest.lower()))
    return tuple(artifacts)


def parse_event(raw: Mapping[str, object]) -> Event:
    if not isinstance(raw, Mapping):
        raise EventError("event must be a JSON object")
    unknown = set(raw) - {
        "event_id", "node", "attempt", "kind", "timestamp", "artifacts", "reason",
    }
    if unknown:
        raise EventError(f"event has unknown fields: {sorted(unknown)}")

    event_id = _field(raw, "event_id", "event")
    node = _field(raw, "node", "event")
    attempt = _field(raw, "attempt", "event")
    kind = _field(raw, "kind", "event")
    timestamp = _field(raw, "timestamp", "event")

    if not isinstance(event_id, str) or not event_id:
        raise EventError("event_id must be a non-empty string")
    if not isinstance(node, str) or not node:
        raise EventError("node must be a non-empty string")
    if isinstance(attempt, bool) or not isinstance(attempt, int) or attempt < 1:
        raise EventError("attempt must be an integer >= 1")
    if kind not in EVENT_KINDS:
        raise EventError(f"kind must be one of {sorted(EVENT_KINDS)}")
    if not isinstance(timestamp, str) or not timestamp:
        raise EventError("timestamp must be a non-empty string")

    artifacts: tuple[Artifact, ...] = ()
    if kind == ARTIFACTS_WRITTEN:
        artifacts = _parse_artifacts(_field(raw, "artifacts", f"event {event_id!r}"),
                                     f"event {event_id!r}")
    elif "artifacts" in raw:
        raise EventError(f"event {event_id!r} of kind {kind!r} must not carry artifacts")

    reason = raw.get("reason", "")
    if kind == FAILED:
        if not isinstance(reason, str) or not reason:
            raise EventError(f"event {event_id!r} failed requires a non-empty reason")
    elif reason:
        raise EventError(f"event {event_id!r} of kind {kind!r} must not carry a reason")

    return Event(
        event_id=event_id,
        node=node,
        attempt=attempt,
        kind=kind,
        timestamp=timestamp,
        artifacts=artifacts,
        reason=reason,
    )


def iter_event_lines(path: str | Path) -> Iterable[tuple[int, Mapping[str, object]]]:
    """Yield ``(line_number, raw_object)`` pairs from a JSONL file."""
    source = Path(path)
    with source.open("r", encoding="utf-8") as handle:
        for line_number, line in enumerate(handle, start=1):
            if not line.strip():
                continue
            try:
                raw = json.loads(line)
            except json.JSONDecodeError as exc:
                raise EventError(f"{source}:{line_number}: invalid JSON: {exc}") from None
            if not isinstance(raw, Mapping):
                raise EventError(f"{source}:{line_number}: event must be a JSON object")
            yield line_number, raw
